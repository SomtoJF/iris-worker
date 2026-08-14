package coverletter

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
	"time"

	sqldb "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/helper"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type TemplateSet struct {
	System          *template.Template
	User            *template.Template
	EditSystem      *template.Template
	EditUser        *template.Template
	LLMFilterSystem *template.Template
	LLMFilterUser   *template.Template
}

var Templates TemplateSet

type CoverLetterWorkflowInput struct {
	IdJobApplication uint              `json:"id_job_application"`
	IdUser           uint              `json:"id_user"`
	WorkflowID       *string           `json:"workflow_id,omitempty"`
	ElementIndex     *int              `json:"element_index,omitempty"`
	EditInstructions *EditInstructions `json:"edit_instructions,omitempty"`
	// UltraWrite only applies when EditInstructions is set: true runs the full
	// analysis write instead of the lightweight edit. Ignored when generating
	// from scratch (no EditInstructions), where the full analysis always runs.
	UltraWrite bool `json:"ultra_write,omitempty"`
}

type EditInstructions struct {
	Instructions string `json:"instructions"`
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

type editCoverLetterPromptData struct {
	CompanyName        string `json:"company_name"`
	JobTitle           string `json:"job_title"`
	JobDescription     string `json:"job_description"`
	CandidateResume    string `json:"candidate_resume"`
	CurrentCoverLetter string `json:"current_cover_letter"`
	EditInstructions   string `json:"edit_instructions"`
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
	Opener               string               `json:"opener"`
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
	Templates.EditSystem, err = helper.LoadTemplate("workflow/coverletter/prompt/edit/system.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.EditUser, err = helper.LoadTemplate("workflow/coverletter/prompt/edit/user.go.tmpl")
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

	isEditMode := input.EditInstructions != nil
	// UltraWrite only matters in edit mode. Full analysis runs when generating
	// from scratch, or when editing with UltraWrite explicitly requested.
	isFullAnalysis := !isEditMode || input.UltraWrite

	// fetch required data (preloads JobApplicationData only when editing)
	data, err := fetchCoverLetterData(ctx, input, isEditMode)
	if err != nil {
		return nil, err
	}

	out, err := generateCoverLetterForInput(ctx, input, data, isFullAnalysis)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"cover_letter": out.CoverLetter,
		"word_count":   len(strings.Fields(out.CoverLetter)),
	}, nil
}

// generateCoverLetterForInput routes between the full-analysis write and the
// lightweight edit based on isFullAnalysis. Web research runs only for the
// full-analysis path.
func generateCoverLetterForInput(ctx workflow.Context, input CoverLetterWorkflowInput, data coverLetterFetchedData, isFullAnalysis bool) (coverLetterLLMResponse, error) {
	if isFullAnalysis {
		companyPages := gatherCompanyInfo(ctx,
			strings.TrimSpace(data.JobApplication.CompanyName),
			strings.TrimSpace(data.JobApplication.JobDescription),
			input.IdUser, input.IdJobApplication)
		return generateCoverLetterFromData(ctx, input, data, companyPages)
	}

	if data.CurrentCoverLetter == nil || strings.TrimSpace(*data.CurrentCoverLetter) == "" {
		return coverLetterLLMResponse{}, fmt.Errorf("cannot edit: no existing cover letter for job application %d", input.IdJobApplication)
	}

	return generateEditedCoverLetter(ctx, input, data)
}

func generateEditedCoverLetter(ctx workflow.Context, input CoverLetterWorkflowInput, data coverLetterFetchedData) (coverLetterLLMResponse, error) {
	promptData := editCoverLetterPromptData{
		CompanyName:        strings.TrimSpace(data.JobApplication.CompanyName),
		JobTitle:           strings.TrimSpace(data.JobApplication.JobTitle),
		JobDescription:     strings.TrimSpace(data.JobApplication.JobDescription),
		CandidateResume:    strings.TrimSpace(data.Resume.Content),
		CurrentCoverLetter: strings.TrimSpace(*data.CurrentCoverLetter),
		EditInstructions:   strings.TrimSpace(input.EditInstructions.Instructions),
	}

	systemPrompt, err := executeTemplateToString(Templates.EditSystem, promptData)
	if err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("render edit system prompt: %w", err)
	}
	userPrompt, err := executeTemplateToString(Templates.EditUser, promptData)
	if err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("render edit user prompt: %w", err)
	}

	return editCoverLetter(ctx, systemPrompt, userPrompt, input.IdUser, input.IdJobApplication)
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

func executeTemplateToString(t *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}
