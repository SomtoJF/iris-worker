package aipi

import (
	"context"

	localOpenRouter "github.com/SomtoJF/iris-worker/aipi/openrouter"
	"github.com/SomtoJF/iris-worker/aipi/types"
	openrouter "github.com/revrost/go-openrouter"
)

type AIPIClient struct {
	openRouterClient *localOpenRouter.OpenRouterProvider
}

func NewAIPIClient(openRouterClient *openrouter.Client) *AIPIClient {
	return &AIPIClient{
		openRouterClient: localOpenRouter.NewOpenRouterProvider(openRouterClient),
	}
}

func (c *AIPIClient) GetCompletion(ctx context.Context, req types.AIPIRequest) (types.AIPIResponse, error) {
	return c.openRouterClient.GetCompletion(ctx, req)
}
