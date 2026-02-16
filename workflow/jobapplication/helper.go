package jobapplication

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"text/template"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/SomtoJF/iris-worker/helper"
	"go.temporal.io/sdk/workflow"
)

type TemplateSet struct {
	System *template.Template
	User   *template.Template
}

type WorkflowTemplates struct {
	Planner TemplateSet
}

var Templates WorkflowTemplates

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
	Reasoning             string    `json:"reasoning,omitempty"`
}

type PlannerRequest struct {
	JobPostingUrl   string                                  `json:"job_posting_url"`
	UserResume      string                                  `json:"user_resume"`
	ScreenshotPath  string                                  `json:"screenshot_path"`
	TaggedNodes     []browserfactory.SerializableTaggedNode `json:"tagged_nodes"`
	ToolCallHistory []ToolCallResult                        `json:"tool_call_history"`
}

type ToolActivity struct {
	ActivityName string
	Description  string
}

var toolActivityNameMap = map[string]ToolActivity{
	"click": {
		ActivityName: "Click",
		Description:  "Click on an element identified by its index",
	},
	"input_text": {
		ActivityName: "Type",
		Description:  "Type text into an input element identified by its index",
	},
	"input_multiple": {
		ActivityName: "TypeMultiple",
		Description:  "Type text into multiple input elements in sequence",
	},
	"scroll": {
		ActivityName: "Scroll",
		Description:  "Scroll the page in a specified direction by a given ratio",
	},
	"navigate": {
		ActivityName: "Navigate",
		Description:  "Navigate to a new URL",
	},
	"web_scrape": {
		ActivityName: "ScrapeWebPage",
		Description:  "Scrape the web page for the given URL",
	},
	"retrieve_job_description": {
		ActivityName: "ScrapeWebPage",
		Description:  "Retrieve the job description using the url of the job description page",
	},
}

var toolRequestStructureMap = map[string]map[string]interface{}{
	"click": {
		"type": "object",
		"properties": map[string]interface{}{
			"element_index": map[string]interface{}{
				"type": "integer",
			},
		},
		"required": []string{"element_index"},
	},
	"input_text": {
		"type": "object",
		"properties": map[string]interface{}{
			"element_index": map[string]interface{}{
				"type": "integer",
			},
			"text": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"element_index", "text"},
	},
	"input_multiple": {
		"type": "object",
		"properties": map[string]interface{}{
			"fields": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"element_index": map[string]interface{}{
							"type": "integer",
						},
						"text": map[string]interface{}{
							"type": "string",
						},
					},
					"required": []string{"element_index", "text"},
				},
			},
		},
		"required": []string{"fields"},
	},
	"scroll": {
		"type": "object",
		"properties": map[string]interface{}{
			"direction": map[string]interface{}{
				"type": "string",
			},
			"ratio": map[string]interface{}{
				"type": "number",
			},
		},
		"required": []string{"direction", "ratio"},
	},
	"navigate": {
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"url"},
	},
	"close_page": {
		"type": "object",
		"properties": map[string]interface{}{
			"page_index": map[string]interface{}{
				"type": "integer",
			},
		},
		"required": []string{"page_index"},
	},
	"web_scrape": {
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"url"},
	},
	"retrieve_job_description": {
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"url"},
	},
}

func SetTemplates() {
	var err error

	// Define template functions
	funcMap := template.FuncMap{
		"add": func(a, b int) int {
			return a + b
		},
		"json": func(v interface{}) string {
			b, _ := json.Marshal(v)
			return string(b)
		},
	}

	Templates.Planner.System, err = helper.LoadTemplateWithFuncs("workflow/jobapplication/prompt/system.go.tmpl", funcMap)
	if err != nil {
		panic(err)
	}
	Templates.Planner.User, err = helper.LoadTemplateWithFuncs("workflow/jobapplication/prompt/user.go.tmpl", funcMap)
	if err != nil {
		panic(err)
	}
}

func planNextAction(ctx workflow.Context, input PlannerRequest) (PlannerResponse, error) {
	var systemPromptBuf bytes.Buffer
	if err := Templates.Planner.System.Execute(&systemPromptBuf, input); err != nil {
		return PlannerResponse{}, err
	}
	systemPrompt := systemPromptBuf.String()

	var userPromptBuf bytes.Buffer
	if err := Templates.Planner.User.Execute(&userPromptBuf, input); err != nil {
		return PlannerResponse{}, err
	}
	userPrompt := userPromptBuf.String()

	screenshotBase64, err := getBase64Screenshot(input.ScreenshotPath)
	if err != nil {
		return PlannerResponse{}, err
	}

	llmRequest := types.AIPIRequest{
		SystemMessage:  systemPrompt,
		UserMessage:    userPrompt,
		ImageUrl:       &screenshotBase64,
		Model:          "google/gemini-3-flash-preview",
		ResponseSchema: getPlannerResponseSchema(),
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return PlannerResponse{}, err
	}

	var plannerResponse PlannerResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &plannerResponse); err != nil {
		return PlannerResponse{}, err
	}

	return plannerResponse, nil
}

func executeToolCall(ctx workflow.Context, workflowID string, toolCall ToolCall) ToolCallResult {
	toolActivity, exists := toolActivityNameMap[toolCall.Name]
	if !exists {
		return ToolCallResult{
			ToolCall: toolCall,
			Error:    fmt.Sprintf("unknown tool: %s", toolCall.Name),
		}
	}

	toolCall.Arguments["workflow_id"] = workflowID

	resp := make(map[string]interface{})
	err := workflow.ExecuteActivity(ctx, toolActivity.ActivityName, toolCall.Arguments).Get(ctx, resp)
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

func getPlannerResponseSchema() map[string]interface{} {
	// Build tool schemas with oneOf pattern for conditional validation
	toolSchemas := make([]map[string]interface{}, 0, len(toolRequestStructureMap))
	for toolName, schema := range toolRequestStructureMap {
		toolSchema := map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type": "string",
					"enum": []string{toolName},
				},
				"arguments": schema,
			},
			"required": []string{"name", "arguments"},
		}
		toolSchemas = append(toolSchemas, toolSchema)
	}

	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"is_application_complete": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the job application has been successfully completed",
			},
			"tool_call": map[string]interface{}{
				"oneOf":       toolSchemas,
				"description": "The next tool to execute, if any",
			},
			"reasoning": map[string]interface{}{
				"type":        "string",
				"description": "Brief explanation of the decision and next action",
			},
		},
		"required": []string{"is_application_complete", "reasoning"},
	}
}

func getBase64Screenshot(screenshotPath string) (string, error) {
	screenshot, err := os.ReadFile(screenshotPath)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(screenshot), nil
}
