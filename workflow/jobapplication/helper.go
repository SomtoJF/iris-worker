package jobapplication

import "go.temporal.io/sdk/workflow"

type ToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolCallResult struct {
	ToolCall
	Result interface{} `json:"result"`
}

type PlannerResponse struct {
	IsApplicationComplete bool       `json:"is_application_complete"`
	ToolCalls             []ToolCall `json:"tool_calls"`
}

type PlannerRequest struct {
	JobPostingUrl   string           `json:"job_posting_url"`
	ToolCallHistory []ToolCallResult `json:"tool_call_history"`
}

func planNextAction(ctx workflow.Context, input PlannerRequest) (PlannerResponse, error) {
	return PlannerResponse{}, nil
}

func executeToolCalls(ctx workflow.Context, toolCalls []ToolCall) ([]ToolCallResult, error) {
	return nil, nil
}
