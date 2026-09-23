package jobapplication

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	browserpooltypes "github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type InitiateApplicationWorkflowInput struct {
	Url                   string  `json:"url"`
	IdUser                uint    `json:"id_user"`
	IdJobApplication      uint    `json:"id_job_application"`
	ApplicationExternalId string  `json:"application_external_id"`
	ApplyAutonomously     bool    `json:"apply_autonomously"`
	IdResume              *uint   `json:"id_resume"`
	BrowserPoolWorkflowId *string `json:"browser_pool_workflow_id"`
	ApplicationWorkflowId *string `json:"application_workflow_id"`
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

	if err := updateJobApplication(ctx, input.IdJobApplication, map[string]interface{}{
		"job_title":       jobDetails.JobTitle,
		"company_name":    jobDetails.CompanyName,
		"job_description": jobDetails.JobDescription,
	}); err != nil {
		logger.Error("Failed to update job application", "error", err)
		return InitiateApplicationWorkflowResponse{}, err
	}

	if err := publishApplicationDetailsUpdated(ctx, input, jobDetails); err != nil {
		logger.Error("Failed to publish application details update", "error", err)
	}

	if input.ApplyAutonomously {
		if err := queueAutonomousApplication(ctx, input); err != nil {
			logger.Error("Failed to signal browser pool application completion", "error", err)
		}

		// TODO: Update application status to queued
		// TODO: Publish notification to client for application status change
	}

	return InitiateApplicationWorkflowResponse{
		JobTitle:       jobDetails.JobTitle,
		CompanyName:    jobDetails.CompanyName,
		JobDescription: jobDetails.JobDescription,
	}, nil
}

func queueAutonomousApplication(ctx workflow.Context, input InitiateApplicationWorkflowInput) error {
	if input.IdResume == nil {
		return errors.New("no resume id provided for auto application")
	}

	if input.ApplicationWorkflowId == nil {
		return errors.New("no application workflow id provided for auto application")
	}

	if input.BrowserPoolWorkflowId == nil {
		return errors.New("BrowserPool workflow id not provided for auto application")
	}

	err := workflow.SignalExternalWorkflow(
		ctx,
		*input.BrowserPoolWorkflowId,
		"",
		browserpooltypes.QUEUE_APPLICATION_SIGNAL_NAME,
		browserpooltypes.ApplicationQueueItem{IdJobApplication: input.IdJobApplication, ApplicationWorkflowId: *input.ApplicationWorkflowId, Url: input.Url, IdUser: input.IdUser, IdResume: *input.IdResume},
	).Get(ctx, nil)
	if err != nil {
		return err
	}
	return nil
}

func publishApplicationDetailsUpdated(ctx workflow.Context, input InitiateApplicationWorkflowInput, jobDetails JobDetails) error {
	return workflow.ExecuteActivity(ctx, "PublishRedisEvent", input.IdUser, string(realtimeevent.EventApplicationDetailsUpdated), map[string]interface{}{
		"id":          input.ApplicationExternalId,
		"jobTitle":    jobDetails.JobTitle,
		"companyName": jobDetails.CompanyName,
		"updatedAt":   workflow.Now(ctx).UTC().Format(time.RFC3339),
	}).Get(ctx, nil)
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
