package jobapplication

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"math"
	"strconv"
	"strings"
	"text/template"

	browseractivity "github.com/SomtoJF/iris-worker/activity/browser"
	s3activity "github.com/SomtoJF/iris-worker/activity/s3"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
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

// PlannerFailureStatus categorizes why the planner marked an application as failed.
type PlannerFailureStatus string

const (
	PlannerFailureStatusCaptcha         PlannerFailureStatus = "CAPTCHA"
	PlannerFailureStatusTruthfulness    PlannerFailureStatus = "TRUTHFULNESS"
	PlannerFailureStatusLoginRequired   PlannerFailureStatus = "LOGIN_REQUIRED"
	PlannerFailureStatusSubmissionError PlannerFailureStatus = "SUBMISSION_ERROR"
	PlannerFailureStatusOther           PlannerFailureStatus = "OTHER"
)

type PlannerResponse struct {
	IsApplicationComplete bool                  `json:"is_application_complete"`
	IsApplicationFailed   bool                  `json:"is_application_failed"`
	FailureReason         *string               `json:"failure_reason,omitempty"`
	FailureStatus         *PlannerFailureStatus `json:"failure_status,omitempty"`
	ToolCall              *ToolCall             `json:"tool_call,omitempty"`
	Reasoning             string                `json:"reasoning,omitempty"`
	QuestionsAnswered     []QuestionAnswer      `json:"questions_answered,omitempty"`
}

type PlannerRequest struct {
	IdUser                     uint                                              `json:"id_user"`
	IdJobApplication           uint                                              `json:"id_job_application"`
	JobPostingUrl              string                                            `json:"job_posting_url"`
	JobDescription             string                                            `json:"job_description"`
	UserResume                 string                                            `json:"user_resume"`
	UserResumePath             string                                            `json:"user_resume_path"`
	ScreenshotPath             string                                            `json:"screenshot_path"`
	TaggedNodes                []browseractivity.SerializableTaggedNode          `json:"tagged_nodes"`
	RequiredFields             []browseractivity.SerializableTaggedNode          `json:"required_fields"`
	TaggedFileInputElements    []browseractivity.SerializableTaggedFileInputNode `json:"tagged_file_input_elements"`
	ToolCallHistory            []ToolCallResult                                  `json:"tool_call_history"`
	UserProfileJSON            string                                            `json:"user_profile"`
	CurrentDate                string                                            `json:"current_date"`
	UserActionID               string                                            `json:"user_action_id,omitempty"`
	UserActionResultCiphertext []byte                                            `json:"user_action_result_ciphertext,omitempty"`
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
		SystemMessage:              systemPrompt,
		UserMessage:                userPrompt,
		ImageUrl:                   &screenshotBase64,
		Model:                      "openai/gpt-5.6-luna",
		ResponseSchema:             getPlannerResponseSchema(),
		Temperature:                &temperaturePtr,
		IdUser:                     input.IdUser,
		IdJobApplication:           &input.IdJobApplication,
		UserActionID:               input.UserActionID,
		UserActionResultCiphertext: input.UserActionResultCiphertext,
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

func executeToolCall(ctx workflow.Context, workflowID string, userID uint, idJobApplication uint, toolCall ToolCall, taggedNodes []browseractivity.SerializableTaggedNode, fileInputNodes []browseractivity.SerializableTaggedFileInputNode, secureActionID string, secureActionCiphertext []byte) ToolCallResult {
	toolItem, exists := toolItemMap[toolCall.Name]
	if !exists {
		return ToolCallResult{
			ToolCall: toolCall,
			Error:    fmt.Errorf("unknown tool: %s", toolCall.Name),
		}
	}
	if toolCall.Arguments == nil {
		toolCall.Arguments = make(map[string]interface{})
	}
	toolCall.Arguments["workflow_id"] = workflowID
	toolCall.Arguments["user_id"] = userID
	toolCall.Arguments["id_job_application"] = idJobApplication
	attachMutationTargets(toolCall.Name, toolCall.Arguments, taggedNodes, fileInputNodes)
	if toolCall.Name == "input_text" {
		if result, handled := executeSecureTextTool(ctx, workflowID, toolCall, secureActionID, secureActionCiphertext); handled {
			return result
		}
	}
	if toolCall.Name == "input_multiple" {
		if result, handled := executeSecureMultipleTool(ctx, workflowID, toolCall, secureActionID, secureActionCiphertext); handled {
			return result
		}
	}
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

func attachMutationTargets(toolName string, arguments map[string]interface{}, nodes []browseractivity.SerializableTaggedNode, fileNodes []browseractivity.SerializableTaggedFileInputNode) {
	targetForNode := func(index int) *sqldb.BrowserMutationTarget {
		for _, node := range nodes {
			if node.Index == index {
				return mutationTargetForNode(node)
			}
		}
		return nil
	}
	switch toolName {
	case "click", "input_text":
		if index, ok := toolArgumentInt(arguments["element_index"]); ok {
			if target := targetForNode(index); target != nil {
				arguments["target"] = target
			}
		}
	case "input_multiple":
		fields, ok := arguments["fields"].([]interface{})
		if !ok {
			return
		}
		for _, rawField := range fields {
			field, ok := rawField.(map[string]interface{})
			if !ok {
				continue
			}
			if index, ok := toolArgumentInt(field["element_index"]); ok {
				if target := targetForNode(index); target != nil {
					field["target"] = target
				}
			}
		}
	case "upload_file":
		index, ok := toolArgumentInt(arguments["file_input_index"])
		if !ok {
			return
		}
		for _, fileNode := range fileNodes {
			if fileNode.Index != index {
				continue
			}
			arguments["target"] = mutationTargetForFileInput(fileNode)
			return
		}
	}
}

func mutationTargetForNode(node browseractivity.SerializableTaggedNode) *sqldb.BrowserMutationTarget {
	return &sqldb.BrowserMutationTarget{
		Role:     node.Role,
		Name:     strings.TrimSpace(node.Name),
		Label:    node.Label,
		Selector: node.Selector,
		Submit:   node.Submit,
	}
}

func mutationTargetForFileInput(node browseractivity.SerializableTaggedFileInputNode) *sqldb.BrowserMutationTarget {
	label := ""
	if node.Label != nil {
		label = *node.Label
	}
	return &sqldb.BrowserMutationTarget{Role: "file", Name: node.Name, Label: label}
}

const secureUserActionValuePrefix = "__IRIS_SECURE_USER_ACTION_VALUE_"

func executeSecureTextTool(ctx workflow.Context, workflowID string, toolCall ToolCall, actionID string, ciphertext []byte) (ToolCallResult, bool) {
	text, ok := toolCall.Arguments["text"].(string)
	if !ok || !strings.Contains(text, secureUserActionValuePrefix) {
		return ToolCallResult{}, false
	}
	valueIndex, ok := secureUserActionValueIndex(text)
	if !ok || actionID == "" || len(ciphertext) == 0 {
		return ToolCallResult{ToolCall: toolCall, Error: fmt.Errorf("secure user-action answer marker is invalid or unavailable")}, true
	}
	elementIndex, ok := toolArgumentInt(toolCall.Arguments["element_index"])
	if !ok {
		return ToolCallResult{ToolCall: toolCall, Error: fmt.Errorf("secure user-action input has an invalid element index")}, true
	}
	err := workflow.ExecuteActivity(ctx, "TypeUserActionSecure", browseractivity.SecureTypeInput{
		ActionID:     actionID,
		Operation:    "user_action_value",
		Ciphertext:   ciphertext,
		WorkflowID:   workflowID,
		ElementIndex: elementIndex,
		ValueIndex:   valueIndex,
		Target:       targetFromArgument(toolCall.Arguments["target"]),
	}).Get(ctx, nil)
	return ToolCallResult{ToolCall: toolCall, Error: err}, true
}

func executeSecureMultipleTool(ctx workflow.Context, workflowID string, toolCall ToolCall, actionID string, ciphertext []byte) (ToolCallResult, bool) {
	fields, ok := toolCall.Arguments["fields"].([]interface{})
	if !ok {
		return ToolCallResult{}, false
	}
	hasSecureValue := false
	for _, rawField := range fields {
		field, ok := rawField.(map[string]interface{})
		if !ok {
			continue
		}
		text, _ := field["text"].(string)
		if strings.Contains(text, secureUserActionValuePrefix) {
			hasSecureValue = true
			break
		}
	}
	if !hasSecureValue {
		return ToolCallResult{}, false
	}
	if actionID == "" || len(ciphertext) == 0 {
		return ToolCallResult{ToolCall: toolCall, Error: fmt.Errorf("secure user-action answer is unavailable")}, true
	}
	for _, rawField := range fields {
		field, ok := rawField.(map[string]interface{})
		if !ok {
			return ToolCallResult{ToolCall: toolCall, Error: fmt.Errorf("input_multiple contains an invalid field")}, true
		}
		text, _ := field["text"].(string)
		elementIndex, ok := toolArgumentInt(field["element_index"])
		if !ok {
			return ToolCallResult{ToolCall: toolCall, Error: fmt.Errorf("input_multiple contains an invalid element index")}, true
		}
		if strings.Contains(text, secureUserActionValuePrefix) {
			valueIndex, valid := secureUserActionValueIndex(text)
			if !valid {
				return ToolCallResult{ToolCall: toolCall, Error: fmt.Errorf("secure user-action answer marker is invalid")}, true
			}
			if err := workflow.ExecuteActivity(ctx, "TypeUserActionSecure", browseractivity.SecureTypeInput{
				ActionID:     actionID,
				Operation:    "user_action_value",
				Ciphertext:   ciphertext,
				WorkflowID:   workflowID,
				ElementIndex: elementIndex,
				ValueIndex:   valueIndex,
				Target:       targetFromArgument(field["target"]),
			}).Get(ctx, nil); err != nil {
				return ToolCallResult{ToolCall: toolCall, Error: err}, true
			}
			continue
		}
		if err := workflow.ExecuteActivity(ctx, "Type", browseractivity.TypeInput{
			WorkflowID:   workflowID,
			ElementIndex: elementIndex,
			Text:         text,
			Replace:      true,
			Target:       targetFromArgument(field["target"]),
		}).Get(ctx, nil); err != nil {
			return ToolCallResult{ToolCall: toolCall, Error: err}, true
		}
	}
	return ToolCallResult{ToolCall: toolCall, Result: map[string]interface{}{}}, true
}

func targetFromArgument(value interface{}) *sqldb.BrowserMutationTarget {
	switch target := value.(type) {
	case *sqldb.BrowserMutationTarget:
		return target
	case sqldb.BrowserMutationTarget:
		return &target
	case map[string]interface{}:
		out := &sqldb.BrowserMutationTarget{}
		out.Role, _ = target["role"].(string)
		out.Name, _ = target["name"].(string)
		out.Label, _ = target["label"].(string)
		out.Selector, _ = target["selector"].(string)
		out.Submit, _ = target["submit"].(bool)
		if out.Role == "" && out.Name == "" && out.Label == "" && out.Selector == "" && !out.Submit {
			return nil
		}
		return out
	default:
		return nil
	}
}

func secureUserActionValueIndex(text string) (int, bool) {
	const suffix = "__"
	if !strings.HasPrefix(text, secureUserActionValuePrefix) || !strings.HasSuffix(text, suffix) {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(text, secureUserActionValuePrefix), suffix)
	index, err := strconv.Atoi(raw)
	return index, err == nil && index >= 0
}

func toolArgumentInt(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, typed >= 0
	case int64:
		return int(typed), typed >= 0
	case float64:
		index := int(typed)
		return index, typed >= 0 && float64(index) == typed
	default:
		return 0, false
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
			"failure_status": map[string]interface{}{
				"anyOf": []map[string]interface{}{
					{
						"type": "string",
						"enum": []string{
							string(PlannerFailureStatusCaptcha),
							string(PlannerFailureStatusTruthfulness),
							string(PlannerFailureStatusLoginRequired),
							string(PlannerFailureStatusSubmissionError),
							string(PlannerFailureStatusOther),
						},
					},
					{"type": "null"},
				},
				"description": "Categorized failure status when is_application_failed is true; null otherwise",
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
		"required": []string{"is_application_complete", "is_application_failed", "failure_reason", "failure_status", "reasoning", "tool_call", "questions_answered"},
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
	if len(questions) < 2 {
		return append([]sqldb.JobApplicationQuestion(nil), questions...), nil
	}

	pairs := make([]qaCandidatePair, 0, len(questions)*(len(questions)-1)/2)
	jevQuestions := make(map[string]types.JevQuestion, len(questions)*(len(questions)-1))
	for first := 0; first < len(questions); first++ {
		for second := first + 1; second < len(questions); second++ {
			pair := qaCandidatePair{first: first, second: second}
			pairs = append(pairs, pair)
			jevQuestions[qaSameQuestionKey(pair)] = types.JevQuestion{
				Type:         "noul",
				Instructions: fmt.Sprintf("Do Q&A entries %d and %d clearly express the exact same underlying application question, despite any wording differences? Be conservative: distinct intent means false.", first, second),
				Criteria: map[string]string{
					"true":  "Both entries ask for the same underlying fact or response.",
					"false": "The entries have different intent, or equivalence is uncertain.",
				},
			}
			jevQuestions[qaCompatibleAnswersKey(pair)] = types.JevQuestion{
				Type:         "noul",
				Instructions: fmt.Sprintf("Are the answers for Q&A entries %d and %d clearly the same value, allowing only harmless formatting or case differences? Do not treat contradictory or substantively different answers as compatible.", first, second),
				Criteria: map[string]string{
					"true":  "The answers clearly express the same value.",
					"false": "The answers conflict, differ substantively, or equivalence is uncertain.",
				},
			}
		}
	}

	state := struct {
		Questions []indexedQA `json:"questions"`
	}{Questions: make([]indexedQA, len(questions))}
	for index, question := range questions {
		state.Questions[index] = indexedQA{Index: index, Question: question.Question, Answer: question.Answer}
	}

	var jevResponse types.JevResponse
	if err := workflow.ExecuteActivity(ctx, "CallJev", types.JevRequest{
		State:            state,
		Questions:        jevQuestions,
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}).Get(ctx, &jevResponse); err != nil {
		return nil, fmt.Errorf("CallJev Q&A deduplication: %w", err)
	}

	decisions, err := validateQADedupDecisions(jevResponse.Answers, pairs, len(questions))
	if err != nil {
		return nil, err
	}
	return mergeEquivalentQA(questions, decisions), nil
}

const qaJevThreshold = 0.8

type indexedQA struct {
	Index    int    `json:"index"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type qaCandidatePair struct {
	first  int
	second int
}

type qaPairDecision struct {
	sameQuestion     bool
	compatibleAnswer bool
}

func qaSameQuestionKey(pair qaCandidatePair) string {
	return fmt.Sprintf("same_question_%d_%d", pair.first, pair.second)
}

func qaCompatibleAnswersKey(pair qaCandidatePair) string {
	return fmt.Sprintf("compatible_answers_%d_%d", pair.first, pair.second)
}

func validateQADedupDecisions(answers map[string]types.JevAnswer, pairs []qaCandidatePair, questionCount int) (map[qaCandidatePair]qaPairDecision, error) {
	expected := make(map[string]qaCandidatePair, len(pairs)*2)
	seenPairs := make(map[qaCandidatePair]struct{}, len(pairs))
	for _, pair := range pairs {
		if pair.first < 0 || pair.second <= pair.first || pair.second >= questionCount {
			return nil, fmt.Errorf("JEV Q&A deduplication has invalid candidate indexes %d,%d", pair.first, pair.second)
		}
		if _, ok := seenPairs[pair]; ok {
			return nil, fmt.Errorf("JEV Q&A deduplication has duplicate candidate indexes %d,%d", pair.first, pair.second)
		}
		seenPairs[pair] = struct{}{}
		expected[qaSameQuestionKey(pair)] = pair
		expected[qaCompatibleAnswersKey(pair)] = pair
	}
	for key := range answers {
		if _, ok := expected[key]; !ok {
			return nil, fmt.Errorf("JEV Q&A deduplication returned unexpected decision %q", key)
		}
	}
	if len(answers) != len(expected) {
		return nil, fmt.Errorf("JEV Q&A deduplication returned %d decisions, want %d", len(answers), len(expected))
	}

	decisions := make(map[qaCandidatePair]qaPairDecision, len(pairs))
	for key, pair := range expected {
		answer, ok := answers[key]
		if !ok {
			return nil, fmt.Errorf("JEV Q&A deduplication is missing decision %q", key)
		}
		if answer.Type != "noul" || answer.Noul == nil || math.IsNaN(*answer.Noul) || math.IsInf(*answer.Noul, 0) || *answer.Noul < 0 || *answer.Noul > 1 {
			return nil, fmt.Errorf("JEV Q&A deduplication returned invalid decision %q", key)
		}
		decision := decisions[pair]
		if key == qaSameQuestionKey(pair) {
			decision.sameQuestion = *answer.Noul >= qaJevThreshold
		} else {
			decision.compatibleAnswer = *answer.Noul >= qaJevThreshold
		}
		decisions[pair] = decision
	}
	return decisions, nil
}

func mergeEquivalentQA(questions []sqldb.JobApplicationQuestion, decisions map[qaCandidatePair]qaPairDecision) []sqldb.JobApplicationQuestion {
	groups := make([][]int, 0, len(questions))
	for index := range questions {
		merged := false
		for groupIndex, group := range groups {
			canMerge := true
			for _, member := range group {
				pair := qaCandidatePair{first: member, second: index}
				decision, ok := decisions[pair]
				if !ok || !decision.sameQuestion || !decision.compatibleAnswer {
					canMerge = false
					break
				}
			}
			if canMerge {
				groups[groupIndex] = append(group, index)
				merged = true
				break
			}
		}
		if !merged {
			groups = append(groups, []int{index})
		}
	}

	mergedQuestions := make([]sqldb.JobApplicationQuestion, 0, len(groups))
	for _, group := range groups {
		selected := questions[group[0]]
		for _, index := range group[1:] {
			if len(strings.TrimSpace(questions[index].Question)) > len(strings.TrimSpace(selected.Question)) {
				selected.Question = questions[index].Question
			}
		}
		mergedQuestions = append(mergedQuestions, selected)
	}
	return mergedQuestions
}
