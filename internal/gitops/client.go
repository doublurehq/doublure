// Package gitops provides read/write operations for the gitops repository
// hosting preview environment values.yaml files.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	gh "github.com/google/go-github/v71/github"
	"gopkg.in/yaml.v3"

	"github.com/doublurehq/doublure/internal/preview"
)

// PRCommentMarker is the HTML comment used as a sentinel for find-or-create
// semantics on PR comments.
const PRCommentMarker = "<!-- doublure:preview -->"

// MaxWriteRetries is the cap on 409-conflict retries for WritePreviewWithRetry.
const MaxWriteRetries = 3

// Sentinel errors. Callers should test with errors.Is.
var (
	ErrNotFound     = errors.New("gitops: not found")
	ErrUnauthorized = errors.New("gitops: unauthorized")
)

// Client wraps a go-github client scoped to a specific gitops owner/repo.
type Client struct {
	gh    *gh.Client
	owner string
	repo  string
}

// NewClient binds the gh client to owner/repo. owner is the GitHub
// organization or user; repo is the bare repository name.
func NewClient(client *gh.Client, owner, repo string) *Client {
	return &Client{gh: client, owner: owner, repo: repo}
}

// classifyHTTPError translates a go-github response into our sentinel errors
// for status codes the caller cares about. Other errors pass through unchanged.
func classifyHTTPError(resp *gh.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp == nil {
		return err
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	return err
}

// FetchServiceCatalog reads services.yaml from the gitops repo and returns
// the keys of its serviceRepos mapping (the canonical service catalog) and its
// validated `preview` block. A malformed `preview` block is an error.
func (c *Client) FetchServiceCatalog(ctx context.Context) (*ServiceCatalog, error) {
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, "services.yaml", nil)
	if err != nil {
		return nil, classifyHTTPError(resp, err)
	}
	if content == nil {
		return nil, fmt.Errorf("services.yaml: not a file")
	}
	raw, err := content.GetContent()
	if err != nil {
		return nil, fmt.Errorf("decode services.yaml: %w", err)
	}
	return ParseServices([]byte(raw))
}

// previewsDir is the gitops repo directory holding one subdirectory per preview.
const previewsDir = "previews"

// ListPreviews returns the sorted names of the directories under previews/.
// A missing previews/ directory yields an empty slice. The contents API caps
// a directory listing at 1,000 entries; this is not paginated.
func (c *Client) ListPreviews(ctx context.Context) ([]string, error) {
	_, entries, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, previewsDir, nil)
	if err != nil {
		err = classifyHTTPError(resp, err)
		if errors.Is(err, ErrNotFound) {
			return []string{}, nil
		}
		return nil, err
	}
	slugs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.GetType() == "dir" {
			slugs = append(slugs, e.GetName())
		}
	}
	slices.Sort(slugs)
	return slugs, nil
}

// PullRequestOpen reports whether owner/repo#number is open. A merged PR is
// closed. Lookup failures (including 404 and 403) are returned as errors and
// never reported as closed.
func (c *Client) PullRequestOpen(ctx context.Context, owner, repo string, number int) (bool, error) {
	pr, resp, err := c.gh.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		return false, classifyHTTPError(resp, err)
	}
	return pr.GetState() == "open", nil
}

// previewPath returns the gitops repo path for a preview slug's values file.
func previewPath(slug string) string {
	return fmt.Sprintf("previews/%s/values.yaml", slug)
}

// GetPreview reads previews/<slug>/values.yaml and returns the parsed values
// plus the file SHA (needed for subsequent updates). Returns ErrNotFound when
// the file does not exist.
func (c *Client) GetPreview(ctx context.Context, slug string) (*preview.PreviewValues, string, error) {
	content, _, resp, err := c.gh.Repositories.GetContents(ctx, c.owner, c.repo, previewPath(slug), nil)
	if err != nil {
		return nil, "", classifyHTTPError(resp, err)
	}
	if content == nil {
		return nil, "", fmt.Errorf("%s: not a file", previewPath(slug))
	}
	raw, err := content.GetContent()
	if err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", previewPath(slug), err)
	}
	var vals preview.PreviewValues
	if err := yaml.Unmarshal([]byte(raw), &vals); err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", previewPath(slug), err)
	}
	return &vals, content.GetSHA(), nil
}

// marshalPreview encodes vals with the same indentation rules used for the
// golden fixture (2-space indent).
func marshalPreview(vals preview.PreviewValues) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(vals); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WritePreview persists vals at previews/<slug>/values.yaml. When sha is empty
// the file is created; otherwise an update is performed against that SHA.
// Returns the html_url of the resulting commit.
func (c *Client) WritePreview(ctx context.Context, slug string, vals preview.PreviewValues, sha, message string) (string, error) {
	body, err := marshalPreview(vals)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	opts := &gh.RepositoryContentFileOptions{
		Message: gh.Ptr(message),
		Content: body,
	}
	if sha != "" {
		opts.SHA = gh.Ptr(sha)
	}
	// Create and update are the same contents PUT; sha is only set on update.
	out, resp, writeErr := c.gh.Repositories.UpdateFile(ctx, c.owner, c.repo, previewPath(slug), opts)
	if writeErr != nil {
		return "", classifyHTTPError(resp, writeErr)
	}
	if out == nil {
		return "", fmt.Errorf("write %s: empty response", previewPath(slug))
	}
	return out.Commit.GetHTMLURL(), nil
}

// statusCode pulls the HTTP status code out of a go-github error. Returns 0
// when the error is not an HTTP error.
func statusCode(err error) int {
	var ghErr *gh.ErrorResponse
	if errors.As(err, &ghErr) && ghErr.Response != nil {
		return ghErr.Response.StatusCode
	}
	return 0
}

// WritePreviewWithRetry composes Get → (Build|Update)PreviewValues → Write and
// retries up to MaxWriteRetries times on 409 conflicts (someone else updated
// the file between our read and write). On every retry the latest file is
// re-fetched and the deploy is re-applied via preview.UpdatePreviewValues, so
// the merge is always against the freshest baseline.
//
// On a fresh create the full catalog is materialized via BuildPreviewValues so
// the resulting values.yaml lists every service (the triggering one pinned to
// commitSha, the rest tracking main).
//
// WriteResult is the outcome of a successful WritePreviewWithRetry call.
// CommitMessage is whichever of {createMessage, updateMessage} was actually
// used for the gitops commit, so callers can label PR summaries consistently
// with what GitHub shows when the user clicks through to the commit.
type WriteResult struct {
	Values        preview.PreviewValues
	CommitURL     string
	CommitMessage string
}

// Returns a WriteResult capturing the final PreviewValues that was written,
// the gitops commit URL, and the commit message used — callers (e.g. the
// deploy handler) can render PR summaries without re-fetching.
func (c *Client) WritePreviewWithRetry(
	ctx context.Context,
	slug string,
	catalog []string,
	serviceName, commitSha string,
	inputs preview.DeployInputs,
	createMessage, updateMessage string,
) (*WriteResult, error) {
	existing, sha, err := c.GetPreview(ctx, slug)
	exists := err == nil
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	var vals preview.PreviewValues
	var message string
	if exists {
		vals = preview.UpdatePreviewValues(*existing, serviceName, commitSha, inputs)
		message = updateMessage
	} else {
		vals = preview.BuildPreviewValues(serviceName, commitSha, catalog, inputs)
		message = createMessage
	}

	for attempt := 1; attempt <= MaxWriteRetries; attempt++ {
		commitURL, writeErr := c.WritePreview(ctx, slug, vals, sha, message)
		if writeErr == nil {
			return &WriteResult{Values: vals, CommitURL: commitURL, CommitMessage: message}, nil
		}
		if statusCode(writeErr) != http.StatusConflict {
			return nil, writeErr
		}
		// 409 — re-fetch, re-merge, retry.
		latest, latestSHA, getErr := c.GetPreview(ctx, slug)
		if getErr != nil {
			return nil, fmt.Errorf("conflict re-fetch: %w", getErr)
		}
		sha = latestSHA
		vals = preview.UpdatePreviewValues(*latest, serviceName, commitSha, inputs)
		message = updateMessage
	}
	return nil, fmt.Errorf("write %s: %d attempts exhausted", previewPath(slug), MaxWriteRetries)
}

// TeardownResult is the outcome of TeardownPreviewWithRetry. Removed lists the
// services unpinned from the closed PR; it is empty when there was nothing to
// tear down. Values holds the written file and is set only when Deleted is
// false.
type TeardownResult struct {
	Values        preview.PreviewValues
	Removed       []string
	Deleted       bool
	CommitURL     string
	CommitMessage string
}

// TeardownPreviewWithRetry unpins every service of previews/<slug>/values.yaml
// deployed by the PR owner/repo#prNumber. The file is updated when other pins
// remain and deleted otherwise. On a 409 conflict the file is re-fetched, the
// unpin re-applied and update vs delete decided again, up to MaxWriteRetries
// attempts. A missing file (on read, re-fetch or delete) or a PR that pinned
// nothing is a no-op: an empty TeardownResult and a nil error.
func (c *Client) TeardownPreviewWithRetry(
	ctx context.Context,
	slug, owner, repo string,
	prNumber int,
) (*TeardownResult, error) {
	existing, sha, err := c.GetPreview(ctx, slug)
	if errors.Is(err, ErrNotFound) {
		return &TeardownResult{}, nil
	}
	if err != nil {
		return nil, err
	}

	for attempt := 1; attempt <= MaxWriteRetries; attempt++ {
		vals, removed, pinned := preview.UnpinPR(*existing, owner, repo, prNumber)
		if len(removed) == 0 {
			return &TeardownResult{}, nil
		}
		result := &TeardownResult{Removed: removed, Deleted: !pinned}
		var writeErr error
		if pinned {
			result.Values = vals
			result.CommitMessage = fmt.Sprintf("chore(preview): remove %s from %s", strings.Join(removed, ", "), slug)
			result.CommitURL, writeErr = c.WritePreview(ctx, slug, vals, sha, result.CommitMessage)
		} else {
			result.CommitMessage = fmt.Sprintf("chore(preview): delete %s preview", slug)
			result.CommitURL, writeErr = c.deletePreview(ctx, slug, sha, result.CommitMessage)
			if errors.Is(writeErr, ErrNotFound) {
				return &TeardownResult{}, nil
			}
		}
		if writeErr == nil {
			return result, nil
		}
		if statusCode(writeErr) != http.StatusConflict {
			return nil, writeErr
		}
		// 409 — re-fetch, re-apply the unpin, decide again.
		existing, sha, err = c.GetPreview(ctx, slug)
		if errors.Is(err, ErrNotFound) {
			return &TeardownResult{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("conflict re-fetch: %w", err)
		}
	}
	return nil, fmt.Errorf("teardown %s: %d attempts exhausted", previewPath(slug), MaxWriteRetries)
}

// DeleteResult is the outcome of DeletePreviewWithRetry. Deleted is false
// when nothing was deleted; Values then is empty. Values holds the content
// that was deleted.
type DeleteResult struct {
	Deleted   bool
	Values    preview.PreviewValues
	CommitURL string
}

// DeletePreviewWithRetry deletes previews/<slug>/values.yaml with message,
// provided stillDelete holds on the file read just before the delete. On a
// 409 conflict the file is re-fetched and stillDelete re-checked, up to
// MaxWriteRetries attempts. A stillDelete returning false, or a missing file
// (on read, re-fetch or delete), deletes nothing: an empty DeleteResult and a
// nil error.
func (c *Client) DeletePreviewWithRetry(
	ctx context.Context,
	slug, message string,
	stillDelete func(preview.PreviewValues) bool,
) (*DeleteResult, error) {
	existing, sha, err := c.GetPreview(ctx, slug)
	if errors.Is(err, ErrNotFound) {
		return &DeleteResult{}, nil
	}
	if err != nil {
		return nil, err
	}

	for attempt := 1; attempt <= MaxWriteRetries; attempt++ {
		if !stillDelete(*existing) {
			return &DeleteResult{}, nil
		}
		commitURL, delErr := c.deletePreview(ctx, slug, sha, message)
		if delErr == nil {
			return &DeleteResult{Deleted: true, Values: *existing, CommitURL: commitURL}, nil
		}
		if errors.Is(delErr, ErrNotFound) {
			return &DeleteResult{}, nil
		}
		if statusCode(delErr) != http.StatusConflict {
			return nil, delErr
		}
		// 409 — re-fetch and re-check before trying again.
		existing, sha, err = c.GetPreview(ctx, slug)
		if errors.Is(err, ErrNotFound) {
			return &DeleteResult{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("conflict re-fetch: %w", err)
		}
	}
	return nil, fmt.Errorf("delete %s: %d attempts exhausted", previewPath(slug), MaxWriteRetries)
}

// FanOutPRComments posts (or updates) the same markdown body as a comment on
// every PR referenced in vals.Services.Metadata.pr-url. Failures on individual
// services are collected but do not abort the loop — best-effort delivery.
//
// Find-or-create semantics: the comment body is prefixed with PRCommentMarker
// (HTML comment), so subsequent runs find and update the existing comment
// rather than spamming new ones.
func (c *Client) FanOutPRComments(ctx context.Context, vals preview.PreviewValues, body string) error {
	var errs []error
	for _, svc := range vals.Services {
		if svc.Metadata == nil || svc.Metadata.PRURL == "" {
			continue
		}
		prURL := svc.Metadata.PRURL
		owner, repo, prNumber, ok := preview.ParsePRURL(prURL)
		if !ok {
			errs = append(errs, fmt.Errorf("service %s: unrecognized pr-url %q", svc.Name, prURL))
			continue
		}
		if upsertErr := c.UpsertPRComment(ctx, owner, repo, prNumber, body); upsertErr != nil {
			errs = append(errs, fmt.Errorf("service %s (%s/%s#%d): %w", svc.Name, owner, repo, prNumber, upsertErr))
		}
	}
	return errors.Join(errs...)
}

// UpsertPRComment posts body as doublure's comment on owner/repo#prNumber,
// prefixed with PRCommentMarker: the existing marked comment is updated, or a
// new one is created.
func (c *Client) UpsertPRComment(ctx context.Context, owner, repo string, prNumber int, body string) error {
	return c.upsertPRComment(ctx, owner, repo, prNumber, PRCommentMarker+"\n"+body)
}

// upsertPRComment finds an existing comment by marker and updates it, or
// creates a new one if none is found. Paginates through all comments.
func (c *Client) upsertPRComment(ctx context.Context, owner, repo string, prNumber int, body string) error {
	opts := &gh.IssueListCommentsOptions{ListOptions: gh.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := c.gh.Issues.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return classifyHTTPError(resp, err)
		}
		for _, comment := range comments {
			if comment.Body == nil || !strings.Contains(*comment.Body, PRCommentMarker) {
				continue
			}
			_, editResp, editErr := c.gh.Issues.EditComment(ctx, owner, repo, comment.GetID(), &gh.IssueComment{Body: gh.Ptr(body)})
			if editErr != nil {
				return classifyHTTPError(editResp, editErr)
			}
			return nil
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	_, createResp, createErr := c.gh.Issues.CreateComment(ctx, owner, repo, prNumber, &gh.IssueComment{Body: gh.Ptr(body)})
	if createErr != nil {
		return classifyHTTPError(createResp, createErr)
	}
	return nil
}

// deletePreview removes previews/<slug>/values.yaml at blob sha and returns
// the html_url of the resulting commit.
func (c *Client) deletePreview(ctx context.Context, slug, sha, message string) (string, error) {
	opts := &gh.RepositoryContentFileOptions{
		Message: gh.Ptr(message),
		SHA:     gh.Ptr(sha),
	}
	out, resp, err := c.gh.Repositories.DeleteFile(ctx, c.owner, c.repo, previewPath(slug), opts)
	if err != nil {
		return "", classifyHTTPError(resp, err)
	}
	if out == nil {
		return "", fmt.Errorf("delete %s: empty response", previewPath(slug))
	}
	return out.Commit.GetHTMLURL(), nil
}
