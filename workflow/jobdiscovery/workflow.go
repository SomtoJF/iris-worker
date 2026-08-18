package jobdiscovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/biter777/countries"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type JobDiscoveryWorkflowInput struct {
	IdUser      uint   `json:"id_user"`
	SearchQuery string `json:"search_query"`
	Location    string `json:"location"`
	DateCutoff  string `json:"date_cutoff"`
}

type DiscoveredJob struct {
	Title       string `json:"title"`
	Url         string `json:"url"`
	CompanyName string `json:"company_name"`
	// DatePosted is the date the job was posted in YYYY-MM-DD format
	DatePosted string `json:"date_posted"`
}

type JobDiscoveryWorkflowOutput struct {
	Jobs []DiscoveredJob `json:"jobs"`
}

type mergedSearchHit struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
	Source  string `json:"source_host_hint"`
	Date    string `json:"date"`
}

func JobDiscoveryWorkflow(ctx workflow.Context, input JobDiscoveryWorkflowInput) (JobDiscoveryWorkflowOutput, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("JobDiscoveryWorkflow started", "input", input)

	activityOptions := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, activityOptions)

	jobSources := []string{
		jobSourceGreenhouse,
		jobSourceAshby,
		jobSourceLever,
		jobSourceWellfound,
		jobSourceWorkable,
		jobSourceRemotefront,
	}

	queries := buildSearchQueries(input, jobSources)
	searchOutputs := runBatchWebSearch(ctx, queries, input.Location, input.DateCutoff)
	now := workflow.Now(ctx)
	merged := mergeCleanedResults(jobSources, searchOutputs, now)

	systemPrompt, err := renderJobDiscoverySystemPrompt()
	if err != nil {
		return JobDiscoveryWorkflowOutput{}, fmt.Errorf("render job discovery system prompt: %w", err)
	}

	userPrompt, err := renderJobDiscoveryUserPrompt(UserPromptData{
		Hits:      userPromptHitsFromMerged(merged),
		TodayDate: now.Format("2006-01-02"),
	})
	if err != nil {
		return JobDiscoveryWorkflowOutput{}, fmt.Errorf("render job discovery user prompt: %w", err)
	}

	llmCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 4 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	})

	llmRequest := types.AIPIRequest{
		SystemMessage:  systemPrompt,
		UserMessage:    userPrompt,
		Model:          "deepseek/deepseek-v4-flash",
		ResponseSchema: discoveredJobsResponseSchema(),
		IdUser:         input.IdUser,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(llmCtx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return JobDiscoveryWorkflowOutput{}, err
	}

	jsonPayload := stripLLMJSONFences(llmResponse.Content)
	var parsed struct {
		Jobs []DiscoveredJob `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(jsonPayload), &parsed); err != nil {
		return JobDiscoveryWorkflowOutput{}, fmt.Errorf("unmarshal discovered jobs: %w", err)
	}
	if parsed.Jobs == nil {
		parsed.Jobs = []DiscoveredJob{}
	}
	applyDeterministicDates(parsed.Jobs, merged)

	return JobDiscoveryWorkflowOutput{Jobs: parsed.Jobs}, nil
}

// stripLLMJSONFences removes markdown code fences (e.g. ```json ... ```) so the
// string can be passed to json.Unmarshal. If there are no fences, content is
// returned trimmed unchanged.
func stripLLMJSONFences(content string) string {
	s := strings.TrimSpace(content)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "```"))
	if len(s) >= 4 && strings.EqualFold(s[:4], "json") {
		s = strings.TrimSpace(s[4:])
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// countryNameFromAlpha2 returns the full name of a country from its ISO 3166-1 alpha-2 code.
func countryNameFromAlpha2(location string) string {
	code := strings.ToUpper(strings.TrimSpace(location))
	if code == "" {
		return ""
	}

	c := countries.ByName(code)
	if c == countries.Unknown {
		return ""
	}

	return c.String()
}

func buildSearchQueries(input JobDiscoveryWorkflowInput, jobSources []string) []string {
	q := strings.TrimSpace(input.SearchQuery)
	out := make([]string, 0, len(jobSources))
	for _, host := range jobSources {
		out = append(out, strings.TrimSpace(fmt.Sprintf("site:%s %s", host, q)))
	}
	return out
}

func mergeCleanedResults(jobSources []string, outputs []web.WebSearchOutput, now time.Time) []mergedSearchHit {
	var out []mergedSearchHit
	for i := range jobSources {
		if i >= len(outputs) {
			break
		}
		cleaned := cleanResultsForJobSource(jobSources[i], outputs[i].Organic)
		for _, r := range cleaned {
			out = append(out, mergedSearchHit{
				Title:   r.Title,
				Link:    r.Link,
				Snippet: r.Snippet,
				Source:  jobSources[i],
				Date:    normalizeSerperDate(r.Date, now),
			})
		}
	}
	return out
}

func discoveredJobsResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"jobs": map[string]interface{}{
				"type":        "array",
				"description": "Single job postings only; omit board or multi-job pages.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type":        "string",
							"description": "Job title",
						},
						"url": map[string]interface{}{
							"type":        "string",
							"description": "Canonical URL for this one job posting",
						},
						"company_name": map[string]interface{}{
							"type":        "string",
							"description": "Employer or brand name hiring for this role; infer from title, snippet, or URL path when not explicit",
						},
						"date_posted": map[string]interface{}{
							"type":        "string",
							"description": "Copy the hit <date> field (already YYYY-MM-DD or empty)",
						},
					},
					"required": []string{"title", "url", "company_name", "date_posted"},
				},
			},
		},
		"required": []string{"jobs"},
	}
}
