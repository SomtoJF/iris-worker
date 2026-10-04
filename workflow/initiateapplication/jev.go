package initiateapplication

import (
	"fmt"
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

	var result types.JevResponse
	if err := workflow.ExecuteActivity(jevCtx, "CallJev", types.JevRequest{
		State: map[string]string{
			"webpage_text": pageText,
		},
		Questions: map[string]types.JevQuestion{
			"is_valid_job_posting": {
				Type:         "noul",
				Instructions: "Is this webpage a single real job posting, rather than a search page, login page, error page, or company careers index?",
				Criteria: map[string]string{
					"true":  "The page represents one specific real job opening.",
					"false": "The page is a search page, login page, error page, general careers index, or not a job posting.",
				},
			},
			"has_job_description": {
				Type:         "noul",
				Instructions: "Does the page contain a substantive description of the role, responsibilities, or qualifications?",
				Criteria: map[string]string{
					"true":  "The page contains meaningful role-specific responsibilities or qualifications.",
					"false": "The page has no substantive role description, responsibilities, or qualifications.",
				},
			},
		},
		IdUser:           userID,
		IdJobApplication: &applicationID,
	}).Get(ctx, &result); err != nil {
		return fmt.Errorf("JEV job posting gate: %w", err)
	}
	validPosting, err := jevNoulDecisionIsYes(result.Answers, "is_valid_job_posting", 0.5)
	if err != nil {
		return fmt.Errorf("parse JEV job posting decision: %w", err)
	}
	hasDescription, err := jevNoulDecisionIsYes(result.Answers, "has_job_description", 0.5)
	if err != nil {
		return fmt.Errorf("parse JEV job description decision: %w", err)
	}
	if !validPosting || !hasDescription {
		return temporal.NewNonRetryableApplicationError("invalid job posting", "InvalidJobPosting", nil)
	}
	return nil
}

func jevNoulDecisionIsYes(answers map[string]types.JevAnswer, name string, threshold float64) (bool, error) {
	answer, ok := answers[name]
	if !ok || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
		return false, fmt.Errorf("missing or invalid Noul answer %q", name)
	}
	return *answer.Noul >= threshold, nil
}
