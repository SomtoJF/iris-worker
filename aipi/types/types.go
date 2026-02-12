package types

import (
	"context"
)

type AIPIRequest struct {
	SystemMessage string `json:"system_message"`
	UserMessage   string `json:"user_message"`
	Model         string `json:"model"`
	// ImageUrl can either be a url or a base64 encoded image
	ImageUrl       *string                `json:"image_url,omitempty"`
	MaxTokens      *int                   `json:"max_tokens,omitempty"`
	ResponseSchema map[string]interface{} `json:"response_schema,omitempty"`
	Temperature    *float64               `json:"temperature,omitempty"`
}

type AIPIResponse struct {
	Content      string  `json:"content"`
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	InputCost    float64 `json:"input_cost,omitempty"`
	OutputCost   float64 `json:"output_cost,omitempty"`
	TotalCost    float64 `json:"total_cost,omitempty"`
	Model        string  `json:"model,omitempty"`
}

type AIPI interface {
	GetCompletion(ctx context.Context, req AIPIRequest) (AIPIResponse, error)
}
