package captcha

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	browser "github.com/SomtoJF/iris-worker/activity/browser"
)

const capSolverBaseURL = "https://api.capsolver.com"

// Activity holds the CapSolver HTTP client. Page-touching captcha work lives on
// activity/browser.Activity; this package only talks to the CapSolver API.
type Activity struct {
	httpClient *http.Client
}

func NewActivities() *Activity {
	return &Activity{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// SolveWithCapSolver creates a CapSolver task for the detected captcha, polls until
// the token is ready, and returns it. Relies on Temporal RetryPolicy for retries;
// keep the caller's StartToCloseTimeout generous (solves take 10-60s).
func (a *Activity) SolveWithCapSolver(ctx context.Context, input SolveWithCapSolverInput) (SolveWithCapSolverOutput, error) {
	apiKey := os.Getenv("CAPSOLVER_API_KEY")
	if apiKey == "" {
		return SolveWithCapSolverOutput{}, fmt.Errorf("CAPSOLVER_API_KEY env var not set")
	}

	task, err := buildTask(input)
	if err != nil {
		return SolveWithCapSolverOutput{}, err
	}

	taskID, err := a.createTask(ctx, apiKey, task)
	if err != nil {
		return SolveWithCapSolverOutput{}, err
	}

	token, err := a.pollTaskResult(ctx, apiKey, taskID)
	if err != nil {
		return SolveWithCapSolverOutput{}, err
	}

	return SolveWithCapSolverOutput{Token: token}, nil
}

// buildTask maps a detected captcha to a CapSolver task object. All captchas use the
// ProxyLess task variants (CapSolver solves from its own IPs).
func buildTask(input SolveWithCapSolverInput) (map[string]interface{}, error) {
	task := map[string]interface{}{
		"websiteURL": input.PageURL,
		"websiteKey": input.SiteKey,
	}

	switch input.Type {
	case browser.CaptchaTypeRecaptchaV2:
		task["type"] = "ReCaptchaV2TaskProxyLess"
		if input.Invisible {
			task["isInvisible"] = true
		}
	case browser.CaptchaTypeRecaptchaV3:
		task["type"] = "ReCaptchaV3TaskProxyLess"
		action := input.Action
		if action == "" {
			action = "submit"
		}
		task["pageAction"] = action
		task["minScore"] = 0.7
	case browser.CaptchaTypeTurnstile:
		task["type"] = "AntiTurnstileTaskProxyLess"
	case browser.CaptchaTypeHcaptcha:
		// CapSolver's hCaptcha endpoint requires the exact lowercase-"less" spelling
		// ("HCaptchaTaskProxyless"); "HCaptchaTaskProxyLess" is rejected as ERROR_INVALID_TASK_DATA.
		task["type"] = "HCaptchaTaskProxyless"
		if input.Invisible {
			task["isInvisible"] = true
		}
	default:
		return nil, fmt.Errorf("unsupported captcha type: %s", input.Type)
	}

	return task, nil
}

func (a *Activity) createTask(ctx context.Context, apiKey string, task map[string]interface{}) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"clientKey": apiKey,
		"task":      task,
	})
	if err != nil {
		return "", fmt.Errorf("marshal createTask: %w", err)
	}

	var resp struct {
		ErrorID          int    `json:"errorId"`
		ErrorDescription string `json:"errorDescription"`
		TaskID           string `json:"taskId"`
	}
	if err := a.postJSON(ctx, "/createTask", body, &resp); err != nil {
		return "", err
	}
	if resp.ErrorID != 0 {
		return "", fmt.Errorf("capsolver createTask error: %s", resp.ErrorDescription)
	}
	if resp.TaskID == "" {
		return "", fmt.Errorf("capsolver createTask returned empty taskId")
	}
	return resp.TaskID, nil
}

func (a *Activity) pollTaskResult(ctx context.Context, apiKey, taskID string) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"clientKey": apiKey,
		"taskId":    taskID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal getTaskResult: %w", err)
	}

	// Poll up to ~120s; the activity StartToCloseTimeout bounds the hard ceiling.
	deadline := time.Now().Add(120 * time.Second)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		var resp struct {
			ErrorID          int    `json:"errorId"`
			ErrorDescription string `json:"errorDescription"`
			Status           string `json:"status"`
			Solution         struct {
				GRecaptchaResponse string `json:"gRecaptchaResponse"`
				Token              string `json:"token"`
			} `json:"solution"`
		}
		if err := a.postJSON(ctx, "/getTaskResult", body, &resp); err != nil {
			return "", err
		}
		if resp.ErrorID != 0 {
			return "", fmt.Errorf("capsolver getTaskResult error: %s", resp.ErrorDescription)
		}
		if resp.Status == "ready" {
			token := resp.Solution.GRecaptchaResponse
			if token == "" {
				token = resp.Solution.Token
			}
			if token == "" {
				return "", fmt.Errorf("capsolver returned empty token")
			}
			return token, nil
		}

		if time.Now().After(deadline) {
			return "", fmt.Errorf("capsolver solve timed out for task %s", taskID)
		}
		time.Sleep(3 * time.Second)
	}
}

func (a *Activity) postJSON(ctx context.Context, path string, body []byte, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, capSolverBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("capsolver request %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read capsolver response %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("capsolver %s returned status %d: %s", path, resp.StatusCode, string(respBody))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("parse capsolver response %s: %w", path, err)
	}
	return nil
}
