package jobapplication

import (
	"fmt"

	"github.com/SomtoJF/iris-worker/browserfactory"
	"go.temporal.io/sdk/workflow"
)

type ToolCall struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type ToolCallResult struct {
	ToolCall
	Result map[string]interface{} `json:"result,omitempty"`
	Error  string                 `json:"error,omitempty"`
}

type PlannerResponse struct {
	IsApplicationComplete bool      `json:"is_application_complete"`
	ToolCall              *ToolCall `json:"tool_call,omitempty"`
}

type PlannerRequest struct {
	JobPostingUrl   string                                  `json:"job_posting_url"`
	ScreenshotPath  string                                  `json:"screenshot_path"`
	TaggedNodes     []browserfactory.SerializableTaggedNode `json:"tagged_nodes"`
	ToolCallHistory []ToolCallResult                        `json:"tool_call_history"`
}

var toolActivityNameMap = map[string]string{
	"click":        "Click",
	"type":         "Type",
	"type_multiple": "TypeMultiple",
	"scroll":       "Scroll",
	"navigate":     "Navigate",
}

func planNextAction(ctx workflow.Context, input PlannerRequest) (PlannerResponse, error) {
	return PlannerResponse{}, nil
}

func executeToolCall(ctx workflow.Context, workflowID string, toolCall ToolCall) ToolCallResult {
	activityName, exists := toolActivityNameMap[toolCall.Name]
	if !exists {
		return ToolCallResult{
			ToolCall: toolCall,
			Error:    fmt.Sprintf("unknown tool: %s", toolCall.Name),
		}
	}

	toolCall.Arguments["workflow_id"] = workflowID

	resp := make(map[string]interface{})
	err := workflow.ExecuteActivity(ctx, activityName, toolCall.Arguments).Get(ctx, resp)
	if err != nil {
		return ToolCallResult{
			ToolCall: toolCall,
			Error:    err.Error(),
		}
	}

	return ToolCallResult{
		ToolCall: toolCall,
		Result:   resp,
	}
}
