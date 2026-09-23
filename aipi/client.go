package aipi

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	localOpenRouter "github.com/SomtoJF/iris-worker/aipi/openrouter"
	"github.com/SomtoJF/iris-worker/aipi/types"
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

func (c *AIPIClient) GetJevCompletion(ctx context.Context, req types.JevRequest) (types.AIPIResponse, error) {
	response, err := c.GetCompletion(ctx, types.AIPIRequest{
		SystemMessage:    req.SystemMessage,
		UserMessage:      req.UserMessage,
		Model:            "typesafe/jev-1.13",
		ResponseSchema:   req.ResponseSchema,
		IdUser:           req.IdUser,
		IdJobApplication: req.IdJobApplication,
	})
	if err != nil {
		return types.AIPIResponse{}, fmt.Errorf("Jev completion: %w", err)
	}

	content := strings.TrimSpace(response.Content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimSpace(strings.TrimPrefix(content, "```"))
		if len(content) >= 4 && strings.EqualFold(content[:4], "json") {
			content = strings.TrimSpace(content[4:])
		}
		if index := strings.LastIndex(content, "```"); index >= 0 {
			content = strings.TrimSpace(content[:index])
		}
	}
	if content == "" {
		return types.AIPIResponse{}, fmt.Errorf("Jev returned empty response")
	}
	response.Content = content
	return types.AIPIResponse{
		Content:      response.Content,
		InputTokens:  response.InputTokens,
		OutputTokens: response.OutputTokens,
		InputCost:    response.InputCost,
		OutputCost:   response.OutputCost,
		TotalCost:    response.TotalCost,
		Model:        response.Model,
	}, nil
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
