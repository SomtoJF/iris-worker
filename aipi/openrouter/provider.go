package openrouter

import (
	"context"
	"fmt"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/revrost/go-openrouter"
	"github.com/revrost/go-openrouter/jsonschema"
)

type OpenRouterProvider struct {
	client *openrouter.Client
}

func NewOpenRouterProvider(client *openrouter.Client) *OpenRouterProvider {
	return &OpenRouterProvider{client: client}
}

func (p *OpenRouterProvider) GetCompletion(ctx context.Context, req types.AIPIRequest) (types.AIPIResponse, error) {
	messages := buildMessages(req)

	chatReq := openrouter.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
	}

	if req.MaxTokens != nil {
		chatReq.MaxTokens = *req.MaxTokens
	}

	if req.Temperature != nil {
		chatReq.Temperature = float32(*req.Temperature)
	}

	if req.ResponseSchema != nil {
		schema, err := jsonschema.GenerateSchemaForType(req.ResponseSchema)
		if err != nil {
			return types.AIPIResponse{}, fmt.Errorf("failed to generate response schema: %w", err)
		}
		chatReq.ResponseFormat = &openrouter.ChatCompletionResponseFormat{
			Type: openrouter.ChatCompletionResponseFormatTypeJSONSchema,
			JSONSchema: &openrouter.ChatCompletionResponseFormatJSONSchema{
				Name:   "response_schema",
				Schema: schema,
				Strict: true,
			},
		}
	}

	resp, err := p.client.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		return types.AIPIResponse{}, fmt.Errorf("openrouter api call failed: %w", err)
	}

	return mapResponse(resp), nil
}

func buildMessages(req types.AIPIRequest) []openrouter.ChatCompletionMessage {
	messages := []openrouter.ChatCompletionMessage{}

	if req.SystemMessage != "" {
		messages = append(messages, openrouter.SystemMessage(req.SystemMessage))
	}

	if req.ImageUrl != nil && *req.ImageUrl != "" {
		messages = append(messages, openrouter.UserMessageWithImage(req.UserMessage, *req.ImageUrl))
	} else {
		messages = append(messages, openrouter.UserMessage(req.UserMessage))
	}

	return messages
}

func mapResponse(resp openrouter.ChatCompletionResponse) types.AIPIResponse {
	content := ""
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content.Text
	}

	inputTokens := 0
	outputTokens := 0
	totalCost := 0.0
	inputCost := 0.0
	outputCost := 0.0

	if resp.Usage != nil {
		inputTokens = resp.Usage.PromptTokens
		outputTokens = resp.Usage.CompletionTokens
		totalCost = resp.Usage.Cost
		inputCost, outputCost = calculateCosts(resp.Model, inputTokens, outputTokens, totalCost)
	}

	return types.AIPIResponse{
		Content:      content,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		InputCost:    inputCost,
		OutputCost:   outputCost,
		TotalCost:    totalCost,
		Model:        resp.Model,
	}
}

func calculateCosts(model string, inputTokens, outputTokens int, totalCost float64) (float64, float64) {
	if totalCost == 0 || (inputTokens+outputTokens) == 0 {
		return 0, 0
	}

	rates := getModelRates(model)
	if rates.inputRate == 0 && rates.outputRate == 0 {
		return 0, 0
	}

	inputCost := float64(inputTokens) * rates.inputRate / 1_000_000
	outputCost := float64(outputTokens) * rates.outputRate / 1_000_000

	return inputCost, outputCost
}

type modelRates struct {
	inputRate  float64
	outputRate float64
}

func getModelRates(model string) modelRates {
	rates := map[string]modelRates{
		"openai/chatgpt-4o-latest":                {inputRate: 5.0, outputRate: 15.0},
		"openai/gpt-4o-mini":                      {inputRate: 0.15, outputRate: 0.60},
		"deepseek/deepseek-chat":                  {inputRate: 0.14, outputRate: 0.28},
		"deepseek/deepseek-r1":                    {inputRate: 0.55, outputRate: 2.19},
		"deepseek/deepseek-r1-distill-llama-70b":  {inputRate: 0.55, outputRate: 2.19},
		"google/gemini-2.0-flash-exp:free":        {inputRate: 0, outputRate: 0},
		"google/gemini-pro-1.5-exp":               {inputRate: 0, outputRate: 0},
		"google/gemini-flash-1.5-8b":              {inputRate: 0.0375, outputRate: 0.15},
		"microsoft/phi-3-mini-128k-instruct:free": {inputRate: 0, outputRate: 0},
		"liquid/lfm-7b":                           {inputRate: 0.10, outputRate: 0.10},
	}

	if rate, ok := rates[model]; ok {
		return rate
	}

	return modelRates{inputRate: 0, outputRate: 0}
}
