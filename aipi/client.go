package aipi

import (
	"context"
	"log"

	localOpenRouter "github.com/SomtoJF/iris-worker/aipi/openrouter"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	openrouter "github.com/revrost/go-openrouter"
	"gorm.io/gorm"
)

type AIPIClient struct {
	openRouterClient *localOpenRouter.OpenRouterProvider
	db               *gorm.DB
}

func NewAIPIClient(openRouterClient *openrouter.Client, db *gorm.DB) *AIPIClient {
	return &AIPIClient{
		openRouterClient: localOpenRouter.NewOpenRouterProvider(openRouterClient),
		db:               db,
	}
}

func (c *AIPIClient) GetCompletion(ctx context.Context, req types.AIPIRequest) (types.AIPIResponse, error) {
	resp, err := c.openRouterClient.GetCompletion(ctx, req)
	if err != nil {
		return resp, err
	}

	c.saveCostTracking(req, resp)

	return resp, nil
}

func (c *AIPIClient) saveCostTracking(req types.AIPIRequest, resp types.AIPIResponse) {
	record := sqldb.CostTracking{
		IdUser:           req.IdUser,
		IdJobApplication: req.IdJobApplication,
		Type:             sqldb.CostTrackingTypeAIPI,
		Model:            &resp.Model,
		InputTokens:      &resp.InputTokens,
		OutputTokens:     &resp.OutputTokens,
		InputCost:        &resp.InputCost,
		OutputCost:       resp.OutputCost,
		TotalCost:        resp.TotalCost,
	}

	if err := c.db.Create(&record).Error; err != nil {
		log.Printf("failed to save cost tracking record: %v", err)
	}
}
