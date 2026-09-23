package initiateapplication

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func gateJobPostingWithJev(ctx workflow.Context, pageText string, userID, applicationID uint) error {
	jevCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    2,
		},
	})

	var result types.AIPIResponse
	if err := workflow.ExecuteActivity(jevCtx, "CallJev", types.JevRequest{
		UserMessage:      pageText,
		SystemMessage:    "Evaluate the supplied webpage text. Return true for is_valid_job_posting only when this is a single real job posting, not a search page, login page, error page, or company careers index. Return true for has_job_description only when the page contains a substantive role description or responsibilities.",
		ResponseSchema:   jobPostingGateSchema(),
		IdUser:           userID,
		IdJobApplication: &applicationID,
	}).Get(ctx, &result); err != nil {
		return fmt.Errorf("Jev job posting gate: %w", err)
	}
	var gate struct {
		IsValidJobPosting bool `json:"is_valid_job_posting"`
		HasJobDescription bool `json:"has_job_description"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Content)), &gate); err != nil {
		return fmt.Errorf("parse Jev job posting gate: %w", err)
	}
	if !gate.IsValidJobPosting || !gate.HasJobDescription {
		return temporal.NewNonRetryableApplicationError("invalid job posting", "InvalidJobPosting", nil)
	}
	return nil
}

func jobPostingGateSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"is_valid_job_posting": map[string]any{"type": "boolean"},
			"has_job_description":  map[string]any{"type": "boolean"},
		},
		"required": []string{"is_valid_job_posting", "has_job_description"},
	}
}
