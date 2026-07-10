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
	Success bool   `json:"success"`
	Reason  string `json:"reason"`
}

func SubmitApplicationWorkflow(ctx workflow.Context, input SubmitApplicationWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)

	clickOut, err := clickSubmit(ctx, input)
	if err != nil {
		logger.Error("Failed to click submit", "error", err)
		return nil, err
	}

	verifyOut, err := verifySubmissionState(ctx, input.WorkflowID, clickOut.BeforeURL)
	if err != nil {
		logger.Error("Failed to verify submission state", "error", err)
		return nil, err
	}

	if result, decided := decideDeterministically(clickOut, verifyOut); decided {
		return result, nil
	}

	return decideWithLLM(ctx, input, clickOut, verifyOut)
}

// clickSubmit physically clicks the submit button, so it must never retry:
// a retry after a successful click would double-submit the application.
func clickSubmit(ctx workflow.Context, input SubmitApplicationWorkflowInput) (browser.ClickSubmitOutput, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	})

	var output browser.ClickSubmitOutput
	err := workflow.ExecuteActivity(ctx, "ClickSubmitAndCapture", browser.ClickSubmitInput{
		WorkflowID:   input.WorkflowID,
		ElementIndex: input.ElementIndex,
	}).Get(ctx, &output)
	if err != nil {
		return output, fmt.Errorf("click submit and capture: %w", err)
	}
	return output, nil
}

// verifySubmissionState is read-only, so it retries safely.
func verifySubmissionState(ctx workflow.Context, workflowID string, beforeURL string) (browser.VerifySubmissionStateOutput, error) {
	ctx = withRetryableOptions(ctx)

	var output browser.VerifySubmissionStateOutput
	err := workflow.ExecuteActivity(ctx, "VerifySubmissionState", browser.VerifySubmissionStateInput{
		WorkflowID: workflowID,
		BeforeURL:  beforeURL,
	}).Get(ctx, &output)
	if err != nil {
		return output, fmt.Errorf("verify submission state: %w", err)
	}
	return output, nil
}

func withRetryableOptions(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    15 * time.Second,
			MaximumAttempts:    3,
		},
	})
}

// decideDeterministically resolves the clear cases without an LLM call.
// A client-side validation failure fires no submit request, so a captured
// 2xx/3xx submit-shaped request with no validation errors is trustworthy.
func decideDeterministically(clickOut browser.ClickSubmitOutput, verifyOut browser.VerifySubmissionStateOutput) (map[string]interface{}, bool) {
	best := bestSubmitRequest(clickOut.Requests)
	hasValidationErrors := len(verifyOut.ValidationErrors) > 0

	if best != nil && best.StatusCode >= 200 && best.StatusCode < 400 && !hasValidationErrors {
		return submissionResult(true, "network",
			fmt.Sprintf("%s %s returned %d, no validation errors on page", best.Method, best.URL, best.StatusCode)), true
	}

	if hasValidationErrors && (best == nil || best.StatusCode >= 400) {
		return submissionResult(false, "validation_errors",
			fmt.Sprintf("Validation errors on page: %v", verifyOut.ValidationErrors)), true
	}

	if verifyOut.SuccessText != "" && !hasValidationErrors {
		return submissionResult(true, "success_element",
			fmt.Sprintf("Found success text: %s", verifyOut.SuccessText)), true
	}

	if best != nil && best.StatusCode >= 400 {
		return submissionResult(false, "network_error",
			fmt.Sprintf("%s %s returned %d", best.Method, best.URL, best.StatusCode)), true
	}

	// Conflicting or missing signals (e.g. no submit request captured at all).
	return nil, false
}

// bestSubmitRequest prefers document navigations (classic form POST) over
// XHR/fetch, and later requests over earlier ones.
func bestSubmitRequest(requests []browser.CapturedRequest) *browser.CapturedRequest {
	var best *browser.CapturedRequest
	for i := range requests {
		req := &requests[i]
		if req.StatusCode == 0 {
			continue
		}
		if best == nil || req.ResourceType == "Document" || best.ResourceType != "Document" {
			best = req
		}
	}
	return best
}

func decideWithLLM(ctx workflow.Context, input SubmitApplicationWorkflowInput, clickOut browser.ClickSubmitOutput, verifyOut browser.VerifySubmissionStateOutput) (map[string]interface{}, error) {
	ctx = withRetryableOptions(ctx)

	systemPrompt := "You are verifying whether a job application was successfully submitted after the submit button was clicked. " +
		"You are given the network requests captured during the click, and the state of the page afterwards. " +
		"A captured submit request with a 2xx/3xx status strongly suggests success. " +
		"Visible validation errors strongly suggest failure. " +
		"A URL change or new tab alone does NOT prove success. Be conservative: only report success if the evidence supports it."

	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      buildLLMEvidence(clickOut, verifyOut),
		Model:            "x-ai/grok-4.3",
		ResponseSchema:   getVerifyResponseSchema(),
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return nil, fmt.Errorf("CallLLM: %w", err)
	}

	var verifyResponse submitVerifyResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &verifyResponse); err != nil {
		return nil, fmt.Errorf("unmarshal verify response: %w", err)
	}

	return submissionResult(verifyResponse.Success, "llm", verifyResponse.Reason), nil
}

func buildLLMEvidence(clickOut browser.ClickSubmitOutput, verifyOut browser.VerifySubmissionStateOutput) string {
	evidence := "Network requests captured during submit click:\n"
	if len(clickOut.Requests) == 0 {
		evidence += "(none)\n"
	}
	for _, req := range clickOut.Requests {
		evidence += fmt.Sprintf("- %s %s (%s) -> status %d\n  response body: %s\n",
			req.Method, req.URL, req.ResourceType, req.StatusCode, req.ResponseBody)
	}

	evidence += fmt.Sprintf("\nPage state after click:\n- URL changed: %t (now %s)\n- New tab opened: %t\n- Form still present: %t\n- Success text found: %q\n- Validation errors: %v\n\nPage text:\n%s",
		verifyOut.URLChanged, verifyOut.CurrentURL, clickOut.NewTabOpened, verifyOut.FormPresent,
		verifyOut.SuccessText, verifyOut.ValidationErrors, verifyOut.PageText)

	evidence += "\n\nWas the job application successfully submitted?"
	return evidence
}

func submissionResult(submitted bool, method string, message string) map[string]interface{} {
	return map[string]interface{}{
		"submitted": submitted,
		"method":    method,
		"message":   message,
	}
}

func getVerifyResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"success": map[string]interface{}{
				"type":        "boolean",
				"description": "Whether the application was successfully submitted",
			},
			"reason": map[string]interface{}{
				"type":        "string",
				"description": "One-sentence justification citing the evidence",
			},
		},
		"required": []string{"success", "reason"},
	}
}
