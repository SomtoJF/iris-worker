package jobapplication

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"text/template"

	s3activity "github.com/SomtoJF/iris-worker/activity/s3"
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
	Error  error                  `json:"error,omitempty"`
}

type PlannerResponse struct {
	IsApplicationComplete bool      `json:"is_application_complete"`
	ToolCall              *ToolCall `json:"tool_call,omitempty"`
	Reasoning             string    `json:"reasoning,omitempty"`
}

type PlannerRequest struct {
	IdUser                  uint                                             `json:"id_user"`
	IdJobApplication        uint                                             `json:"id_job_application"`
	JobPostingUrl           string                                           `json:"job_posting_url"`
	JobDescription          string                                           `json:"job_description"`
	UserResume              string                                           `json:"user_resume"`
	UserResumePath          string                                           `json:"user_resume_path"`
	ScreenshotPath          string                                           `json:"screenshot_path"`
	TaggedNodes             []browserfactory.SerializableTaggedNode          `json:"tagged_nodes"`
	TaggedFileInputElements []browserfactory.SerializableTaggedFileInputNode `json:"tagged_file_input_elements"`
	ToolCallHistory         []ToolCallResult                                 `json:"tool_call_history"`
	UserProfileJSON         string                                           `json:"user_profile"`
}

type ToolItem struct {
	TemporalString string
	Description    string
	IsWorkflow     bool
}

var toolItemMap = map[string]ToolItem{
	"click": {
		TemporalString: "Click",
		Description:    "Click on an element identified by its index",
	},
	"input_text": {
		TemporalString: "Type",
		Description:    "Type text into an input element identified by its index",
	},
	"input_multiple": {
		TemporalString: "TypeMultiple",
		Description:    "Type text into multiple input elements in sequence",
	},
	"scroll": {
		TemporalString: "Scroll",
		Description:    "Scroll the page in a specified direction by a given ratio",
	},
	"navigate": {
		TemporalString: "Navigate",
		Description:    "Navigate to a new URL",
	},
	"web_scrape": {
		TemporalString: "ScrapeWebPage",
		Description:    "Scrape the web page for the given URL",
	},
	"upload_file": {
		TemporalString: "UploadFile",
		Description:    "Upload a file (e.g., resume) to a file input element",
	},
	"write_cover_letter": {
		TemporalString: "CoverLetterWorkflow",
		Description:    "Generate a cover letter for the current job application",
		IsWorkflow:     true,
	},
	"submit_application": {
		TemporalString: "SubmitApplicationWorkflow",
		Description:    "Click final submit button and verify form submission",
		IsWorkflow:     true,
	},
	"handle_user_action": {
		TemporalString: "HandleUserActionWorkflow",
		Description:    "Request user intervention for blocking pages",
		IsWorkflow:     true,
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
			"replace": map[string]interface{}{
				"type": "boolean",
			},
		},
		"required": []string{"element_index", "text", "replace"},
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
	"web_scrape": {
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type": "string",
			},
			"advanced": map[string]interface{}{
				"type": "boolean",
			},
		},
		"required": []string{"url", "advanced"},
	},
	"upload_file": {
		"type": "object",
		"properties": map[string]interface{}{
			"file_input_index": map[string]interface{}{
				"type": "integer",
			},
			"file_path": map[string]interface{}{
				"type": "string",
			},
		},
		"required": []string{"file_input_index", "file_path"},
	},
	"write_cover_letter": {
		"type": "object",
		"properties": map[string]interface{}{
			"id_user": map[string]interface{}{
				"type": "integer",
			},
			"id_job_application": map[string]interface{}{
				"type": "integer",
			},
			"element_index": map[string]interface{}{
				"type": "integer",
			},
		},
		"required": []string{"id_user", "id_job_application", "element_index"},
	},
	"submit_application": {
		"type": "object",
		"properties": map[string]interface{}{
			"element_index": map[string]interface{}{
				"type": "integer",
			},
		},
		"required": []string{"element_index"},
	},
	"handle_user_action": {
		"type": "object",
		"properties": map[string]interface{}{
			"user_action": map[string]interface{}{
				"type": "string",
				"enum": []string{"USER_ACTION_CAPTCHA", "USER_ACTION_AUTHENTICATION", "USER_ACTION_OTP"},
			},
			"action_details": map[string]interface{}{
				"type": "string",
			},
			"id_user": map[string]interface{}{
				"type": "integer",
			},
			"id_job_application": map[string]interface{}{
				"type": "integer",
			},
		},
		"required": []string{"user_action", "action_details", "id_user", "id_job_application"},
	},
}

func init() {
	// Validate that all tools in schema map have corresponding activity mappings
	for toolName := range toolRequestStructureMap {
		if _, exists := toolItemMap[toolName]; !exists {
			panic(fmt.Sprintf("tool '%s' has schema but no activity mapping", toolName))
		}
	}
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
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		ImageUrl:         &screenshotBase64,
		Model:            "x-ai/grok-4.1-fast",
		ResponseSchema:   getPlannerResponseSchema(),
		IdUser:           input.IdUser,
		IdJobApplication: &input.IdJobApplication,
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

func executeToolCall(ctx workflow.Context, workflowID string, userID uint, idJobApplication uint, toolCall ToolCall) ToolCallResult {
	toolItem, exists := toolItemMap[toolCall.Name]
	if !exists {
		return ToolCallResult{
			ToolCall: toolCall,
			Error:    fmt.Errorf("unknown tool: %s", toolCall.Name),
		}
	}

	toolCall.Arguments["workflow_id"] = workflowID
	toolCall.Arguments["user_id"] = userID
	toolCall.Arguments["id_job_application"] = idJobApplication
	resp := make(map[string]interface{})
	var err error
	if toolItem.IsWorkflow {
		err = workflow.ExecuteChildWorkflow(ctx, toolItem.TemporalString, toolCall.Arguments).Get(ctx, &resp)
	} else {
		err = workflow.ExecuteActivity(ctx, toolItem.TemporalString, toolCall.Arguments).Get(ctx, &resp)
	}
	if err != nil {
		return ToolCallResult{
			ToolCall: toolCall,
			Error:    err,
		}
	}

	return ToolCallResult{
		ToolCall: toolCall,
		Result:   resp,
	}
}

func getPlannerResponseSchema() map[string]interface{} {
	// Build list of tool names for enum
	toolNames := make([]string, 0, len(toolRequestStructureMap))
	for toolName := range toolRequestStructureMap {
		toolNames = append(toolNames, toolName)
	}

	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"is_application_complete": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the job application has been successfully completed",
			},
			"tool_call": map[string]interface{}{
				"anyOf": []map[string]interface{}{
					{"type": "null"},
					{
						"type": "object",
						"properties": map[string]interface{}{
							"name": map[string]interface{}{
								"type": "string",
								"enum": toolNames,
							},
							"arguments": map[string]interface{}{
								"type": "object",
							},
						},
						"required": []string{"name", "arguments"},
					},
				},
				"description": "The next tool to execute, or null when is_application_complete is true",
			},
			"reasoning": map[string]interface{}{
				"type":        "string",
				"description": "Brief explanation of the decision and next action",
			},
		},
		"required": []string{"is_application_complete", "reasoning", "tool_call"},
	}
}

func getBase64Screenshot(screenshotPath string) (string, error) {
	screenshot, err := os.ReadFile(screenshotPath)
	if err != nil {
		return "", err
	}

	base64Screenshot := base64.StdEncoding.EncodeToString(screenshot)

	return fmt.Sprintf("data:image/jpeg;base64,%s", base64Screenshot), nil
}

type JobDetails struct {
	JobTitle       string `json:"job_title"`
	CompanyName    string `json:"company_name"`
	JobDescription string `json:"job_description"`
}

func retrieveJobDetails(ctx workflow.Context, url string, idUser uint, idJobApplication uint) (JobDetails, error) {
	// Scrape webpage with advanced mode (Serper)
	var scrapeOutput map[string]interface{}
	if err := workflow.ExecuteActivity(ctx, "ScrapeWebPage", map[string]interface{}{
		"url":                url,
		"advanced":           "true",
		"id_user":            idUser,
		"id_job_application": idJobApplication,
	}).Get(ctx, &scrapeOutput); err != nil {
		return JobDetails{}, err
	}

	scrapedData, ok := scrapeOutput["data"].(string)
	if !ok {
		return JobDetails{}, fmt.Errorf("failed to get scraped data")
	}

	// Build LLM request to extract job details
	systemPrompt := "Extract the job title, company name, and job description from the provided scraped webpage content. Return the data in JSON format. Most job descriptions have a 'Who we are' or 'About us' or 'Company Description' section that contains the company's details. This is where you should look for the company name."
	userPrompt := fmt.Sprintf("Scraped content:\n\n%s", scrapedData)

	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            "x-ai/grok-4.1-fast",
		ResponseSchema:   getJobDetailsResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return JobDetails{}, err
	}

	var jobDetails JobDetails
	if err := json.Unmarshal([]byte(llmResponse.Content), &jobDetails); err != nil {
		return JobDetails{}, err
	}

	return jobDetails, nil
}

func getJobDetailsResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"job_title": map[string]interface{}{
				"type":        "string",
				"description": "The title of the job position",
			},
			"company_name": map[string]interface{}{
				"type":        "string",
				"description": "The name of the company posting the job",
			},
			"job_description": map[string]interface{}{
				"type":        "string",
				"description": "The full job description including responsibilities, requirements, and qualifications",
			},
		},
		"required": []string{"job_title", "company_name", "job_description"},
	}
}

func loadResumeIntoMemory(ctx workflow.Context, filename string, fileKey string) (string, error) {
	destPath := fmt.Sprintf("%s/%s", os.TempDir(), filename)

	var output s3activity.DownloadFileOutput
	if err := workflow.ExecuteActivity(ctx, "DownloadFile", s3activity.DownloadFileInput{
		Key:      fileKey,
		DestPath: destPath,
	}).Get(ctx, &output); err != nil {
		return "", fmt.Errorf("failed to download resume from S3: %w", err)
	}

	return output.Path, nil
}
