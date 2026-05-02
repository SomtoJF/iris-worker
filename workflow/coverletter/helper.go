package coverletter

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/workflow"
)

// ── LLM filter types ──

type llmFilterPromptData struct {
	CompanyName    string
	JobDescription string
	Results        []llmFilterResultItem
}

type llmFilterResultItem struct {
	Index   int    `json:"index"`
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

type llmFilterResponse struct {
	CompanyDomain string                 `json:"company_domain"`
	Results       []llmFilterResultEntry `json:"results"`
}

type llmFilterResultEntry struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	Link  string `json:"link"`
	Valid bool   `json:"valid"`
}

// ── concurrent scrape types ──

type indexedScrapeResult struct {
	Index int
	Page  sqldb.WebsiteCachePage
}

// ── constants ──

const MAX_SCRAPED_CONTENT_LEN = 3000
const MAX_PAGES_TO_SCRAPE = 4

var blockedPathSegments = []string{
	"investor", "career", "partner", "product", "feature",
	"leadership", "privacy", "conditions", "policy",
}

// ── orchestrator ──

func gatherCompanyInfo(ctx workflow.Context, companyName, jobDescription string, idUser, idJobApplication uint) []sqldb.WebsiteCachePage {
	logger := workflow.GetLogger(ctx)

	if strings.TrimSpace(companyName) == "" {
		return nil
	}

	searchOut := searchCompany(ctx, companyName)
	if len(searchOut.Organic) == 0 {
		return nil
	}

	domain, validResults := llmFilterSearchResults(ctx, searchOut.Organic, companyName, jobDescription, idUser, idJobApplication)
	if len(validResults) == 0 {
		return nil
	}

	filtered := programmaticFilterResults(validResults, domain)
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) > MAX_PAGES_TO_SCRAPE {
		filtered = filtered[:MAX_PAGES_TO_SCRAPE]
	}

	// check cache
	if domain != "" {
		var cached sqldb.WebsiteCache
		err := workflow.ExecuteActivity(ctx, "GetWebsiteCache", domain).Get(ctx, &cached)
		if err == nil && len(cached.Pages) > 0 {
			logger.Info("using cached company info", "domain", domain)
			return cached.Pages
		}
	}

	pages := concurrentScrapePages(ctx, filtered, idUser, idJobApplication)
	if len(pages) == 0 {
		return nil
	}

	// save cache
	if domain != "" {
		err := workflow.ExecuteActivity(ctx, "SaveWebsiteCache", sqldb.SaveWebsiteCacheInput{
			Domain: domain,
			Pages:  sqldb.WebsiteCachePages(pages),
		}).Get(ctx, nil)
		if err != nil {
			logger.Warn("SaveWebsiteCache failed", "error", err)
		}
	}

	return pages
}

// ── step helpers ──

func searchCompany(ctx workflow.Context, companyName string) web.WebSearchOutput {
	logger := workflow.GetLogger(ctx)
	in := web.WebSearchInput{
		Query: "about " + companyName + " company",
	}
	var out web.WebSearchOutput
	if err := workflow.ExecuteActivity(ctx, "WebSearch", in).Get(ctx, &out); err != nil {
		logger.Warn("WebSearch failed", "error", err)
		return web.WebSearchOutput{Organic: []web.SerperOrganicResult{}}
	}
	return out
}

func llmFilterSearchResults(ctx workflow.Context, results []web.SerperOrganicResult, companyName, jobDescription string, idUser, idJobApplication uint) (string, []web.SerperOrganicResult) {
	logger := workflow.GetLogger(ctx)

	items := make([]llmFilterResultItem, len(results))
	for i, r := range results {
		items[i] = llmFilterResultItem{
			Index:   i,
			Title:   r.Title,
			Link:    r.Link,
			Snippet: r.Snippet,
		}
	}

	promptData := llmFilterPromptData{
		CompanyName:    companyName,
		JobDescription: jobDescription,
		Results:        items,
	}

	systemPrompt, err := executeTemplateToString(Templates.LLMFilterSystem, promptData)
	if err != nil {
		logger.Warn("render llmfilter system prompt failed", "error", err)
		return "", nil
	}
	userPrompt, err := executeTemplateToString(Templates.LLMFilterUser, promptData)
	if err != nil {
		logger.Warn("render llmfilter user prompt failed", "error", err)
		return "", nil
	}

	llmReq := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            "google/gemma-4-31b-it:free",
		ResponseSchema:   getLLMFilterResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}

	var llmResp types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmReq).Get(ctx, &llmResp); err != nil {
		logger.Warn("LLM filter CallLLM failed", "error", err)
		return "", nil
	}

	var parsed llmFilterResponse
	if err := json.Unmarshal([]byte(llmResp.Content), &parsed); err != nil {
		logger.Warn("LLM filter unmarshal failed", "error", err)
		return "", nil
	}

	var valid []web.SerperOrganicResult
	for _, entry := range parsed.Results {
		if entry.Valid && entry.Index >= 0 && entry.Index < len(results) {
			valid = append(valid, results[entry.Index])
		}
	}

	return strings.TrimSpace(parsed.CompanyDomain), valid
}

func programmaticFilterResults(results []web.SerperOrganicResult, companyDomain string) []web.SerperOrganicResult {
	var filtered []web.SerperOrganicResult
	for _, r := range results {
		parsed, err := url.Parse(r.Link)
		if err != nil {
			continue
		}

		// domain match
		host := strings.ToLower(parsed.Hostname())
		if companyDomain != "" && !strings.Contains(host, strings.ToLower(companyDomain)) {
			continue
		}

		// blocked path segments
		pathLower := strings.ToLower(parsed.Path)
		blocked := false
		for _, seg := range blockedPathSegments {
			if strings.Contains(pathLower, seg) {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}

		filtered = append(filtered, r)
	}
	return filtered
}

func concurrentScrapePages(ctx workflow.Context, results []web.SerperOrganicResult, idUser, idJobApplication uint) []sqldb.WebsiteCachePage {
	logger := workflow.GetLogger(ctx)
	n := len(results)
	if n == 0 {
		return nil
	}

	ch := workflow.NewBufferedChannel(ctx, n)
	for i, r := range results {
		i, r := i, r
		workflow.Go(ctx, func(gctx workflow.Context) {
			in := web.ScrapeWebPageInput{
				Url:              r.Link,
				IdUser:           idUser,
				IdJobApplication: &idJobApplication,
				Advanced:         false,
			}
			var out web.ScrapeWebPageOutput
			err := workflow.ExecuteActivity(gctx, "ScrapeWebPage", in).Get(gctx, &out)

			page := sqldb.WebsiteCachePage{
				Title: r.Title,
				Url:   r.Link,
			}
			if err != nil {
				logger.Warn("ScrapeWebPage failed", "url", r.Link, "error", err)
			} else {
				content := strings.TrimSpace(out.Data)
				if len(content) > MAX_SCRAPED_CONTENT_LEN {
					content = content[:MAX_SCRAPED_CONTENT_LEN]
				}
				page.Content = content
			}
			ch.Send(gctx, indexedScrapeResult{Index: i, Page: page})
		})
	}

	pages := make([]sqldb.WebsiteCachePage, n)
	for range results {
		var slot indexedScrapeResult
		ch.Receive(ctx, &slot)
		pages[slot.Index] = slot.Page
	}

	// filter out pages with empty content
	var nonEmpty []sqldb.WebsiteCachePage
	for _, p := range pages {
		if p.Content != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	return nonEmpty
}

// ── response schema ──

func getLLMFilterResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"company_domain": map[string]interface{}{
				"type":        "string",
				"description": "The company's primary domain (e.g. stripe.com)",
			},
			"results": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"index": map[string]interface{}{
							"type":        "integer",
							"description": "Original index of the search result",
						},
						"title": map[string]interface{}{
							"type":        "string",
							"description": "Title of the search result",
						},
						"link": map[string]interface{}{
							"type":        "string",
							"description": "URL of the search result",
						},
						"valid": map[string]interface{}{
							"type":        "boolean",
							"description": "Whether this result is from the company domain and contains relevant company info",
						},
					},
					"required": []string{"index", "title", "link", "valid"},
				},
			},
		},
		"required": []string{"company_domain", "results"},
	}
}
