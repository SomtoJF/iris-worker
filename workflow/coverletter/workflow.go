package coverletter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	sqldb "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
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
	IdJobApplication uint   `json:"id_job_application"`
	IdUser           uint   `json:"id_user"`
	WorkflowID       string `json:"workflow_id"`
	ElementIndex     int    `json:"element_index"`
}

type coverLetterPromptData struct {
	IdJobApplication uint                    `json:"id_job_application"`
	IdUser           uint                    `json:"id_user"`
	CompanyName      string                  `json:"company_name"`
	JobTitle         string                  `json:"job_title"`
	JobDescription   string                  `json:"job_description"`
	CandidateResume  string                  `json:"candidate_resume"`
	CompanyPages     []sqldb.WebsiteCachePage `json:"company_pages"`
}

type coverLetterLLMResponse struct {
	CoverLetter string `json:"cover_letter"`
}

func SetTemplates() {
	var err error
	Templates.System, err = helper.LoadTemplate("workflow/coverletter/prompt/system.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.User, err = helper.LoadTemplate("workflow/coverletter/prompt/user.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.LLMFilterSystem, err = helper.LoadTemplate("workflow/jobapplication/prompt/llmfilter/system.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.LLMFilterUser, err = helper.LoadTemplate("workflow/jobapplication/prompt/llmfilter/user.go.tmpl")
	if err != nil {
		panic(err)
	}
}

func CoverLetterWorkflow(ctx workflow.Context, input CoverLetterWorkflowInput) (map[string]interface{}, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("CoverLetterWorkflow started", "input", input)

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    15 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	var resume sqldb.Resume
	if err := workflow.ExecuteActivity(ctx, "FetchActiveUserResume", input.IdUser).Get(ctx, &resume); err != nil {
		return nil, fmt.Errorf("fetch active user resume: %w", err)
	}

	var jobApplication sqldb.JobApplication
	if err := workflow.ExecuteActivity(ctx, "GetJobApplication", input.IdJobApplication).Get(ctx, &jobApplication); err != nil {
		return nil, fmt.Errorf("get job application: %w", err)
	}

	companyPages := gatherCompanyInfo(ctx,
		strings.TrimSpace(jobApplication.CompanyName),
		strings.TrimSpace(jobApplication.JobDescription),
		input.IdUser, input.IdJobApplication)

	promptData := coverLetterPromptData{
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
		CompanyName:      strings.TrimSpace(jobApplication.CompanyName),
		JobTitle:         strings.TrimSpace(jobApplication.JobTitle),
		JobDescription:   strings.TrimSpace(jobApplication.JobDescription),
		CandidateResume:  strings.TrimSpace(resume.Content),
		CompanyPages:     companyPages,
	}

	systemPrompt, err := executeTemplateToString(Templates.System, promptData)
	if err != nil {
		return nil, fmt.Errorf("render system prompt: %w", err)
	}
	userPrompt, err := executeTemplateToString(Templates.User, promptData)
	if err != nil {
		return nil, fmt.Errorf("render user prompt: %w", err)
	}

	out, err := generateCoverLetter(ctx, systemPrompt, userPrompt, input.IdUser, input.IdJobApplication)
	if err != nil {
		return nil, err
	}

	if err := workflow.ExecuteActivity(ctx, "Type", browser.TypeInput{
		WorkflowID:   input.WorkflowID,
		ElementIndex: input.ElementIndex,
		Text:         out.CoverLetter,
	}).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("type cover letter: %w", err)
	}

	wordCount := len(strings.Fields(out.CoverLetter))
	return map[string]interface{}{
		"cover_letter": out.CoverLetter,
		"word_count":   wordCount,
	}, nil
}

func executeTemplateToString(t *template.Template, data any) (string, error) {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func generateCoverLetter(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication uint) (coverLetterLLMResponse, error) {
	resp, err := callCoverLetterLLM(ctx, systemPrompt, userPrompt, idUser, idJobApplication)
	if err != nil {
		return coverLetterLLMResponse{}, err
	}

	if isValidCoverLetter(resp.CoverLetter) {
		resp.CoverLetter = normalizeParagraphs(resp.CoverLetter)
		return resp, nil
	}

	repairUserPrompt := fmt.Sprintf(
		"%s\n\n<repair_request>\nThe previous output did not meet the requirements.\nRewrite it so it is exactly 3 paragraphs (separated by one blank line) and no more than 450 words.\nReturn ONLY valid JSON matching the schema.\n</repair_request>\n\n<previous_output>\n%s\n</previous_output>\n",
		userPrompt,
		resp.CoverLetter,
	)

	resp2, err := callCoverLetterLLM(ctx, systemPrompt, repairUserPrompt, idUser, idJobApplication)
	if err != nil {
		return coverLetterLLMResponse{}, err
	}
	if !isValidCoverLetter(resp2.CoverLetter) {
		return coverLetterLLMResponse{}, fmt.Errorf("cover letter failed validation after retry")
	}
	resp2.CoverLetter = normalizeParagraphs(resp2.CoverLetter)
	return resp2, nil
}

func callCoverLetterLLM(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication uint) (coverLetterLLMResponse, error) {
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            "x-ai/grok-4.1-fast",
		ResponseSchema:   getCoverLetterResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("CallLLM: %w", err)
	}

	var out coverLetterLLMResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &out); err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("unmarshal cover letter response: %w", err)
	}
	out.CoverLetter = strings.TrimSpace(out.CoverLetter)
	return out, nil
}

func getCoverLetterResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"cover_letter": map[string]interface{}{
				"type":        "string",
				"description": "The complete cover letter text, exactly 3 paragraphs, separated by one blank line.",
			},
		},
		"required": []string{"cover_letter"},
	}
}

func normalizeParagraphs(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	parts := splitParagraphs(s)
	if len(parts) != 3 {
		return s
	}
	return strings.Join(parts, "\n\n")
}

func isValidCoverLetter(s string) bool {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return false
	}
	if len(strings.Fields(s)) > 450 {
		return false
	}
	paragraphs := splitParagraphs(s)
	return len(paragraphs) == 3
}

func splitParagraphs(s string) []string {
	// Split on one-or-more blank lines.
	raw := strings.Split(s, "\n")
	var paragraphs []string
	var cur []string

	flush := func() {
		if len(cur) == 0 {
			return
		}
		p := strings.TrimSpace(strings.Join(cur, "\n"))
		if p != "" {
			paragraphs = append(paragraphs, p)
		}
		cur = nil
	}

	for _, line := range raw {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()
	return paragraphs
}
