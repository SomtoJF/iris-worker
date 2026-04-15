package jobdiscovery

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/helper"
	"go.temporal.io/sdk/workflow"
)

type indexedWebSearchResult struct {
	Index  int
	Output web.WebSearchOutput
	ErrMsg string
}

var (
	systemPromptTemplate *template.Template
	userPromptTemplate   *template.Template
)

// UserPromptData is passed to workflow/jobdiscovery/prompt/user.go.tmpl.
type UserPromptData struct {
	Hits []UserPromptHit
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

// runConcurrentWebSearches runs one WebSearch activity per query using workflow.Go.
// On activity failure, logs a warning and uses empty organic results for that index (resilient merge).
func runConcurrentWebSearches(ctx workflow.Context, queries []string, location, dateCutoff string) []web.WebSearchOutput {
	logger := workflow.GetLogger(ctx)
	n := len(queries)
	if n == 0 {
		return nil
	}

	ch := workflow.NewBufferedChannel(ctx, n)
	for i, query := range queries {
		i, query := i, query
		workflow.Go(ctx, func(gctx workflow.Context) {
			in := web.WebSearchInput{
				Query:      query,
				Location:   location,
				DateCutoff: dateCutoff,
			}
			var out web.WebSearchOutput
			err := workflow.ExecuteActivity(gctx, "WebSearch", in).Get(gctx, &out)
			slot := indexedWebSearchResult{Index: i, Output: out}
			if err != nil {
				slot.ErrMsg = err.Error()
				slot.Output = web.WebSearchOutput{Organic: []web.SerperOrganicResult{}}
				logger.Warn("WebSearch activity failed", "index", i, "error", slot.ErrMsg)
			}
			ch.Send(gctx, slot)
		})
	}

	results := make([]web.WebSearchOutput, n)
	for range queries {
		var slot indexedWebSearchResult
		ch.Receive(ctx, &slot)
		results[slot.Index] = slot.Output
	}
	return results
}
