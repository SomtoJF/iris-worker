package jobapplication

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type InitiateApplicationWorkflowInput struct {
	Url              string `json:"url"`
	IdUser           uint   `json:"id_user"`
	IdJobApplication uint   `json:"id_job_application"`
}

type InitiateApplicationWorkflowResponse struct {
	JobTitle       string `json:"jobTitle"`
	CompanyName    string `json:"companyName"`
	JobDescription string `json:"jobDescription"`
}

func InitiateApplicationWorkflow(ctx workflow.Context, input InitiateApplicationWorkflowInput) (InitiateApplicationWorkflowResponse, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("InitiateApplicationWorkflow started", "url", input.Url, "id_job_application", input.IdJobApplication)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})

	pageText, err := scrapeWebPageTextOnly(ctx, input.Url, input.IdUser, input.IdJobApplication)
	if err != nil {
		logger.Error("Failed to scrape webpage", "error", err)
		return InitiateApplicationWorkflowResponse{}, err
	}

	jobDetails, err := extractJobDetailsFromText(ctx, pageText, input.IdUser, input.IdJobApplication)
	if err != nil {
		logger.Error("Failed to extract job details", "error", err)
		return InitiateApplicationWorkflowResponse{}, err
	}

	if !jobDetails.IsValidJobPosting {
		return InitiateApplicationWorkflowResponse{}, temporal.NewNonRetryableApplicationError(
			"invalid job posting",
			"InvalidJobPosting",
			nil,
		)
	}

	return InitiateApplicationWorkflowResponse{
		JobTitle:       jobDetails.JobTitle,
		CompanyName:    jobDetails.CompanyName,
		JobDescription: jobDetails.JobDescription,
	}, nil
}

func scrapeWebPageTextOnly(ctx workflow.Context, url string, idUser uint, idJobApplication uint) (string, error) {
	var scrapeOutput map[string]interface{}
	err := workflow.ExecuteActivity(ctx, "ScrapeWebPage", map[string]interface{}{
		"url":                url,
		"advanced":           true,
		"id_user":            idUser,
		"id_job_application": idJobApplication,
	}).Get(ctx, &scrapeOutput)
	if err != nil {
		return "", err
	}
	pageText, ok := scrapeOutput["data"].(string)
	if !ok || strings.TrimSpace(pageText) == "" {
		return "", fmt.Errorf("ScrapeWebPage returned empty data")
	}
	return pageText, nil
}
