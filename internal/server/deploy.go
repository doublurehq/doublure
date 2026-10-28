package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/preview"
)

// deployTimeout caps the upstream work for a single deploy. Catalog + read +
// write + fan-out comments add up to ~4-8 GitHub round-trips; 30s is generous
// without letting a stuck request pin a goroutine forever.
const deployTimeout = 30 * time.Second

// gitopsAPI is the subset of gitops.Client used by the deploy and teardown
// handlers. Defined here so tests can inject fakes without spinning up an
// httptest server for every GitHub round-trip.
type gitopsAPI interface {
	FetchServiceCatalog(ctx context.Context) (*gitops.ServiceCatalog, error)
	WritePreviewWithRetry(
		ctx context.Context,
		slug string,
		catalog []string,
		serviceName, commitSha string,
		inputs preview.DeployInputs,
		createMessage, updateMessage string,
	) (*gitops.WriteResult, error)
	FanOutPRComments(ctx context.Context, vals preview.PreviewValues, body string) error
	TeardownPreviewWithRetry(
		ctx context.Context,
		slug, owner, repo string,
		prNumber int,
	) (*gitops.TeardownResult, error)
	UpsertPRComment(ctx context.Context, owner, repo string, prNumber int, body string) error
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*gitops.PullRequest, error)
}

// DeployRequest is the body of POST /v1/deploy. All fields are required; the
// server stamps timestamp from time.Now() so retries don't drift.
type DeployRequest struct {
	Service        string `json:"service"`
	HeadRef        string `json:"head_ref"`
	CommitSha      string `json:"commit_sha"`
	PRAuthor       string `json:"pr_author"`
	PRURL          string `json:"pr_url"`
	PRNumber       string `json:"pr_number"`
	WorkflowRunURL string `json:"workflow_run_url"`
}

// DeployResponse is the success body. Warnings is populated when the values
// write succeeded but the comment fan-out had partial failures.
type DeployResponse struct {
	PreviewSlug     string   `json:"preview_slug"`
	GitOpsCommitURL string   `json:"gitops_commit_url"`
	Warnings        []string `json:"warnings,omitempty"`
}

// ErrorResponse is the failure body for every non-2xx response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// validate returns the first missing field's JSON tag, or "" if all are set.
func (r DeployRequest) validate() string {
	switch {
	case r.Service == "":
		return "service"
	case r.HeadRef == "":
		return "head_ref"
	case r.CommitSha == "":
		return "commit_sha"
	case r.PRAuthor == "":
		return "pr_author"
	case r.PRURL == "":
		return "pr_url"
	case r.PRNumber == "":
		return "pr_number"
	case r.WorkflowRunURL == "":
		return "workflow_run_url"
	}
	return ""
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), deployTimeout)
	defer cancel()

	var req DeployRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSONError(ctx, w, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
		return
	}
	if missing := req.validate(); missing != "" {
		writeJSONError(ctx, w, http.StatusBadRequest, fmt.Sprintf("missing required field: %s", missing))
		return
	}

	slug := preview.Slugify(req.HeadRef)
	logger := slog.With(
		slog.String("service", req.Service),
		slog.String("slug", slug),
		slog.String("commit_sha", req.CommitSha),
		slog.String("pr_number", req.PRNumber),
	)

	services, err := s.gitops.FetchServiceCatalog(ctx)
	if err != nil {
		writeUpstreamError(ctx, logger, w, "fetch catalog", err)
		return
	}
	if !slices.Contains(services.Services, req.Service) {
		writeJSONError(ctx, w, http.StatusNotFound,
			fmt.Sprintf("service %q not in catalog", req.Service))
		return
	}

	inputs := preview.DeployInputs{
		GitopsRepo:     "", // not used downstream; carry-over from earlier shape
		ServiceName:    req.Service,
		HeadRef:        req.HeadRef,
		CommitSha:      req.CommitSha,
		PRAuthor:       req.PRAuthor,
		PRURL:          req.PRURL,
		PRNumber:       req.PRNumber,
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		WorkflowRunURL: req.WorkflowRunURL,
	}

	result, fanErr, err := lifecycle.DeployPR(ctx, s.gitops, services, inputs)
	if err != nil {
		writeUpstreamError(ctx, logger, w, "write preview", err)
		return
	}

	resp := DeployResponse{PreviewSlug: slug, GitOpsCommitURL: result.CommitURL}
	if fanErr != nil {
		logger.WarnContext(ctx, "comment fan-out had failures", slog.String("err", fanErr.Error()))
		resp.Warnings = splitJoinedErr(fanErr)
	}

	logger.InfoContext(ctx, "deploy succeeded", slog.String("gitops_commit_url", result.CommitURL))
	writeJSON(ctx, w, http.StatusOK, resp)
}

// splitJoinedErr renders an errors.Join-style multi-error as one string per
// underlying cause so clients can show them in a list.
func splitJoinedErr(err error) []string {
	type unwrapMany interface{ Unwrap() []error }
	if u, ok := err.(unwrapMany); ok {
		errs := u.Unwrap()
		out := make([]string, 0, len(errs))
		for _, e := range errs {
			out = append(out, e.Error())
		}
		return out
	}
	return []string{err.Error()}
}

// writeUpstreamError maps gitops sentinel errors to HTTP status codes and
// emits a structured log line. ErrUnauthorized surfaces as 502 because it
// reflects upstream (GitHub) credentials, not the caller's.
func writeUpstreamError(ctx context.Context, logger *slog.Logger, w http.ResponseWriter, op string, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, gitops.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, gitops.ErrUnauthorized):
		status = http.StatusBadGateway
	}
	logger.ErrorContext(ctx, "deploy failed", slog.String("op", op), slog.String("err", err.Error()))
	writeJSONError(ctx, w, status, fmt.Sprintf("%s: %v", op, err))
}

func writeJSONError(ctx context.Context, w http.ResponseWriter, status int, msg string) {
	writeJSON(ctx, w, status, ErrorResponse{Error: msg})
}

func writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.ErrorContext(ctx, "response encode failed", slog.String("err", err.Error()))
	}
}
