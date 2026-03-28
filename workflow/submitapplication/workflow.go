package submitapplication

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type SubmitApplicationWorkflowInput struct {
	WorkflowID       string `json:"workflow_id"`
	ElementIndex     int    `json:"element_index"`
	IdUser           uint   `json:"id_user"`
	IdJobApplication *uint  `json:"id_job_application,omitempty"`
}

type submitVerifyResponse struct {
	Success bool `json:"success"`
}

func SubmitApplicationWorkflow(ctx workflow.Context, input SubmitApplicationWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    15 * time.Second,
			MaximumAttempts:    3,
		},
	})

	formAction, err := getFormAction(ctx, input.WorkflowID)
	if err != nil {
		logger.Error("Failed to get form action", "error", err)
		return nil, err
	}

	if formAction.HasAction {
		return handleNetworkInterception(ctx, input, formAction)
	}

	return handleFallbackDetection(ctx, input, formAction.CurrentURL)
}

func getFormAction(ctx workflow.Context, workflowID string) (browser.GetFormActionOutput, error) {
	var output browser.GetFormActionOutput
	err := workflow.ExecuteActivity(ctx, "GetFormAction", browser.GetFormActionInput{
		WorkflowID: workflowID,
	}).Get(ctx, &output)
	return output, err
}

func handleNetworkInterception(ctx workflow.Context, input SubmitApplicationWorkflowInput, formAction browser.GetFormActionOutput) (map[string]interface{}, error) {
	var hijackOutput browser.HijackSubmitClickOutput
	err := workflow.ExecuteActivity(ctx, "HijackSubmitClick", browser.HijackSubmitClickInput{
		WorkflowID:   input.WorkflowID,
		ElementIndex: input.ElementIndex,
		ActionURL:    formAction.Action,
	}).Get(ctx, &hijackOutput)
	if err != nil {
		return nil, fmt.Errorf("hijack submit click: %w", err)
	}

	if hijackOutput.TimedOut {
		return runFallbackAfterClick(ctx, input, formAction.CurrentURL)
	}

	if hijackOutput.StatusCode >= 200 && hijackOutput.StatusCode < 300 {
		success, err := verifyResponseWithLLM(ctx, hijackOutput.ResponseBody, input.IdUser, input.IdJobApplication)
		if err != nil {
			return nil, fmt.Errorf("verify response with LLM: %w", err)
		}
		if success {
			return map[string]interface{}{
				"submitted": true,
				"method":    "network_interception",
				"message":   fmt.Sprintf("Server returned %d, LLM confirmed success", hijackOutput.StatusCode),
			}, nil
		}
	}

	return map[string]interface{}{
		"submitted": false,
		"method":    "network_interception",
		"message":   fmt.Sprintf("Server returned %d", hijackOutput.StatusCode),
	}, nil
}

func runFallbackAfterClick(ctx workflow.Context, input SubmitApplicationWorkflowInput, beforeURL string) (map[string]interface{}, error) {
	var fallbackOutput browser.CheckSubmissionFallbackOutput
	err := workflow.ExecuteActivity(ctx, "CheckSubmissionFallback", browser.CheckSubmissionFallbackInput{
		WorkflowID:   input.WorkflowID,
		ElementIndex: input.ElementIndex,
		BeforeURL:    beforeURL,
		SkipClick:    true,
	}).Get(ctx, &fallbackOutput)
	if err != nil {
		return nil, fmt.Errorf("check submission fallback: %w", err)
	}

	return map[string]interface{}{
		"submitted": fallbackOutput.Submitted,
		"method":    fallbackOutput.DetectionMethod,
		"message":   fallbackOutput.Message,
	}, nil
}

func handleFallbackDetection(ctx workflow.Context, input SubmitApplicationWorkflowInput, beforeURL string) (map[string]interface{}, error) {
	var fallbackOutput browser.CheckSubmissionFallbackOutput
	err := workflow.ExecuteActivity(ctx, "CheckSubmissionFallback", browser.CheckSubmissionFallbackInput{
		WorkflowID:   input.WorkflowID,
		ElementIndex: input.ElementIndex,
		BeforeURL:    beforeURL,
		SkipClick:    false,
	}).Get(ctx, &fallbackOutput)
	if err != nil {
		return nil, fmt.Errorf("check submission fallback: %w", err)
	}

	return map[string]interface{}{
		"submitted": fallbackOutput.Submitted,
		"method":    fallbackOutput.DetectionMethod,
		"message":   fallbackOutput.Message,
	}, nil
}

func verifyResponseWithLLM(ctx workflow.Context, responseBody string, idUser uint, idJobApplication *uint) (bool, error) {
	systemPrompt := "You are verifying whether an HTTP response indicates a successful job application submission. Analyze the response body and determine if the application was submitted successfully. An empty or null response body also indicates success (many servers return empty 200 responses on successful form submissions)."
	userPrompt := fmt.Sprintf("HTTP Response Body:\n\n%s\n\nDoes this response indicate a successful job application submission?", responseBody)

	if responseBody == "" {
		return true, nil
	}

	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            "x-ai/grok-4.1-fast",
		ResponseSchema:   getVerifyResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return false, fmt.Errorf("CallLLM: %w", err)
	}

	var verifyResponse submitVerifyResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &verifyResponse); err != nil {
		return false, fmt.Errorf("unmarshal verify response: %w", err)
	}

	return verifyResponse.Success, nil
}

func getVerifyResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the response indicates a successful job application submission",
			},
		},
		"required": []string{"success"},
	}
}
