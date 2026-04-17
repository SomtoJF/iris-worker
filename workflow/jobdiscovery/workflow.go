package jobdiscovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/aipi/types"
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
		jobSourceLever,
		jobSourceWellfound,
		jobSourceWorkable,
		jobSourceAshby,
		// jobSourceRemotefront,
	}

	queries := buildSearchQueries(input, jobSources)
	searchOutputs := runConcurrentWebSearches(ctx, queries, input.Location, input.DateCutoff)
	merged := mergeCleanedResults(jobSources, searchOutputs)

	systemPrompt, err := renderJobDiscoverySystemPrompt()
	if err != nil {
		return JobDiscoveryWorkflowOutput{}, fmt.Errorf("render job discovery system prompt: %w", err)
	}

	userPrompt, err := renderJobDiscoveryUserPrompt(UserPromptData{
		Hits:      userPromptHitsFromMerged(merged),
		TodayDate: time.Now().Format("2006-01-02"),
	})
	if err != nil {
		return JobDiscoveryWorkflowOutput{}, fmt.Errorf("render job discovery user prompt: %w", err)
	}

	llmRequest := types.AIPIRequest{
		SystemMessage:  systemPrompt,
		UserMessage:    userPrompt,
		Model:          "google/gemma-4-31b-it:free",
		ResponseSchema: discoveredJobsResponseSchema(),
		IdUser:         input.IdUser,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return JobDiscoveryWorkflowOutput{}, err
	}

	var parsed struct {
		Jobs []DiscoveredJob `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(llmResponse.Content), &parsed); err != nil {
		return JobDiscoveryWorkflowOutput{}, fmt.Errorf("unmarshal discovered jobs: %w", err)
	}
	if parsed.Jobs == nil {
		parsed.Jobs = []DiscoveredJob{}
	}

	return JobDiscoveryWorkflowOutput{Jobs: parsed.Jobs}, nil
}

func buildSearchQueries(input JobDiscoveryWorkflowInput, jobSources []string) []string {
	q := strings.TrimSpace(input.SearchQuery)
	out := make([]string, 0, len(jobSources))
	for _, host := range jobSources {
		out = append(out, strings.TrimSpace(fmt.Sprintf("site:%s %s", host, q)))
	}
	return out
}

func mergeCleanedResults(jobSources []string, outputs []web.WebSearchOutput) []mergedSearchHit {
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
				Date:    r.Date,
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
							"description": "Date the job was posted in YYYY-MM-DD format",
						},
					},
					"required": []string{"title", "url", "company_name"},
				},
			},
		},
		"required": []string{"jobs"},
	}
}
