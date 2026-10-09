package types

import (
	"context"
)

type AIPIRequest struct {
	SystemMessage string `json:"system_message"`
	UserMessage   string `json:"user_message"`
	Model         string `json:"model"`
	// ImageUrl can either be a url or a base64 encoded image
	ImageUrl                   *string  `json:"image_url,omitempty"`
	MaxTokens                  *int     `json:"max_tokens,omitempty"`
	ResponseSchema             any      `json:"response_schema,omitempty"`
	Temperature                *float64 `json:"temperature,omitempty"`
	IdUser                     uint     `json:"id_user"`
	IdJobApplication           *uint    `json:"id_job_application,omitempty"`
	UserActionID               string   `json:"user_action_id,omitempty"`
	UserActionResultCiphertext []byte   `json:"user_action_result_ciphertext,omitempty"`
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

type JevRequest struct {
	State     any                    `json:"state"`
	Questions map[string]JevQuestion `json:"questions"`
	// ScreenshotDataURL is an optional data URL or URL of the page screenshot sent to the multimodal decider.
	ScreenshotDataURL string `json:"screenshot_data_url,omitempty"`
	IdUser            uint   `json:"id_user"`
	IdJobApplication  *uint  `json:"id_job_application,omitempty"`
}

type JevQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria"`
}

type JevResponse struct {
	ID       string               `json:"id"`
	Model    string               `json:"model"`
	Provider string               `json:"provider"`
	Answers  map[string]JevAnswer `json:"answers"`
	Usage    JevUsage             `json:"usage"`
}

type JevAnswer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type JevUsage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Cost         float64 `json:"cost"`
}

type AIPI interface {
	GetCompletion(ctx context.Context, req AIPIRequest) (AIPIResponse, error)
	GetDecisionsCompletion(ctx context.Context, req JevRequest) (JevResponse, error)
}
