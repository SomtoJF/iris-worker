package jobapplication

import (
	"encoding/json"
	"fmt"

	browseractivity "github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/captcha"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// maxCaptchaVerifyAttempts bounds the inject → vision → click loop so a captcha
// that won't clear can't spin forever.
const maxCaptchaVerifyAttempts = 2

// maybeSolveCaptcha detects a captcha on the current page and, if present, solves
// it in-process on sessionCtx so browser activities stay sticky to the worker that
// owns activeSessions. CapSolver/LLM also run on sessionCtx (same worker is fine;
// they don't touch the page map). Returns true when a captcha was present.
// Users cannot solve captchas — failures return an error, never HandleUserAction.
func maybeSolveCaptcha(sessionCtx workflow.Context, workflowID string, userID uint, idJobApplication uint) (bool, error) {
	detected, err := detectCaptcha(sessionCtx, workflowID)
	if err != nil {
		return false, err
	}
	if detected.Type == browseractivity.CaptchaTypeNone {
		return false, nil
	}

	logger := workflow.GetLogger(sessionCtx)
	logger.Info("Captcha detected", "type", detected.Type, "site_key", detected.SiteKey)

	if detected.SiteKey == "" {
		return true, temporal.NewNonRetryableApplicationError(
			"captcha detected but sitekey could not be extracted",
			"CaptchaSolveFailed",
			nil,
		)
	}

	solved, err := solveAndInjectCaptcha(sessionCtx, workflowID, userID, idJobApplication, detected)
	if err != nil {
		return true, fmt.Errorf("solve captcha: %w", err)
	}
	if !solved {
		return true, temporal.NewNonRetryableApplicationError(
			"automated captcha solving did not clear the challenge",
			"CaptchaSolveFailed",
			nil,
		)
	}

	return true, nil
}

func solveAndInjectCaptcha(
	sessionCtx workflow.Context,
	workflowID string,
	userID uint,
	idJobApplication uint,
	detected browseractivity.DetectCaptchaOutput,
) (bool, error) {
	token, err := solveWithCapSolver(sessionCtx, detected)
	if err != nil {
		return false, err
	}

	if _, err := injectCaptchaToken(sessionCtx, workflowID, detected.Type, token); err != nil {
		return false, fmt.Errorf("inject token: %w", err)
	}

	for attempt := 0; attempt < maxCaptchaVerifyAttempts; attempt++ {
		verdict, err := verifyCaptchaVision(sessionCtx, workflowID, userID, idJobApplication)
		if err != nil {
			return false, fmt.Errorf("vision verify: %w", err)
		}
		if verdict.IsSolved {
			return true, nil
		}
		if !verdict.NeedsButtonClick || verdict.ButtonSelector == "" {
			return false, nil
		}

		clicked, err := clickCaptchaButton(sessionCtx, workflowID, verdict.ButtonSelector)
		if err != nil {
			return false, fmt.Errorf("click captcha button: %w", err)
		}
		if !clicked {
			return false, nil
		}
	}

	post, err := detectCaptcha(sessionCtx, workflowID)
	if err != nil {
		return false, err
	}
	return post.Type == browseractivity.CaptchaTypeNone, nil
}

func detectCaptcha(sessionCtx workflow.Context, workflowID string) (browseractivity.DetectCaptchaOutput, error) {
	var out browseractivity.DetectCaptchaOutput
	err := workflow.ExecuteActivity(sessionCtx, "DetectCaptcha", browseractivity.DetectCaptchaInput{
		WorkflowID: workflowID,
	}).Get(sessionCtx, &out)
	return out, err
}

func solveWithCapSolver(ctx workflow.Context, detected browseractivity.DetectCaptchaOutput) (string, error) {
	var out captcha.SolveWithCapSolverOutput
	err := workflow.ExecuteActivity(ctx, "SolveWithCapSolver", captcha.SolveWithCapSolverInput{
		Type:    detected.Type,
		SiteKey: detected.SiteKey,
		PageURL: detected.PageURL,
		Action:  detected.Action,
	}).Get(ctx, &out)
	return out.Token, err
}

func injectCaptchaToken(sessionCtx workflow.Context, workflowID, captchaType, token string) (browseractivity.InjectCaptchaTokenOutput, error) {
	var out browseractivity.InjectCaptchaTokenOutput
	err := workflow.ExecuteActivity(sessionCtx, "InjectCaptchaToken", browseractivity.InjectCaptchaTokenInput{
		WorkflowID: workflowID,
		Type:       captchaType,
		Token:      token,
	}).Get(sessionCtx, &out)
	return out, err
}

func clickCaptchaButton(sessionCtx workflow.Context, workflowID, selector string) (bool, error) {
	var out browseractivity.ClickCaptchaButtonOutput
	err := workflow.ExecuteActivity(sessionCtx, "ClickCaptchaButton", browseractivity.ClickCaptchaButtonInput{
		WorkflowID: workflowID,
		Selector:   selector,
	}).Get(sessionCtx, &out)
	return out.Clicked, err
}

type captchaVisionVerdict struct {
	IsSolved         bool   `json:"is_solved"`
	NeedsButtonClick bool   `json:"needs_button_click"`
	ButtonSelector   string `json:"button_selector"`
}

func verifyCaptchaVision(sessionCtx workflow.Context, workflowID string, userID uint, idJobApplication uint) (captchaVisionVerdict, error) {
	var screenshot browseractivity.TakeScreenshotOutput
	if err := workflow.ExecuteActivity(sessionCtx, "TakeScreenshot", browseractivity.TakeScreenshotInput{
		WorkflowID: workflowID,
		FileName:   "captcha_verify.png",
	}).Get(sessionCtx, &screenshot); err != nil {
		return captchaVisionVerdict{}, err
	}

	screenshotBase64, err := getBase64Screenshot(sessionCtx, screenshot.Path)
	if err != nil {
		return captchaVisionVerdict{}, err
	}

	systemPrompt := "You verify whether a CAPTCHA on a job application page has been solved. " +
		"A token was just injected programmatically. Look at the screenshot. If the captcha is " +
		"clearly solved (green check, no challenge visible, form ready to submit), set is_solved=true. " +
		"If a button must be clicked to confirm/continue the captcha (e.g. a 'Verify' button or the " +
		"reCAPTCHA checkbox), set needs_button_click=true and provide a specific CSS selector for it in " +
		"button_selector. Do NOT return the application's final submit button. If unsure, set is_solved=false " +
		"and needs_button_click=false."

	var temperature float64 = 0.1
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      "Is the captcha solved, or does a button need clicking? Return the schema.",
		ImageUrl:         &screenshotBase64,
		Model:            "x-ai/grok-4.3",
		ResponseSchema:   captchaVisionResponseSchema(),
		Temperature:      &temperature,
		IdUser:           userID,
		IdJobApplication: &idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(sessionCtx, "CallLLM", llmRequest).Get(sessionCtx, &llmResponse); err != nil {
		return captchaVisionVerdict{}, err
	}

	var verdict captchaVisionVerdict
	if err := json.Unmarshal([]byte(llmResponse.Content), &verdict); err != nil {
		return captchaVisionVerdict{}, err
	}
	return verdict, nil
}

func captchaVisionResponseSchema() map[string]interface{} {
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
