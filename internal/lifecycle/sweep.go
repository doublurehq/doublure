package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// SweepAPI is the subset of gitops.Client used by Sweep.
type SweepAPI interface {
	TeardownAPI
	ListPreviews(ctx context.Context) ([]string, error)
	GetPreview(ctx context.Context, slug string) (*preview.PreviewValues, string, error)
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*gitops.PullRequest, error)
	DeletePreviewWithRetry(
		ctx context.Context,
		slug, message string,
		stillDelete func(preview.PreviewValues) bool,
	) (*gitops.DeleteResult, error)
}

// Sweep visits every preview of the gitops repo once and applies the first
// matching rule:
//
//  1. no pinned entry: the preview is deleted;
//  2. ttl > 0 and the newest updated-at of its pinned entries is before
//     now - ttl: the preview is deleted and its open PRs are told it expired;
//  3. otherwise every distinct PR pinning an entry is looked up, and each
//     closed one goes through TeardownPR.
//
// A ttl of 0 disables rule 2. A failing preview read or rule 1-2 delete is
// logged and counted, and the sweep goes on; Sweep then returns an error
// carrying the count. A closed-PR teardown failure is only logged by
// TeardownPR, not counted, and retried on the next run. A ListPreviews error is
// returned at once.
func Sweep(ctx context.Context, api SweepAPI, now time.Time, ttl time.Duration) error {
	slugs, err := api.ListPreviews(ctx)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "cleanup started",
		slog.Int("previews", len(slugs)),
		slog.String("ttl", ttl.String()),
	)
	failed := 0
	for _, slug := range slugs {
		if sweepErr := sweepPreview(ctx, api, slug, now, ttl); sweepErr != nil {
			failed++
			slog.ErrorContext(ctx, "cleanup preview failed",
				slog.String("slug", slug),
				slog.String("err", sweepErr.Error()),
			)
		}
	}
	slog.InfoContext(ctx, "cleanup finished",
		slog.Int("previews", len(slugs)),
		slog.Int("failed", failed),
	)
	if failed > 0 {
		return fmt.Errorf("cleanup: %d preview(s) failed", failed)
	}
	return nil
}

// sweepPreview applies the first matching sweep rule to one preview. A
// preview that vanished since the listing is skipped.
func sweepPreview(ctx context.Context, api SweepAPI, slug string, now time.Time, ttl time.Duration) error {
	vals, _, err := api.GetPreview(ctx, slug)
	if errors.Is(err, gitops.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if !hasPin(*vals) {
		return deleteUnpinned(ctx, api, slug)
	}
	if ttl > 0 {
		cutoff := now.Add(-ttl)
		if expired(*vals, cutoff) {
			return deleteExpired(ctx, api, slug, ttl, cutoff)
		}
	}
	teardownClosedPRs(ctx, api, slug, *vals)
	return nil
}

// hasPin reports whether any entry of vals is pinned to a commit.
func hasPin(vals preview.PreviewValues) bool {
	for _, svc := range vals.Services {
		if svc.CommitSha != "" {
			return true
		}
	}
	return false
}

// expired reports whether the newest updated-at of vals' pinned entries is
// before cutoff. A preview without a parseable updated-at never expires.
func expired(vals preview.PreviewValues, cutoff time.Time) bool {
	last, ok := preview.LastDeploy(vals)
	return ok && last.Before(cutoff)
}

// deleteUnpinned deletes a preview with no pinned entry (rule 1), unless a
// deploy pinned one in the meantime.
func deleteUnpinned(ctx context.Context, api SweepAPI, slug string) error {
	message := fmt.Sprintf("chore(preview): delete %s preview", slug)
	res, err := api.DeletePreviewWithRetry(ctx, slug, message, func(v preview.PreviewValues) bool {
		return !hasPin(v)
	})
	if err != nil {
		return err
	}
	if !res.Deleted {
		return nil
	}
	slog.InfoContext(ctx, "preview deleted",
		slog.String("slug", slug),
		slog.String("reason", "no pins"),
		slog.String("gitops_commit_url", res.CommitURL),
	)
	return nil
}

// deleteExpired deletes a preview with no deploy since cutoff (rule 2), unless
// a deploy refreshed it in the meantime, then tells its open PRs.
func deleteExpired(ctx context.Context, api SweepAPI, slug string, ttl time.Duration, cutoff time.Time) error {
	message := fmt.Sprintf("chore(preview): expire %s preview", slug)
	res, err := api.DeletePreviewWithRetry(ctx, slug, message, func(v preview.PreviewValues) bool {
		return expired(v, cutoff)
	})
	if err != nil {
		return err
	}
	if !res.Deleted {
		return nil
	}
	last, _ := preview.LastDeploy(res.Values)
	slog.InfoContext(ctx, "preview deleted",
		slog.String("slug", slug),
		slog.String("reason", "expired"),
		slog.String("gitops_commit_url", res.CommitURL),
		slog.String("last_deploy", last.Format(time.RFC3339)),
	)
	body := preview.BuildExpirySummary(slug, ttl, message, res.CommitURL)
	if fanErr := api.FanOutPRComments(ctx, res.Values, body); fanErr != nil {
		slog.WarnContext(ctx, "preview expiry comment failed",
			slog.String("slug", slug),
			slog.String("err", fanErr.Error()),
		)
	}
	return nil
}

// teardownClosedPRs looks up every distinct PR pinning an entry of vals, in
// file order, and tears down each closed one (rule 3). A PR whose state cannot
// be read keeps its pins.
func teardownClosedPRs(ctx context.Context, api SweepAPI, slug string, vals preview.PreviewValues) {
	for _, t := range pinnedPRs(slug, vals) {
		pr, err := api.GetPullRequest(ctx, t.Owner, t.Repo, t.Number)
		if err != nil {
			slog.WarnContext(ctx, "cleanup pr state unknown",
				slog.String("slug", slug),
				slog.String("repo", t.FullName),
				slog.Int("pr_number", t.Number),
				slog.String("err", err.Error()),
			)
			continue
		}
		if !pr.Open {
			TeardownPR(ctx, api, t)
		}
	}
}

// pinnedPRs returns one Target per distinct PR referenced by a pinned entry of
// vals, in file order. PRs compare on lowercase owner/repo and number. Entries
// without metadata or with an unparsable pr-url are skipped.
func pinnedPRs(slug string, vals preview.PreviewValues) []Target {
	var targets []Target
	seen := map[string]bool{}
	for _, svc := range vals.Services {
		if svc.CommitSha == "" || svc.Metadata == nil {
			continue
		}
		owner, repo, number, ok := preview.ParsePRURL(svc.Metadata.PRURL)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%s/%s#%d", strings.ToLower(owner), strings.ToLower(repo), number)
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, Target{
			Slug:     slug,
			Owner:    owner,
			Repo:     repo,
			FullName: owner + "/" + repo,
			Number:   number,
		})
	}
	return targets
}
