package handleuseraction

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/SomtoJF/iris-worker/helper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var SystemTemplate *template.Template

func SetTemplates() {
	var err error
	SystemTemplate, err = helper.LoadTemplate("workflow/handleuseraction/prompt/system.go.tmpl")
	if err != nil {
		panic(err)
	}
}

type HandleUserActionWorkflowInput struct {
	WorkflowID       string `json:"workflow_id"`
	IdUser           uint   `json:"id_user"`
	IdJobApplication uint   `json:"id_job_application"`
	UserAction       string `json:"user_action"`
	ActionDetails    string `json:"action_details"`
}

const signalName = "USER_ACTION_RESULT"

func HandleUserActionWorkflow(ctx workflow.Context, input HandleUserActionWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("HandleUserActionWorkflow started", "input", input)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})

	childWorkflowID := workflow.GetInfo(ctx).WorkflowExecution.ID

	// Mark job application as blocked
	if err := updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusBlocked); err != nil {
		logger.Error("Failed to mark job application as blocked", "error", err)
		return nil, err
	}

	// Fetch job application for context
	jobApplication, err := getJobApplication(ctx, input.IdJobApplication)
	if err != nil {
		logger.Error("Failed to fetch job application", "error", err)
		return nil, err
	}

	// Take screenshot of current page
	screenshot, err := takeScreenshot(ctx, input.WorkflowID)
	if err != nil {
		logger.Error("Failed to take screenshot", "error", err)
		return nil, err
	}

	// Call LLM to build the user action layout and form description from the screenshot
	formDescription, layout, err := buildUserActionLayout(ctx, screenshot, input.UserAction, input.ActionDetails, jobApplication.JobTitle, jobApplication.CompanyName, input.IdUser, input.IdJobApplication)
	if err != nil {
		logger.Error("Failed to build user action layout", "error", err)
		return nil, err
	}

	if err := workflow.ExecuteActivity(ctx, "DeletePendingUserActions", sqldb.DeletePendingUserActionsInput{
		JobApplicationId: input.IdJobApplication,
	}).Get(ctx, nil); err != nil {
		logger.Error("Failed to delete pending user actions", "error", err)
		return nil, err
	}

	// Create user action record in DB
	userAction, err := createUserAction(ctx, sqldb.CreateUserActionInput{
		UserId:           input.IdUser,
		JobApplicationId: input.IdJobApplication,
		UserActionType:   input.UserAction,
		ActionDetails:    formDescription,
		WorkflowID:       childWorkflowID,
		Layout:           layout,
	})
	if err != nil {
		logger.Error("Failed to create user action record", "error", err)
		return nil, err
	}

	// Notify user
	if err := notifyUser(ctx, input.IdUser, notifyUserInput{
		UserActionID:   userAction.IdUserAction,
		UserActionType: input.UserAction,
		ActionDetails:  formDescription,
		Layout:         layout,
		WorkflowID:     childWorkflowID,
		SignalName:     signalName,
		JobTitle:       jobApplication.JobTitle,
		CompanyName:    jobApplication.CompanyName,
		ApplicationID:  jobApplication.IdExternal.String(),
	}); err != nil {
		logger.Error("Failed to notify user", "error", err)
		return nil, err
	}

	// Wait for user action signal (indefinitely; canceled when parent closes)
	result, err := waitForUserAction(ctx)
	if err != nil {
		logger.Error("Canceled or error waiting for user action", "error", err)
		return nil, err
	}

	// Mark user action as resolved
	if err := workflow.ExecuteActivity(ctx, "UpdateUserAction", sqldb.UpdateUserActionInput{
		IdUserAction: userAction.IdUserAction,
		Data:         map[string]interface{}{"is_pending": false},
	}).Get(ctx, nil); err != nil {
		logger.Error("Failed to update user action record", "error", err)
	}

	// Restore job application status to processing
	if err := updateJobApplicationStatus(ctx, input.IdJobApplication, sqldb.JobApplicationStatusPending); err != nil {
		logger.Error("Failed to restore job application status", "error", err)
	}

	return map[string]interface{}{
		"user_action_result": result,
	}, nil
}

// ====== HELPERS ======

func updateJobApplicationStatus(ctx workflow.Context, idJobApplication uint, status sqldb.JobApplicationStatus) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: idJobApplication,
		Data:             map[string]interface{}{"status": status},
	}).Get(ctx, nil)
}

func takeScreenshot(ctx workflow.Context, workflowID string) (browser.TakeScreenshotOutput, error) {
	var output browser.TakeScreenshotOutput
	err := workflow.ExecuteActivity(ctx, "TakeScreenshot", browser.TakeScreenshotInput{
		WorkflowID: workflowID,
		FileName:   "user_action_screenshot.png",
	}).Get(ctx, &output)
	return output, err
}

func buildUserActionLayout(ctx workflow.Context, screenshot browser.TakeScreenshotOutput, userAction string, actionDetails string, jobTitle string, companyName string, idUser uint, idJobApplication uint) (string, sqldb.UserActionLayout, error) {
	promptData := struct {
		UserAction    string
		ActionDetails string
		JobTitle      string
		CompanyName   string
	}{
		UserAction:    userAction,
		ActionDetails: actionDetails,
		JobTitle:      jobTitle,
		CompanyName:   companyName,
	}

	var buf bytes.Buffer
	if err := SystemTemplate.Execute(&buf, promptData); err != nil {
		return "", nil, fmt.Errorf("render system prompt: %w", err)
	}

	screenshotBase64, err := getBase64Screenshot(screenshot.Path)
	if err != nil {
		return "", nil, fmt.Errorf("encode screenshot: %w", err)
	}

	userMessage := buildUserActionUserMessage(actionDetails, screenshot.TaggedNodes)

	formDescription, layout, err := callUserActionLayoutLLM(ctx, buf.String(), userMessage, screenshotBase64, idUser, idJobApplication)
	if err != nil {
		return "", nil, err
	}

	if missing := choiceFieldsMissingOptions(layout); len(missing) > 0 {
		retryMsg := userMessage + "\n\nCRITICAL RETRY: Your previous layout omitted options for these choice fields: " +
			strings.Join(missing, "; ") +
			". Re-read the screenshot and tagged elements. For every select/radio/checkbox/combobox you MUST return a non-empty options array with every visible choice label. Do not use free-text input for those fields."
		formDescription, layout, err = callUserActionLayoutLLM(ctx, buf.String(), retryMsg, screenshotBase64, idUser, idJobApplication)
		if err != nil {
			return "", nil, err
		}
		if missing = choiceFieldsMissingOptions(layout); len(missing) > 0 {
			return "", nil, fmt.Errorf("user action layout missing options for: %s", strings.Join(missing, "; "))
		}
	}

	return formDescription, layout, nil
}

func buildUserActionUserMessage(actionDetails string, taggedNodes []browserfactory.SerializableTaggedNode) string {
	var b strings.Builder
	b.WriteString("Analyze the screenshot and return the form description and layout for this user action.\n")
	b.WriteString("Action details: ")
	b.WriteString(actionDetails)
	b.WriteString("\n")
	if len(taggedNodes) == 0 {
		b.WriteString("No tagged interactive elements were extracted; rely on the screenshot.")
		return b.String()
	}
	b.WriteString("Tagged interactive elements on the page (use these to recover option labels for radios/selects/checkboxes):\n")
	for _, n := range taggedNodes {
		b.WriteString(fmt.Sprintf("- [%d] role=%q desc=%q", n.Index, n.Role, n.Description))
		if n.Value != nil && *n.Value != "" {
			b.WriteString(fmt.Sprintf(" value=%q", *n.Value))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func callUserActionLayoutLLM(ctx workflow.Context, systemMessage, userMessage, screenshotBase64 string, idUser uint, idJobApplication uint) (string, sqldb.UserActionLayout, error) {
	var temperaturePtr float64 = 0.2

	llmRequest := types.AIPIRequest{
		SystemMessage:    systemMessage,
		UserMessage:      userMessage,
		ImageUrl:         &screenshotBase64,
		Model:            "x-ai/grok-4.3",
		ResponseSchema:   getUserActionResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
		Temperature:      &temperaturePtr,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return "", nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var response struct {
		FormDescription string                 `json:"form_description"`
		Layout          sqldb.UserActionLayout `json:"layout"`
	}
	if err := json.Unmarshal([]byte(llmResponse.Content), &response); err != nil {
		return "", nil, fmt.Errorf("parse LLM response: %w", err)
	}

	return response.FormDescription, response.Layout, nil
}

// choiceFieldsMissingOptions returns field names for select/radio/checkbox/combobox
// entries that have no options — those layouts are unanswerable in the UI.
func choiceFieldsMissingOptions(layout sqldb.UserActionLayout) []string {
	var missing []string
	for _, item := range layout {
		comp := ""
		if item.Component != nil {
			comp = strings.ToLower(strings.TrimSpace(*item.Component))
		}
		switch comp {
		case "select", "radio", "checkbox", "combobox":
			if item.Options == nil || len(*item.Options) == 0 {
				missing = append(missing, item.FieldName)
			}
		}
	}
	return missing
}

func createUserAction(ctx workflow.Context, input sqldb.CreateUserActionInput) (sqldb.UserAction, error) {
	var userAction sqldb.UserAction
	err := workflow.ExecuteActivity(ctx, "CreateUserAction", input).Get(ctx, &userAction)
	return userAction, err
}

func getJobApplication(ctx workflow.Context, idJobApplication uint) (sqldb.JobApplication, error) {
	var jobApp sqldb.JobApplication
	err := workflow.ExecuteActivity(ctx, "GetJobApplication", sqldb.GetJobApplicationInput{
		IdJobApplication: idJobApplication,
	}).Get(ctx, &jobApp)
	return jobApp, err
}

type notifyUserInput struct {
	UserActionID   uint
	UserActionType string
	ActionDetails  string
	Layout         sqldb.UserActionLayout
	WorkflowID     string
	SignalName     string
	JobTitle       string
	CompanyName    string
	ApplicationID  string
}

func notifyUser(ctx workflow.Context, userID uint, input notifyUserInput) error {
	return workflow.ExecuteActivity(ctx, "PublishRedisEvent", userID, string(realtimeevent.EventUserActionRequired), map[string]interface{}{
		"user_action_id":   input.UserActionID,
		"user_action_type": input.UserActionType,
		"action_details":   input.ActionDetails,
		"layout":           input.Layout,
		"workflow_id":      input.WorkflowID,
		"signal_name":      input.SignalName,
		"job_title":        input.JobTitle,
		"company_name":     input.CompanyName,
		"application_id":   input.ApplicationID,
	}).Get(ctx, nil)
}

func waitForUserAction(ctx workflow.Context) (sqldb.UserActionResult, error) {
	signalChan := workflow.GetSignalChannel(ctx, signalName)

	var result sqldb.UserActionResult
	selector := workflow.NewSelector(ctx)
	signalReceived := false

	selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &result)
		signalReceived = true
	})
	selector.AddReceive(ctx.Done(), func(c workflow.ReceiveChannel, more bool) {})
	selector.Select(ctx)

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !signalReceived {
		return nil, fmt.Errorf("no user action signal received")
	}
	return result, nil
}

func getBase64Screenshot(screenshotPath string) (string, error) {
	data, err := os.ReadFile(screenshotPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(data)), nil
}

func getUserActionResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"form_description": map[string]interface{}{
				"type":        "string",
				"description": "Plain-text description (no tags, max 3 lines) of what the user needs to do",
			},
			"layout": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"field_name": map[string]interface{}{
							"type":        "string",
							"description": "Human-readable label for the field",
						},
						"description": map[string]interface{}{
							"anyOf": []map[string]interface{}{
								{"type": "string"},
								{"type": "null"},
							},
							"description": "Short context about what this field is asking, assuming the user hasn't read the job description. Null for self-explanatory fields.",
						},
						"type": map[string]interface{}{
							"anyOf": []map[string]interface{}{
								{"type": "string"},
								{"type": "null"},
							},
							"description": "HTML input type (text, password, email, etc.)",
						},
						"component": map[string]interface{}{
							"anyOf": []map[string]interface{}{
								{"type": "string"},
								{"type": "null"},
							},
							"description": "UI component type (input, textarea, select, radio, checkbox)",
						},
						"options": map[string]interface{}{
							"anyOf": []map[string]interface{}{
								{"type": "array", "items": map[string]interface{}{"type": "string"}, "minItems": 1},
								{"type": "null"},
							},
							"description": "REQUIRED non-empty array of every visible choice when component is select, radio, checkbox, or combobox. Null only for free-text input/textarea.",
						},
					},
					"required": []string{"field_name", "description", "type", "component", "options"},
				},
			},
		},
		"required": []string{"form_description", "layout"},
	}
}
