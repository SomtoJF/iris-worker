package initiateapplication

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	browserpooltypes "github.com/SomtoJF/iris-worker/workflow/browserpool/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type InitiateApplicationWorkflowInput struct {
	IdJobApplication      uint    `json:"id_job_application"`
	ApplyAutonomously     bool    `json:"apply_autonomously"`
	BrowserPoolWorkflowID *string `json:"browser_pool_workflow_id"`
}

type InitiateApplicationWorkflowResponse struct {
	JobTitle       string `json:"jobTitle"`
	CompanyName    string `json:"companyName"`
	JobDescription string `json:"jobDescription"`
}

type jobDetails struct {
	JobTitle          string `json:"job_title"`
	CompanyName       string `json:"company_name"`
	JobDescription    string `json:"job_description"`
	IsValidJobPosting bool   `json:"is_valid_job_posting"`
}

func InitiateApplicationWorkflow(ctx workflow.Context, input InitiateApplicationWorkflowInput) (resp InitiateApplicationWorkflowResponse, retErr error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("InitiateApplicationWorkflow started", "id_job_application", input.IdJobApplication)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})

	var application sqldb.JobApplication
	if err := workflow.ExecuteActivity(ctx, "GetJobApplication", sqldb.GetJobApplicationInput{
		IdJobApplication: input.IdJobApplication,
	}).Get(ctx, &application); err != nil {
		return InitiateApplicationWorkflowResponse{}, fmt.Errorf("get job application: %w", err)
	}

	if input.ApplyAutonomously {
		defer func() {
			if retErr == nil {
				return
			}
			cleanupCtx, cancel := workflow.NewDisconnectedContext(ctx)
			defer cancel()
			if err := updateJobApplication(cleanupCtx, application.IdJobApplication, map[string]interface{}{"status": sqldb.JobApplicationStatusFailed}); err != nil {
				logger.Error("Failed to mark application failed", "error", err)
				return
			}
			if err := publishApplicationFailed(cleanupCtx, application.UserId, application.IdExternal.String()); err != nil {
				logger.Error("Failed to publish application failed event", "error", err)
			}
		}()
	}

	pageText, err := scrapeWebPageTextOnly(ctx, application.Url, application.UserId, application.IdJobApplication)
	if err != nil {
		logger.Error("Failed to scrape webpage", "error", err)
		return InitiateApplicationWorkflowResponse{}, err
	}

	if err := gateJobPostingWithJev(ctx, pageText, application.UserId, application.IdJobApplication); err != nil {
		logger.Error("Jev job posting gate failed", "error", err)
		return InitiateApplicationWorkflowResponse{}, err
	}

	details, err := extractJobDetailsFromText(ctx, pageText, application.UserId, application.IdJobApplication)
	if err != nil {
		logger.Error("Failed to extract job details", "error", err)
		return InitiateApplicationWorkflowResponse{}, err
	}
	if !details.IsValidJobPosting {
		return InitiateApplicationWorkflowResponse{}, temporal.NewNonRetryableApplicationError("invalid job posting", "InvalidJobPosting", nil)
	}

	if err := updateJobApplication(ctx, application.IdJobApplication, map[string]interface{}{
		"job_title":       details.JobTitle,
		"company_name":    details.CompanyName,
		"job_description": details.JobDescription,
	}); err != nil {
		return InitiateApplicationWorkflowResponse{}, fmt.Errorf("update job application: %w", err)
	}

	externalID := application.IdExternal.String()
	if err := publishApplicationDetailsUpdated(ctx, application.UserId, externalID, details.JobTitle, details.CompanyName); err != nil {
		logger.Error("Failed to publish application details update", "error", err)
	}

	if input.ApplyAutonomously {
		if err := queueAutonomousApplication(ctx, input, application); err != nil {
			return InitiateApplicationWorkflowResponse{}, err
		}
		if err := updateJobApplication(ctx, application.IdJobApplication, map[string]interface{}{"status": sqldb.JobApplicationStatusQueued}); err != nil {
			return InitiateApplicationWorkflowResponse{}, fmt.Errorf("mark application queued: %w", err)
		}
		if err := publishApplicationQueued(ctx, application.UserId, externalID, details.JobTitle, details.CompanyName); err != nil {
			logger.Error("Failed to publish application queued event", "error", err)
		}
	}

	return InitiateApplicationWorkflowResponse{
		JobTitle:       details.JobTitle,
		CompanyName:    details.CompanyName,
		JobDescription: details.JobDescription,
	}, nil
}

func queueAutonomousApplication(ctx workflow.Context, input InitiateApplicationWorkflowInput, application sqldb.JobApplication) error {
	if input.BrowserPoolWorkflowID == nil {
		return errors.New("browser pool workflow id not provided for auto application")
	}
	if application.ResumeId == 0 {
		return errors.New("no resume id provided for auto application")
	}
	if application.WorkflowID == nil || *application.WorkflowID == "" {
		return errors.New("no application workflow id provided for auto application")
	}

	return workflow.SignalExternalWorkflow(
		ctx,
		*input.BrowserPoolWorkflowID,
		"",
		browserpooltypes.QUEUE_APPLICATION_SIGNAL_NAME,
		browserpooltypes.ApplicationQueueItem{
			IdJobApplication:      application.IdJobApplication,
			Url:                   application.Url,
			IdUser:                application.UserId,
			IdResume:              application.ResumeId,
			ApplicationWorkflowId: *application.WorkflowID,
		},
	).Get(ctx, nil)
}

func updateJobApplication(ctx workflow.Context, id uint, data map[string]interface{}) error {
	return workflow.ExecuteActivity(ctx, "UpdateJobApplication", sqldb.UpdateJobApplicationInput{
		IdJobApplication: id,
		Data:             data,
	}).Get(ctx, nil)
}

func publishApplicationDetailsUpdated(ctx workflow.Context, userID uint, externalID, title, company string) error {
	return publishEvent(ctx, userID, realtimeevent.EventApplicationDetailsUpdated, map[string]interface{}{
		"id":          externalID,
		"jobTitle":    title,
		"companyName": company,
		"updatedAt":   workflow.Now(ctx).UTC().Format(time.RFC3339),
	})
}

func publishApplicationQueued(ctx workflow.Context, userID uint, externalID, title, company string) error {
	return publishEvent(ctx, userID, realtimeevent.EventApplicationDetailsUpdated, map[string]interface{}{
		"id":          externalID,
		"jobTitle":    title,
		"companyName": company,
		"status":      string(sqldb.JobApplicationStatusQueued),
		"updatedAt":   workflow.Now(ctx).UTC().Format(time.RFC3339),
	})
}

func publishApplicationFailed(ctx workflow.Context, userID uint, externalID string) error {
	return publishEvent(ctx, userID, realtimeevent.EventApplicationDetailsUpdated, map[string]interface{}{
		"id":        externalID,
		"status":    string(sqldb.JobApplicationStatusFailed),
		"updatedAt": workflow.Now(ctx).UTC().Format(time.RFC3339),
	})
}

func publishEvent(ctx workflow.Context, userID uint, event realtimeevent.EventType, data map[string]interface{}) error {
	return workflow.ExecuteActivity(ctx, "PublishRedisEvent", userID, string(event), data).Get(ctx, nil)
}

func scrapeWebPageTextOnly(ctx workflow.Context, url string, userID, applicationID uint) (string, error) {
	var output map[string]interface{}
	if err := workflow.ExecuteActivity(ctx, "ScrapeWebPage", map[string]interface{}{
		"url":                url,
		"advanced":           true,
		"id_user":            userID,
		"id_job_application": applicationID,
	}).Get(ctx, &output); err != nil {
		return "", err
	}
	pageText, ok := output["data"].(string)
	if !ok || strings.TrimSpace(pageText) == "" {
		return "", fmt.Errorf("ScrapeWebPage returned empty data")
	}
	return pageText, nil
}
