package coverletter

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	sqldb "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/helper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type TemplateSet struct {
	System          *template.Template
	User            *template.Template
	LLMFilterSystem *template.Template
	LLMFilterUser   *template.Template
}

var Templates TemplateSet

type CoverLetterWorkflowInput struct {
	IdJobApplication uint                       `json:"id_job_application"`
	IdUser           uint                       `json:"id_user"`
	WorkflowID       *string                    `json:"workflow_id,omitempty"`
	ElementIndex     *int                       `json:"element_index,omitempty"`
	StandaloneArgs   *StandaloneCoverLetterArgs `json:"standalone_args,omitempty"`
}

type StandaloneCoverLetterArgs struct {
	JobDescription string `json:"job_description"`
	CompanyName    string `json:"company_name"`
	JobTitle       string `json:"job_title"`
}

type coverLetterPromptData struct {
	IdJobApplication uint                     `json:"id_job_application"`
	IdUser           uint                     `json:"id_user"`
	CompanyName      string                   `json:"company_name"`
	JobTitle         string                   `json:"job_title"`
	JobDescription   string                   `json:"job_description"`
	CandidateResume  string                   `json:"candidate_resume"`
	CompanyPages     []sqldb.WebsiteCachePage `json:"company_pages"`
}

type taskPriorities struct {
	MostImportant []string `json:"most_important"`
	LessImportant []string `json:"less_important"`
	Negotiable    []string `json:"negotiable"`
}

type qualificationMatch struct {
	Requirement   string `json:"requirement"`
	Qualification string `json:"qualification"`
	StoryTheme    string `json:"story_theme"`
	Connection    string `json:"connection"`
}

type coverLetterLLMResponse struct {
	TasksOrSkills        taskPriorities       `json:"tasks_or_skills"`
	QualificationMatches []qualificationMatch `json:"qualification_matches"`
	CompanyReasons       []string             `json:"company_reasons"`
	SummaryStatement     string               `json:"summary_statement"`
	CoverLetter          string               `json:"cover_letter"`
}

func SetTemplates() {
	var err error
	Templates.System, err = helper.LoadTemplate("workflow/coverletter/prompt/coverletter/system.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.User, err = helper.LoadTemplate("workflow/coverletter/prompt/coverletter/user.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.LLMFilterSystem, err = helper.LoadTemplate("workflow/coverletter/prompt/llmfilter/system.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.LLMFilterUser, err = helper.LoadTemplate("workflow/coverletter/prompt/llmfilter/user.go.tmpl")
	if err != nil {
		panic(err)
	}
}

func CoverLetterWorkflow(ctx workflow.Context, input CoverLetterWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("CoverLetterWorkflow started", "input", input)

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    15 * time.Second,
			MaximumAttempts:    3,
		},
	})

	// fetch required data
	data, err := fetchCoverLetterData(ctx, input)
	if err != nil {
		return nil, err
	}

	// Search the web for company information
	companyPages := gatherCompanyInfo(ctx,
		strings.TrimSpace(data.JobApplication.CompanyName),
		strings.TrimSpace(data.JobApplication.JobDescription),
		input.IdUser, input.IdJobApplication)

	// Generate the cover letter
	out, err := generateCoverLetterFromData(ctx, input, data, companyPages)
	if err != nil {
		return nil, err
	}

	// Type the cover letter
	if input.WorkflowID == nil || input.ElementIndex == nil {
		if err := typeCoverLetter(ctx, input, out.CoverLetter); err != nil {
			return nil, err
		}
	}

	return map[string]interface{}{
		"cover_letter": out.CoverLetter,
		"word_count":   len(strings.Fields(out.CoverLetter)),
	}, nil
}

func generateCoverLetterFromData(ctx workflow.Context, input CoverLetterWorkflowInput, data coverLetterFetchedData, companyPages []sqldb.WebsiteCachePage) (coverLetterLLMResponse, error) {
	promptData := coverLetterPromptData{
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
		CompanyName:      strings.TrimSpace(data.JobApplication.CompanyName),
		JobTitle:         strings.TrimSpace(data.JobApplication.JobTitle),
		JobDescription:   strings.TrimSpace(data.JobApplication.JobDescription),
		CandidateResume:  strings.TrimSpace(data.Resume.Content),
		CompanyPages:     companyPages,
	}

	systemPrompt, err := executeTemplateToString(Templates.System, promptData)
	if err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("render system prompt: %w", err)
	}
	userPrompt, err := executeTemplateToString(Templates.User, promptData)
	if err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("render user prompt: %w", err)
	}

	return generateCoverLetter(ctx, systemPrompt, userPrompt, input.IdUser, input.IdJobApplication)
}

func typeCoverLetter(ctx workflow.Context, input CoverLetterWorkflowInput, coverLetter string) error {
	if err := workflow.ExecuteActivity(ctx, "Type", browser.TypeInput{
		WorkflowID:   *input.WorkflowID,
		ElementIndex: *input.ElementIndex,
		Text:         coverLetter,
	}).Get(ctx, nil); err != nil {
		return fmt.Errorf("type cover letter: %w", err)
	}
	return nil
}

func executeTemplateToString(t *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
