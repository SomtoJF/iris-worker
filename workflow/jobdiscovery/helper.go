package jobdiscovery

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/helper"
	"go.temporal.io/sdk/workflow"
)

var (
	systemPromptTemplate *template.Template
	userPromptTemplate   *template.Template
)

// UserPromptData is passed to workflow/jobdiscovery/prompt/user.go.tmpl.
type UserPromptData struct {
	Hits      []UserPromptHit
	TodayDate string
}

// UserPromptHit is one search result block in the user template.
type UserPromptHit struct {
	Index          int
	Title          string
	Link           string
	Snippet        string
	SourceHostHint string
	Date           string
}

func SetTemplates() error {
	var err error
	systemPromptTemplate, err = helper.LoadTemplate("workflow/jobdiscovery/prompt/system.go.tmpl")
	if err != nil {
		return fmt.Errorf("jobdiscovery system prompt: %w", err)
	}
	userPromptTemplate, err = helper.LoadTemplate("workflow/jobdiscovery/prompt/user.go.tmpl")
	if err != nil {
		return fmt.Errorf("jobdiscovery user prompt: %w", err)
	}
	return nil
}

func renderJobDiscoverySystemPrompt() (string, error) {
	if systemPromptTemplate == nil {
		return "", fmt.Errorf("jobdiscovery system prompt template not loaded")
	}
	var b bytes.Buffer
	if err := systemPromptTemplate.Execute(&b, nil); err != nil {
		return "", err
	}
	return b.String(), nil
}

func renderJobDiscoveryUserPrompt(data UserPromptData) (string, error) {
	if userPromptTemplate == nil {
		return "", fmt.Errorf("jobdiscovery user prompt template not loaded")
	}
	var b bytes.Buffer
	if err := userPromptTemplate.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

func userPromptHitsFromMerged(merged []mergedSearchHit) []UserPromptHit {
	out := make([]UserPromptHit, len(merged))
	for i, m := range merged {
		out[i] = UserPromptHit{
			Index:          i,
			Title:          m.Title,
			Link:           m.Link,
			Snippet:        m.Snippet,
			SourceHostHint: m.Source,
			Date:           m.Date,
		}
	}
	return out
}

// runBatchWebSearch runs all queries in one WebSearchBatch activity (Serper multi-query).
// On activity failure, logs a warning and returns empty organic results per query.
func runBatchWebSearch(ctx workflow.Context, queries []string, location, dateCutoff string) []web.WebSearchOutput {
	logger := workflow.GetLogger(ctx)
	n := len(queries)
	if n == 0 {
		return nil
	}

	empty := func() []web.WebSearchOutput {
		out := make([]web.WebSearchOutput, n)
		for i := range out {
			out[i] = web.WebSearchOutput{Organic: []web.SerperOrganicResult{}}
		}
		return out
	}

	inputs := make([]web.WebSearchInput, n)
	for i, query := range queries {
		inputs[i] = web.WebSearchInput{
			Query:      query,
			Location:   location,
			DateCutoff: dateCutoff,
		}
	}

	var batchOut web.WebSearchBatchOutput
	err := workflow.ExecuteActivity(ctx, "WebSearchBatch", web.WebSearchBatchInput{Queries: inputs}).Get(ctx, &batchOut)
	if err != nil {
		logger.Warn("WebSearchBatch activity failed", "error", err)
		return empty()
	}
	if len(batchOut.Results) != n {
		logger.Warn("WebSearchBatch result count mismatch", "want", n, "got", len(batchOut.Results))
		out := empty()
		copy(out, batchOut.Results)
		return out
	}
	return batchOut.Results
}
