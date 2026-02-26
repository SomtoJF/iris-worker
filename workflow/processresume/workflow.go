package processresume

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type ProcessResumeWorkflowInput struct {
	IdUser        uint   `json:"id_user"`
	IdResume      uint   `json:"id_resume"`
	ResumeContent string `json:"resume_content"`
}

func ProcessResumeWorkflow(ctx workflow.Context, input ProcessResumeWorkflowInput) error {
	logger := workflow.GetLogger(ctx)

	logger.Info("ProcessResumeWorkflow started", "input", input)

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	user, err := getUser(ctx, input.IdUser)
	if err != nil {
		logger.Error("Failed to get user", "error", err)
		return err
	}

	resumeContent, err := processResumeContent(ctx, input.ResumeContent)
	if err != nil {
		logger.Error("Failed to process resume content", "error", err)
		return err
	}

	if err := updateJobApplicationProfile(ctx, user, resumeContent); err != nil {
		logger.Error("Failed to update job application profile", "error", err)
		return err
	}

	return nil
}

type ResumeContent struct {
	FirstName          string `json:"first_name"`
	LastName           string `json:"last_name"`
	Email              string `json:"email"`
	Phone              string `json:"phone"`
	Address            string `json:"address"`
	City               string `json:"city"`
	State              string `json:"state"`
	CountryOfResidence string `json:"country_of_residence"`
}

func getUser(ctx workflow.Context, idUser uint) (sqldb.User, error) {
	input := sqldb.GetUserInput{
		IdUser:                       idUser,
		IncludeJobApplicationProfile: true,
	}
	var user sqldb.User
	if err := workflow.ExecuteActivity(ctx, "GetUser", input).Get(ctx, &user); err != nil {
		return sqldb.User{}, err
	}
	return user, nil
}

func processResumeContent(ctx workflow.Context, resumeContent string) (ResumeContent, error) {
	systemPrompt := "Extract structured resume fields from the given resume text. Return only valid JSON with the requested fields. Use empty string for any field not found."
	userPrompt := fmt.Sprintf("Resume text:\n\n%s", resumeContent)

	llmRequest := types.AIPIRequest{
		SystemMessage:  systemPrompt,
		UserMessage:    userPrompt,
		Model:          "x-ai/grok-4.1-fast",
		ResponseSchema: getResumeContentResponseSchema(),
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return ResumeContent{}, err
	}

	var out ResumeContent
	if err := json.Unmarshal([]byte(llmResponse.Content), &out); err != nil {
		return ResumeContent{}, fmt.Errorf("unmarshal resume content: %w", err)
	}
	return out, nil
}

func getResumeContentResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"first_name":           map[string]interface{}{"type": "string", "description": "Candidate first name"},
			"last_name":            map[string]interface{}{"type": "string", "description": "Candidate last name"},
			"email":                map[string]interface{}{"type": "string", "description": "Email address"},
			"phone":                map[string]interface{}{"type": "string", "description": "Phone number"},
			"address":              map[string]interface{}{"type": "string", "description": "Street address"},
			"city":                 map[string]interface{}{"type": "string", "description": "City"},
			"state":                map[string]interface{}{"type": "string", "description": "State or region"},
			"country_of_residence": map[string]interface{}{"type": "string", "description": "Country of residence"},
		},
		"required": []string{"first_name", "last_name", "email", "phone", "address", "city", "state", "country_of_residence"},
	}
}

func updateJobApplicationProfile(ctx workflow.Context, user sqldb.User, resumeContent ResumeContent) error {
	if user.JobApplicationProfile.IdJobApplicationProfile == 0 {
		return fmt.Errorf("user has no job application profile")
	}
	data := make(map[string]interface{})
	if resumeContent.FirstName != "" {
		data["first_name"] = resumeContent.FirstName
	}
	if resumeContent.LastName != "" {
		data["last_name"] = resumeContent.LastName
	}
	if resumeContent.Email != "" {
		data["email"] = resumeContent.Email
	}
	if resumeContent.Phone != "" {
		data["phone"] = resumeContent.Phone
	}
	if resumeContent.Address != "" {
		data["address"] = resumeContent.Address
	}
	if resumeContent.City != "" {
		data["city"] = resumeContent.City
	}
	if resumeContent.State != "" {
		data["state"] = resumeContent.State
	}
	if resumeContent.CountryOfResidence != "" {
		data["country_of_residence"] = resumeContent.CountryOfResidence
	}
	if len(data) == 0 {
		return nil
	}
	input := sqldb.UpdateJobApplicationProfileInput{
		IdJobApplicationProfile: user.JobApplicationProfile.IdJobApplicationProfile,
		Data:                    data,
	}
	return workflow.ExecuteActivity(ctx, "UpdateJobApplicationProfile", input).Get(ctx, nil)
}
