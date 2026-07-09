package solvecaptcha

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	browser "github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/captcha"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/SomtoJF/iris-worker/shared"
	"github.com/SomtoJF/iris-worker/workflow/handleuseraction"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type SolveCaptchaWorkflowInput struct {
	WorkflowID       string `json:"workflow_id"`
	IdUser           uint   `json:"id_user"`
	IdJobApplication uint   `json:"id_job_application"`
}

// maxVerifyAttempts bounds the detect->inject->click->verify loop so a captcha that
// won't clear can't spin forever before we fall back to the user.
const maxVerifyAttempts = 2

// SolveCaptchaWorkflow is a self-contained tool: detect args -> CapSolver -> inject ->
// LLM vision check -> click if needed -> verify. On failure it falls back to a manual
// user action. When it returns, the captcha is solved (or handed to the user). The
// planner never reasons about captchas.
func SolveCaptchaWorkflow(ctx workflow.Context, input SolveCaptchaWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})

	detected, err := detectCaptcha(ctx, input.WorkflowID)
	if err != nil {
		return nil, fmt.Errorf("detect captcha: %w", err)
	}
	if detected.Type == browser.CaptchaTypeNone {
		return solvedResult(true, "no captcha present"), nil
	}
	logger.Info("Captcha detected", "type", detected.Type, "site_key", detected.SiteKey)

	if detected.SiteKey == "" {
		return fallbackToUser(ctx, input, "captcha detected but sitekey could not be extracted")
	}

	solved, err := solveAndInject(ctx, input, detected)
	if err != nil {
		logger.Warn("CapSolver flow failed, falling back to user", "error", err)
		return fallbackToUser(ctx, input, err.Error())
	}
	if solved {
		return solvedResult(true, "captcha solved automatically"), nil
	}

	logger.Warn("Captcha not cleared after solve attempts, falling back to user")
	return fallbackToUser(ctx, input, "automated captcha solving did not clear the challenge")
}

// solveAndInject runs CapSolver, injects the token, then verifies via LLM vision,
// clicking a button when the vision check says one is required. Returns whether the
// captcha cleared.
func solveAndInject(ctx workflow.Context, input SolveCaptchaWorkflowInput, detected browser.DetectCaptchaOutput) (bool, error) {
	token, err := solveWithCapSolver(ctx, detected)
	if err != nil {
		return false, err
	}

	if _, err := injectToken(ctx, input.WorkflowID, detected.Type, token); err != nil {
		return false, fmt.Errorf("inject token: %w", err)
	}

	for attempt := 0; attempt < maxVerifyAttempts; attempt++ {
		verdict, err := visionVerify(ctx, input)
		if err != nil {
			return false, fmt.Errorf("vision verify: %w", err)
		}
		if verdict.IsSolved {
			return true, nil
		}
		if !verdict.NeedsButtonClick || verdict.ButtonSelector == "" {
			return false, nil
		}

		clicked, err := clickButton(ctx, input.WorkflowID, verdict.ButtonSelector)
		if err != nil {
			return false, fmt.Errorf("click captcha button: %w", err)
		}
		if !clicked {
			return false, nil
		}
	}

	// Final re-detect: if the captcha markers are gone, treat as solved.
	post, err := detectCaptcha(ctx, input.WorkflowID)
	if err != nil {
		return false, err
	}
	return post.Type == browser.CaptchaTypeNone, nil
}

// ====== ACTIVITY WRAPPERS ======

func detectCaptcha(ctx workflow.Context, workflowID string) (browser.DetectCaptchaOutput, error) {
	var out browser.DetectCaptchaOutput
	err := workflow.ExecuteActivity(ctx, "DetectCaptcha", browser.DetectCaptchaInput{
		WorkflowID: workflowID,
	}).Get(ctx, &out)
	return out, err
}

func solveWithCapSolver(ctx workflow.Context, detected browser.DetectCaptchaOutput) (string, error) {
	var out captcha.SolveWithCapSolverOutput
	err := workflow.ExecuteActivity(ctx, "SolveWithCapSolver", captcha.SolveWithCapSolverInput{
		Type:    detected.Type,
		SiteKey: detected.SiteKey,
		PageURL: detected.PageURL,
		Action:  detected.Action,
	}).Get(ctx, &out)
	return out.Token, err
}

func injectToken(ctx workflow.Context, workflowID, captchaType, token string) (browser.InjectCaptchaTokenOutput, error) {
	var out browser.InjectCaptchaTokenOutput
	err := workflow.ExecuteActivity(ctx, "InjectCaptchaToken", browser.InjectCaptchaTokenInput{
		WorkflowID: workflowID,
		Type:       captchaType,
		Token:      token,
	}).Get(ctx, &out)
	return out, err
}

func clickButton(ctx workflow.Context, workflowID, selector string) (bool, error) {
	var out browser.ClickCaptchaButtonOutput
	err := workflow.ExecuteActivity(ctx, "ClickCaptchaButton", browser.ClickCaptchaButtonInput{
		WorkflowID: workflowID,
		Selector:   selector,
	}).Get(ctx, &out)
	return out.Clicked, err
}

// ====== VISION VERIFICATION ======

type visionVerdict struct {
	IsSolved         bool   `json:"is_solved"`
	NeedsButtonClick bool   `json:"needs_button_click"`
	ButtonSelector   string `json:"button_selector"`
}

func visionVerify(ctx workflow.Context, input SolveCaptchaWorkflowInput) (visionVerdict, error) {
	var screenshot browser.TakeScreenshotOutput
	if err := workflow.ExecuteActivity(ctx, "TakeScreenshot", browser.TakeScreenshotInput{
		WorkflowID: input.WorkflowID,
		FileName:   "captcha_verify.png",
	}).Get(ctx, &screenshot); err != nil {
		return visionVerdict{}, err
	}

	screenshotBase64, err := getBase64Screenshot(screenshot.Path)
	if err != nil {
		return visionVerdict{}, err
	}

	systemPrompt := "You verify whether a CAPTCHA on a job application page has been solved. " +
		"A token was just injected programmatically. Look at the screenshot. If the captcha is " +
		"clearly solved (green check, no challenge visible, form ready to submit), set is_solved=true. " +
		"If a button must be clicked to confirm/continue the captcha (e.g. a 'Verify' button or the " +
		"reCAPTCHA checkbox), set needs_button_click=true and provide a specific CSS selector for it in " +
		"button_selector. Do NOT return the application's final submit button. If unsure, set is_solved=false " +
		"and needs_button_click=false."

	var temperaturePtr float64 = 0.1
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      "Is the captcha solved, or does a button need clicking? Return the schema.",
		ImageUrl:         &screenshotBase64,
		Model:            "x-ai/grok-4.3",
		ResponseSchema:   visionResponseSchema(),
		Temperature:      &temperaturePtr,
		IdUser:           input.IdUser,
		IdJobApplication: &input.IdJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return visionVerdict{}, err
	}

	var verdict visionVerdict
	if err := json.Unmarshal([]byte(llmResponse.Content), &verdict); err != nil {
		return visionVerdict{}, err
	}
	return verdict, nil
}

func visionResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"is_solved": map[string]interface{}{
				"type":        "boolean",
				"description": "True if the captcha is clearly solved and no further action is needed",
			},
			"needs_button_click": map[string]interface{}{
				"type":        "boolean",
				"description": "True if a button/checkbox must be clicked to complete the captcha",
			},
			"button_selector": map[string]interface{}{
				"type":        "string",
				"description": "CSS selector for the captcha button/checkbox to click. Empty if none.",
			},
		},
		"required": []string{"is_solved", "needs_button_click", "button_selector"},
	}
}

// ====== FALLBACK ======

func fallbackToUser(ctx workflow.Context, input SolveCaptchaWorkflowInput, reason string) (map[string]interface{}, error) {
	workflow.GetLogger(ctx).Info("Falling back to manual captcha solving", "reason", reason)
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{})
	var result map[string]interface{}
	err := workflow.ExecuteChildWorkflow(childCtx, "HandleUserActionWorkflow", handleuseraction.HandleUserActionWorkflowInput{
		WorkflowID:       input.WorkflowID,
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
		UserAction:       shared.UserActionCaptcha,
		ActionDetails:    "Please solve the CAPTCHA on the page to continue your application.",
	}).Get(ctx, &result)
	if err != nil {
		return nil, fmt.Errorf("handle user action fallback: %w", err)
	}
	return solvedResult(true, "captcha handled by user"), nil
}

func solvedResult(solved bool, message string) map[string]interface{} {
	return map[string]interface{}{
		"solved":  solved,
		"message": message,
	}
}

func getBase64Screenshot(screenshotPath string) (string, error) {
	data, err := os.ReadFile(screenshotPath)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(data)), nil
}
