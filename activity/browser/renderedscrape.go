package browser

import (
	"context"
)

func (a *Activity) ScrapeRenderedPage(ctx context.Context, input ScrapeRenderedPageInput) (ScrapeRenderedPageOutput, error) {
	text, err := a.client.ScrapeRenderedPage(ctx, applicationBrowserID(input.WorkflowID))
	if err != nil {
		return ScrapeRenderedPageOutput{}, err
	}
	return ScrapeRenderedPageOutput{Data: text}, nil
}
