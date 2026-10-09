package aipi

import (
	"context"
	"fmt"
	"log"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	localOpenRouter "github.com/SomtoJF/iris-worker/aipi/openrouter"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"gorm.io/gorm"
)

type AIPIClient struct {
	openRouterClient *localOpenRouter.OpenRouterProvider
	db               *gorm.DB
}

func NewAIPIClient(apiKey string, db *gorm.DB) *AIPIClient {
	return &AIPIClient{
		openRouterClient: localOpenRouter.NewOpenRouterProvider(apiKey),
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

func (c *AIPIClient) GetDecisionsCompletion(ctx context.Context, req types.JevRequest) (types.JevResponse, error) {
	response, err := c.openRouterClient.GetDecisionsCompletion(ctx, req)
	if err != nil {
		return types.JevResponse{}, fmt.Errorf("JEV completion: %w", err)
	}

	c.saveJevCostTracking(req, response)
	return response, nil
}

func (c *AIPIClient) saveCostTracking(req types.AIPIRequest, resp types.AIPIResponse) {
	record := sqldb.CostTracking{
		UserId:           req.IdUser,
		JobApplicationId: req.IdJobApplication,
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

func (c *AIPIClient) saveJevCostTracking(req types.JevRequest, resp types.JevResponse) {
	model := resp.Model
	inputTokens := resp.Usage.InputTokens
	outputTokens := resp.Usage.OutputTokens
	inputCost := resp.Usage.Cost

	record := sqldb.CostTracking{
		UserId:           req.IdUser,
		JobApplicationId: req.IdJobApplication,
		Type:             sqldb.CostTrackingTypeAIPI,
		Model:            &model,
		InputTokens:      &inputTokens,
		OutputTokens:     &outputTokens,
		InputCost:        &inputCost,
		OutputCost:       0,
		TotalCost:        resp.Usage.Cost,
	}

	if err := c.db.Create(&record).Error; err != nil {
		log.Printf("failed to save JEV cost tracking record: %v", err)
	}
}
