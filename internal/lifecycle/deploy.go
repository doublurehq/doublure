package lifecycle

import (
	"context"
	"fmt"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// DeployAPI is the subset of gitops.Client used by DeployPR.
type DeployAPI interface {
	WritePreviewWithRetry(
		ctx context.Context,
		slug string,
		catalog []string,
		serviceName, commitSha string,
		inputs preview.DeployInputs,
		createMessage, updateMessage string,
	) (*gitops.WriteResult, error)
	FanOutPRComments(ctx context.Context, vals preview.PreviewValues, body string) error
}

// DeployPR pins inputs.ServiceName at inputs.CommitSha in the preview of
// inputs.HeadRef, then posts the preview summary on every open PR of the
// preview. It returns the write result, the fan-out error (possibly joined,
// nil when every comment landed) and the write error; on a write error
// nothing is commented. It does not log.
func DeployPR(
	ctx context.Context,
	api DeployAPI,
	catalog *gitops.ServiceCatalog,
	inputs preview.DeployInputs,
) (*gitops.WriteResult, error, error) {
	slug := preview.Slugify(inputs.HeadRef)
	createMsg := fmt.Sprintf("chore(preview): create %s preview", slug)
	updateMsg := fmt.Sprintf("chore(preview): update %s in %s", inputs.ServiceName, slug)

	result, err := api.WritePreviewWithRetry(
		ctx, slug, catalog.Services, inputs.ServiceName, inputs.CommitSha, inputs, createMsg, updateMsg,
	)
	if err != nil {
		return nil, nil, err //nolint:nilnil // the write error is the last result; no fan-out ran
	}

	summary := preview.BuildSummary(slug, result.Values, catalog.URLs, result.CommitMessage, result.CommitURL)
	return result, api.FanOutPRComments(ctx, result.Values, summary), nil
}
