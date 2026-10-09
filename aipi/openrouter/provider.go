package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/revrost/go-openrouter"
	"github.com/revrost/go-openrouter/jsonschema"
)

const (
	jevModel          = "perplexity/pplx-decider-v1.1-27b"
	decisionsEndpoint = "https://openrouter.ai/api/alpha/decisions"
)

// rawSchema wraps a map to implement json.Marshaler
type rawSchema map[string]any

func (r rawSchema) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(r))
}

type OpenRouterProvider struct {
	client        *openrouter.Client
	apiKey        string
	jevHTTPClient interface {
		Do(*http.Request) (*http.Response, error)
	}
	decisionsEndpoint string
}

func NewOpenRouterProvider(apiKey string) *OpenRouterProvider {
	openrouterClient := openrouter.NewClient(apiKey)
	return &OpenRouterProvider{
		client:            openrouterClient,
		apiKey:            apiKey,
		jevHTTPClient:     http.DefaultClient,
		decisionsEndpoint: decisionsEndpoint,
	}
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

	if len(req.Tools) > 0 {
		for _, tool := range req.Tools {
			chatReq.Tools = append(chatReq.Tools, openrouter.Tool{
				Type: openrouter.ToolTypeFunction,
				Function: &openrouter.FunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
					Strict:      tool.Strict,
				},
			})
		}
		chatReq.ToolChoice = toolChoice(req.ToolChoice)
		if req.ParallelToolCalls != nil {
			chatReq.ParallelToolCalls = *req.ParallelToolCalls
		}
	}

	if req.ResponseSchema != nil {
		var schema json.Marshaler
		// If ResponseSchema is already a map, wrap it; otherwise generate from struct
		// Check both map[string]any and map[string]interface{} (they're the same but type assertion needs exact match)
		if schemaMap, ok := req.ResponseSchema.(map[string]any); ok {
			schema = rawSchema(schemaMap)
		} else if schemaMap, ok := req.ResponseSchema.(map[string]interface{}); ok {
			schema = rawSchema(schemaMap)
		} else {
			generatedSchema, err := jsonschema.GenerateSchemaForType(req.ResponseSchema)
			if err != nil {
				return types.AIPIResponse{}, fmt.Errorf("failed to generate response schema: %w", err)
			}
			schema = generatedSchema
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

func (p *OpenRouterProvider) GetDecisionsCompletion(ctx context.Context, req types.JevRequest) (types.JevResponse, error) {
	var state any = req.State
	if req.ScreenshotDataURL != "" {
		state = map[string]any{
			"page_state": req.State,
			"screenshot": map[string]any{
				"type":      "image_url",
				"image_url": map[string]string{"url": req.ScreenshotDataURL},
			},
		}
	}
	requestBody := struct {
		Model     string                       `json:"model"`
		State     any                          `json:"state"`
		Questions map[string]types.JevQuestion `json:"questions"`
	}{
		Model:     jevModel,
		State:     state,
		Questions: req.Questions,
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return types.JevResponse{}, fmt.Errorf("marshal JEV Decisions request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.decisionsEndpoint, bytes.NewReader(body))
	if err != nil {
		return types.JevResponse{}, fmt.Errorf("create JEV Decisions request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := p.jevHTTPClient.Do(httpReq)
	if err != nil {
		return types.JevResponse{}, fmt.Errorf("send JEV Decisions request: %w", err)
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		return types.JevResponse{}, fmt.Errorf("read JEV Decisions response: %w", err)
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return types.JevResponse{}, fmt.Errorf("JEV Decisions API returned %s: %s", httpResp.Status, strings.TrimSpace(string(responseBody)))
	}

	var response types.JevResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return types.JevResponse{}, fmt.Errorf("decode JEV Decisions response: %w", err)
	}
	if response.Model == "" {
		return types.JevResponse{}, fmt.Errorf("JEV Decisions response missing model")
	}
	if response.Answers == nil {
		return types.JevResponse{}, fmt.Errorf("JEV Decisions response missing answers")
	}
	return response, nil
}

// toolChoice maps the provider-agnostic value onto the OpenAI-compatible tool_choice parameter.
func toolChoice(choice string) any {
	switch choice {
	case "":
		return nil
	case "auto", "none", "required":
		return choice
	default:
		return map[string]any{"type": "function", "function": map[string]string{"name": choice}}
	}
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
	var toolCalls []types.ToolCall
	if len(resp.Choices) > 0 {
		content = resp.Choices[0].Message.Content.Text
		for _, call := range resp.Choices[0].Message.ToolCalls {
			toolCalls = append(toolCalls, types.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		}
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
		ToolCalls:    toolCalls,
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
		"deepseek/deepseek-chat":                       {inputRate: 0.14, outputRate: 0.28},
		"deepseek/deepseek-r1":                         {inputRate: 0.55, outputRate: 2.19},
		"deepseek/deepseek-r1-distill-llama-70b":       {inputRate: 0.55, outputRate: 2.19},
		"deepseek/deepseek-v4-flash":                   {inputRate: 0.07, outputRate: 0.17},
		"deepseek/deepseek-v4-pro":                     {inputRate: 0.435, outputRate: 0.87},
		"google/gemini-2.0-flash-lite":                 {inputRate: 0.075, outputRate: 0.30},
		"google/gemini-2.5-flash-lite-preview-09-2025": {inputRate: 0.10, outputRate: 0.40},
		"google/gemini-2.5-flash-lite":                 {inputRate: 0.10, outputRate: 0.40},
		"google/gemini-2.0-flash":                      {inputRate: 0.10, outputRate: 0.40},
		"google/gemini-2.5-flash":                      {inputRate: 0.30, outputRate: 2.50},
		"google/gemini-3-flash-preview":                {inputRate: 0.50, outputRate: 3.0},
		"google/gemini-3-pro-preview":                  {inputRate: 2.0, outputRate: 12.0},
		"google/gemini-2.5-pro":                        {inputRate: 1.25, outputRate: 10.0},
		"x-ai/grok-4.5":                                {inputRate: 2.0, outputRate: 6.0},
		"x-ai/grok-4.3":                                {inputRate: 1.25, outputRate: 2.50},
		"x-ai/grok-4.1-fast":                           {inputRate: 0.20, outputRate: 0.50},
		"x-ai/grok-4-fast":                             {inputRate: 0.20, outputRate: 0.50},
		"google/gemma-4-31b-it:free":                   {inputRate: 0.00, outputRate: 0.00},
		"google/gemma-4-31b-it":                        {inputRate: 0.12, outputRate: 0.35},
		"openai/gpt-6-luna-decisions":                  {inputRate: 0.10, outputRate: 0.00},
		"typesafe/jev-1.13":                            {inputRate: 0.042, outputRate: 0.00},
		"perplexity/pplx-decider-v1.1-27b":             {inputRate: 0.02, outputRate: 0.00},
		"openai/gpt-5.6-luna":                          {inputRate: 0.20, outputRate: 1.20},
		"qwen/qwen3.8-flash":                           {inputRate: 0.15, outputRate: 0.47},
	}

	if rate, ok := rates[model]; ok {
		return rate
	}

	return modelRates{inputRate: 0, outputRate: 0}
}
