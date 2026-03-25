package handleuseraction

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"text/template"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
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

	// Take screenshot of current page
	screenshot, err := takeScreenshot(ctx, input.WorkflowID)
	if err != nil {
		logger.Error("Failed to take screenshot", "error", err)
		return nil, err
	}

	// Call LLM to build the user action layout from the screenshot
	layout, err := buildUserActionLayout(ctx, screenshot.Path, input.UserAction, input.ActionDetails)
	if err != nil {
		logger.Error("Failed to build user action layout", "error", err)
		return nil, err
	}

	// Create user action record in DB
	userAction, err := createUserAction(ctx, sqldb.CreateUserActionInput{
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
		UserActionType:   input.UserAction,
		ActionDetails:    input.ActionDetails,
		Layout:           layout,
	})
	if err != nil {
		logger.Error("Failed to create user action record", "error", err)
		return nil, err
	}

	// Fetch job application for notification context
	jobApplication, err := getJobApplication(ctx, input.IdJobApplication)
	if err != nil {
		logger.Error("Failed to fetch job application", "error", err)
		return nil, err
	}

	// Notify user
	if err := notifyUser(ctx, input.IdUser, notifyUserInput{
		UserActionID:    userAction.IdUserAction,
		UserActionType:  input.UserAction,
		ActionDetails:   input.ActionDetails,
		Layout:          layout,
		WorkflowID:      childWorkflowID,
		SignalName:      signalName,
		JobTitle:        jobApplication.JobTitle,
		CompanyName:     jobApplication.CompanyName,
		ApplicationID:   jobApplication.IdExternal.String(),
	}); err != nil {
		logger.Error("Failed to notify user", "error", err)
		return nil, err
	}

	// Wait for user action signal with 10-minute timeout
	result, err := waitForUserAction(ctx)
	if err != nil {
		logger.Error("Timeout or error waiting for user action", "error", err)
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

func buildUserActionLayout(ctx workflow.Context, screenshotPath string, userAction string, actionDetails string) (sqldb.UserActionLayout, error) {
	promptData := struct {
		UserAction    string
		ActionDetails string
	}{
		UserAction:    userAction,
		ActionDetails: actionDetails,
	}

	var buf bytes.Buffer
	if err := SystemTemplate.Execute(&buf, promptData); err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}

	screenshotBase64, err := getBase64Screenshot(screenshotPath)
	if err != nil {
		return nil, fmt.Errorf("encode screenshot: %w", err)
	}

	llmRequest := types.AIPIRequest{
		SystemMessage:  buf.String(),
		UserMessage:    "Analyze the screenshot and return the form layout as a JSON array.",
		ImageUrl:       &screenshotBase64,
		Model:          "x-ai/grok-4.1-fast",
		ResponseSchema: getUserActionLayoutSchema(),
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	var layout sqldb.UserActionLayout
	if err := json.Unmarshal([]byte(llmResponse.Content), &layout); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}

	return layout, nil
}

func createUserAction(ctx workflow.Context, input sqldb.CreateUserActionInput) (sqldb.UserAction, error) {
	var userAction sqldb.UserAction
	err := workflow.ExecuteActivity(ctx, "CreateUserAction", input).Get(ctx, &userAction)
	return userAction, err
}

func getJobApplication(ctx workflow.Context, idJobApplication uint) (sqldb.JobApplication, error) {
	var jobApp sqldb.JobApplication
	err := workflow.ExecuteActivity(ctx, "GetJobApplication", idJobApplication).Get(ctx, &jobApp)
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
	timerChan := workflow.NewTimer(ctx, 10*time.Minute)

	var result sqldb.UserActionResult
	selector := workflow.NewSelector(ctx)
	signalReceived := false

	selector.AddReceive(signalChan, func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx, &result)
		signalReceived = true
	})
	selector.AddFuture(timerChan, func(f workflow.Future) {})
	selector.Select(ctx)

	if !signalReceived {
		return nil, fmt.Errorf("timeout waiting for user action after 10 minutes")
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

func getUserActionLayoutSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "array",
		"items": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"field_name": map[string]interface{}{
					"type":        "string",
					"description": "Human-readable label for the field",
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
						{"type": "array", "items": map[string]interface{}{"type": "string"}},
						{"type": "null"},
					},
					"description": "Available options for select/radio/checkbox fields",
				},
			},
			"required": []string{"field_name", "type", "component", "options"},
		},
	}
}
