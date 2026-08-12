package jobapplication

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"
	"time"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type AutofillQuestion struct {
	Id       uint   `json:"id"`
	Question string `json:"question"`
}

type AutofillAnsweredQuestion struct {
	Id       uint   `json:"id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type AutofillApplicationWorkflowInput struct {
	Url              string             `json:"url"`
	IdUser           uint               `json:"id_user"`
	IdJobApplication uint               `json:"id_job_application"`
	Questions        []AutofillQuestion `json:"questions"`
}

type AutofillApplicationWorkflowResponse struct {
	Questions []AutofillAnsweredQuestion `json:"questions"`
}

type autofillPromptData struct {
	CurrentDate                string
	JobPostingUrl              string
	JobTitle                   string
	CompanyName                string
	JobDescription             string
	UserProfileJSON            string
	UserResume                 string
	ExistingApplicationAnswers string
	QuestionsJSON              string
}

type autofillLLMResponse struct {
	Questions []AutofillAnsweredQuestion `json:"questions"`
}

func AutofillApplicationWorkflow(ctx workflow.Context, input AutofillApplicationWorkflowInput) (AutofillApplicationWorkflowResponse, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("AutofillApplicationWorkflow started", "url", input.Url, "id_job_application", input.IdJobApplication)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})

	if len(input.Questions) == 0 {
		return AutofillApplicationWorkflowResponse{Questions: []AutofillAnsweredQuestion{}}, nil
	}

	var jobApp sqldb.JobApplication
	if err := workflow.ExecuteActivity(ctx, "GetJobApplication", sqldb.GetJobApplicationInput{
		IdJobApplication:          input.IdJobApplication,
		IncludeJobApplicationData: true,
	}).Get(ctx, &jobApp); err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("get job application: %w", err)
	}

	var resume sqldb.Resume
	if err := workflow.ExecuteActivity(ctx, "GetResumeByID", jobApp.ResumeId).Get(ctx, &resume); err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("get resume: %w", err)
	}

	userProfile, err := fetchJobApplicationProfile(ctx, input.IdUser)
	if err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("fetch job application profile: %w", err)
	}

	userProfileJSON, err := json.Marshal(userProfile)
	if err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("marshal user profile: %w", err)
	}

	existingAnswersJSON := "[]"
	if jobApp.JobApplicationData != nil && len(jobApp.JobApplicationData.Questions) > 0 {
		b, err := json.Marshal(jobApp.JobApplicationData.Questions)
		if err != nil {
			return AutofillApplicationWorkflowResponse{}, fmt.Errorf("marshal existing answers: %w", err)
		}
		existingAnswersJSON = string(b)
	}

	questionsJSON, err := json.Marshal(input.Questions)
	if err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("marshal questions: %w", err)
	}

	jobUrl := input.Url
	if jobUrl == "" {
		jobUrl = jobApp.Url
	}

	promptData := autofillPromptData{
		CurrentDate:                workflow.Now(ctx).Format("2006-01-02"),
		JobPostingUrl:              jobUrl,
		JobTitle:                   jobApp.JobTitle,
		CompanyName:                jobApp.CompanyName,
		JobDescription:             jobApp.JobDescription,
		UserProfileJSON:            string(userProfileJSON),
		UserResume:                 resume.Content,
		ExistingApplicationAnswers: existingAnswersJSON,
		QuestionsJSON:              string(questionsJSON),
	}

	systemPrompt, err := executeAutofillTemplate(Templates.Autofill.System, promptData)
	if err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("render autofill system prompt: %w", err)
	}
	userPrompt, err := executeAutofillTemplate(Templates.Autofill.User, promptData)
	if err != nil {
		return AutofillApplicationWorkflowResponse{}, fmt.Errorf("render autofill user prompt: %w", err)
	}

	answered, err := callAutofillLLM(ctx, systemPrompt, userPrompt, input.IdUser, input.IdJobApplication)
	if err != nil {
		return AutofillApplicationWorkflowResponse{}, err
	}

	return AutofillApplicationWorkflowResponse{
		Questions: mergeAutofillAnswers(input.Questions, answered),
	}, nil
}

func executeAutofillTemplate(tmpl *template.Template, data autofillPromptData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func callAutofillLLM(ctx workflow.Context, systemPrompt, userPrompt string, idUser, idJobApplication uint) ([]AutofillAnsweredQuestion, error) {
	temperature := 0.2
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            "x-ai/grok-4.3",
		ResponseSchema:   getAutofillResponseSchema(),
		Temperature:      &temperature,
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return nil, fmt.Errorf("call LLM: %w", err)
	}

	var parsed autofillLLMResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal autofill LLM response: %w", err)
	}
	return parsed.Questions, nil
}

func mergeAutofillAnswers(input []AutofillQuestion, answered []AutofillAnsweredQuestion) []AutofillAnsweredQuestion {
	byID := make(map[uint]AutofillAnsweredQuestion, len(answered))
	for _, q := range answered {
		byID[q.Id] = q
	}

	out := make([]AutofillAnsweredQuestion, 0, len(input))
	for _, q := range input {
		if a, ok := byID[q.Id]; ok {
			out = append(out, AutofillAnsweredQuestion{
				Id:       q.Id,
				Question: q.Question,
				Answer:   a.Answer,
			})
			continue
		}
		out = append(out, AutofillAnsweredQuestion{
			Id:       q.Id,
			Question: q.Question,
			Answer:   "",
		})
	}
	return out
}

func getAutofillResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"questions": map[string]interface{}{
				"type":        "array",
				"description": "Answered questions. Include every input question exactly once with its original id.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id": map[string]interface{}{
							"type":        "integer",
							"description": "The original question id from the input; must be echoed unchanged",
						},
						"question": map[string]interface{}{
							"type":        "string",
							"description": "The original question text",
						},
						"answer": map[string]interface{}{
							"type":        "string",
							"description": "The answer; empty string when the question cannot be answered truthfully from known data",
						},
					},
					"required": []string{"id", "question", "answer"},
				},
			},
		},
		"required": []string{"questions"},
	}
}
