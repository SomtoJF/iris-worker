package jobapplication

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"text/template"

	browseractivity "github.com/SomtoJF/iris-worker/activity/browser"
	s3activity "github.com/SomtoJF/iris-worker/activity/s3"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/SomtoJF/iris-worker/helper"
	"github.com/SomtoJF/iris-worker/shared"
	enumspb "go.temporal.io/api/enums/v1"
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

type QuestionAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type PlannerResponse struct {
	IsApplicationComplete bool             `json:"is_application_complete"`
	IsApplicationFailed   bool             `json:"is_application_failed"`
	FailureReason         *string          `json:"failure_reason,omitempty"`
	ToolCall              *ToolCall        `json:"tool_call,omitempty"`
	Reasoning             string           `json:"reasoning,omitempty"`
	QuestionsAnswered     []QuestionAnswer `json:"questions_answered,omitempty"`
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
	RequiredFields          []browserfactory.SerializableTaggedNode          `json:"required_fields"`
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
				"enum": []string{shared.UserActionAdditionalInfo, shared.UserActionOTP},
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
		"xmlEscape": func(s string) string {
			var buf bytes.Buffer
			_ = xml.EscapeText(&buf, []byte(s))
			return buf.String()
		},
		"derefString": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"derefBool": func(b *bool) bool {
			if b == nil {
				return false
			}
			return *b
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

	screenshotBase64, err := getBase64Screenshot(ctx, input.ScreenshotPath)
	if err != nil {
		return PlannerResponse{}, err
	}

	var temperaturePtr float64 = 0.2

	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		ImageUrl:         &screenshotBase64,
		Model:            "x-ai/grok-4.1-fast",
		ResponseSchema:   getPlannerResponseSchema(),
		Temperature:      &temperaturePtr,
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
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		})
		err = workflow.ExecuteChildWorkflow(childCtx, toolItem.TemporalString, toolCall.Arguments).Get(ctx, &resp)
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
			"is_application_failed": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the job application has failed and requires human intervention",
			},
			"failure_reason": map[string]interface{}{
				"anyOf": []map[string]interface{}{
					{"type": "string"},
					{"type": "null"},
				},
				"description": "The reason the job application failed (string or null)",
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
			"questions_answered": map[string]interface{}{
				"type":        "array",
				"description": "All application question/answer pairs currently visible as filled in tagged_nodes and required_elements. Only include actual application form questions, not buttons or navigation.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"question": map[string]interface{}{
							"type":        "string",
							"description": "The form field label or question text",
						},
						"answer": map[string]interface{}{
							"type":        "string",
							"description": "The value filled in the field",
						},
					},
					"required": []string{"question", "answer"},
				},
			},
		},
		"required": []string{"is_application_complete", "is_application_failed", "failure_reason", "reasoning", "tool_call", "questions_answered"},
	}
}

func getBase64Screenshot(ctx workflow.Context, screenshotPath string) (string, error) {
	var screenshotBase64 string
	if err := workflow.ExecuteActivity(ctx, "GetBase64Screenshot", browseractivity.GetBase64ScreenshotInput{
		Path: screenshotPath,
	}).Get(ctx, &screenshotBase64); err != nil {
		return "", err
	}
	return screenshotBase64, nil
}

type JobDetails struct {
	JobTitle          string `json:"job_title"`
	CompanyName       string `json:"company_name"`
	JobDescription    string `json:"job_description"`
	IsValidJobPosting bool   `json:"is_valid_job_posting"`
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
	systemPrompt := "Extract the job title, company name, and job description from the provided scraped webpage content. Return the data in JSON format. Most job descriptions have a 'Who we are' or 'About us' or 'Company Description' section that contains the company's details. This is where you should look for the company name. If this page doesn't include the job description, It is invalid and you should set is_valid_job_posting to false. For invalid job postings, return an empty string for the job description, job title, and company name."
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
			"is_valid_job_posting": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the job posting is valid and contains the necessary information",
			},
		},
		"required": []string{"job_title", "company_name", "job_description", "is_valid_job_posting"},
	}
}

func loadResumeIntoMemory(ctx workflow.Context, filename string, fileKey string) (string, error) {
	var output s3activity.DownloadFileOutput
	if err := workflow.ExecuteActivity(ctx, "DownloadFile", s3activity.DownloadFileInput{
		Key:      fileKey,
		DestPath: "",
		Filename: filename,
	}).Get(ctx, &output); err != nil {
		return "", fmt.Errorf("failed to download resume from S3: %w", err)
	}

	return output.Path, nil
}

func deduplicateQA(ctx workflow.Context, idUser uint, idJobApplication uint, questions []sqldb.JobApplicationQuestion) ([]sqldb.JobApplicationQuestion, error) {
	questionsJSON, err := json.Marshal(questions)
	if err != nil {
		return nil, fmt.Errorf("marshal questions: %w", err)
	}

	llmRequest := types.AIPIRequest{
		SystemMessage: "You are cleaning up job application form Q&A pairs.\n\nTask: Deduplicate the list by merging ONLY entries that clearly refer to the exact same underlying question but use different wording (label drift).\n\nHard rules:\n- Be conservative: if you are not confident two questions are the same, DO NOT merge.\n- Never merge questions that differ in intent (e.g. \"Phone\" vs \"Mobile phone\", \"Location\" vs \"Willing to relocate\", \"Work authorization\" vs \"Visa sponsorship\").\n- Never invent new answers or modify answers.\n- Prefer keeping separate entries over incorrect merges.\n\nWhen you do merge:\n- Keep the most descriptive/clear question text.\n- If the answers are identical (ignoring case/whitespace), keep one.\n- If answers differ, only choose one if you can justify they are the same value in different formatting (e.g. \"Yes\" vs \"yes\", phone formatting). Otherwise keep BOTH as separate entries.\n\nReturn ONLY valid JSON matching the schema.",
		UserMessage:   string(questionsJSON),
		Model:         "google/gemma-4-31b-it:free",
		ResponseSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"questions": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"question": map[string]interface{}{"type": "string"},
							"answer":   map[string]interface{}{"type": "string"},
						},
						"required": []string{"question", "answer"},
					},
				},
			},
			"required": []string{"questions"},
		},
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return nil, fmt.Errorf("CallLLM: %w", err)
	}

	var resp deduplicateQAResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal dedup response: %w", err)
	}

	return resp.Questions, nil
}
