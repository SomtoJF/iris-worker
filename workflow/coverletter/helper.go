package coverletter

import (
	"encoding/json"
	"fmt"
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
const COVER_LETTER_MODEL = "deepseek/deepseek-v4-pro"

var blockedPathSegments = []string{
	"investor", "career", "partner", "product", "feature",
	"leadership", "privacy", "conditions", "policy",
}

// ── data fetching ──

type coverLetterFetchedData struct {
	Resume             sqldb.Resume
	JobApplication     sqldb.JobApplication
	CurrentCoverLetter *string
}

func fetchCoverLetterData(ctx workflow.Context, input CoverLetterWorkflowInput, isEditMode bool) (coverLetterFetchedData, error) {
	var data coverLetterFetchedData

	if err := workflow.ExecuteActivity(ctx, "GetJobApplication", sqldb.GetJobApplicationInput{
		IdJobApplication:          input.IdJobApplication,
		IncludeJobApplicationData: isEditMode,
	}).Get(ctx, &data.JobApplication); err != nil {
		return data, fmt.Errorf("get job application: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "GetResumeByID", data.JobApplication.ResumeId).Get(ctx, &data.Resume); err != nil {
		return data, fmt.Errorf("get resume by id: %w", err)
	}

	if isEditMode && data.JobApplication.JobApplicationData != nil {
		data.CurrentCoverLetter = data.JobApplication.JobApplicationData.CoverLetter
	}

	return data, nil
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
		Model:            COVER_LETTER_MODEL,
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

// editCoverLetter runs the lightweight edit call: single-field schema, one
// validation-retry, same model as the full write.
func editCoverLetter(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication uint) (coverLetterLLMResponse, error) {
	resp, err := callEditCoverLetterLLM(ctx, systemPrompt, userPrompt, idUser, idJobApplication)
	if err != nil {
		return coverLetterLLMResponse{}, err
	}

	if isValidCoverLetter(resp.CoverLetter) {
		resp.CoverLetter = normalizeParagraphs(resp.CoverLetter)
		return resp, nil
	}

	repairUserPrompt := fmt.Sprintf(
		"%s\n\n<repair_request>\nThe previous output did not meet the requirements.\nRewrite it so it is exactly 3 paragraphs (separated by one blank line) and no more than 450 words, while still applying the edit instructions.\nReturn ONLY valid JSON matching the schema.\n</repair_request>\n\n<previous_output>\n%s\n</previous_output>\n",
		userPrompt,
		resp.CoverLetter,
	)

	resp2, err := callEditCoverLetterLLM(ctx, systemPrompt, repairUserPrompt, idUser, idJobApplication)
	if err != nil {
		return coverLetterLLMResponse{}, err
	}
	if !isValidCoverLetter(resp2.CoverLetter) {
		return coverLetterLLMResponse{}, fmt.Errorf("edited cover letter failed validation after retry")
	}
	resp2.CoverLetter = normalizeParagraphs(resp2.CoverLetter)
	return resp2, nil
}

func callEditCoverLetterLLM(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication uint) (coverLetterLLMResponse, error) {
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            COVER_LETTER_MODEL,
		ResponseSchema:   getEditCoverLetterResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: &idJobApplication,
	}

	var llmResponse types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmRequest).Get(ctx, &llmResponse); err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("CallLLM: %w", err)
	}

	var out coverLetterLLMResponse
	if err := json.Unmarshal([]byte(llmResponse.Content), &out); err != nil {
		return coverLetterLLMResponse{}, fmt.Errorf("unmarshal edited cover letter response: %w", err)
	}
	out.CoverLetter = strings.TrimSpace(out.CoverLetter)
	return out, nil
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

func getCoverLetterResponseSchema() map[string]interface{} {
	storyThemes := []string{
		"Leading People",
		"Taking Initiative",
		"Affinity for Challenging Work",
		"Affinity for Different Types of Work",
		"Affinity for Specific Work",
		"Dealing with Failure",
		"Managing Conflict",
		"Driven by Curiosity",
	}

	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tasks_or_skills": map[string]interface{}{
				"type":        "object",
				"description": "Job description requirements categorized by importance.",
				"properties": map[string]interface{}{
					"most_important": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Top 2-3 requirements the employer emphasizes most.",
					},
					"less_important": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Secondary requirements.",
					},
					"negotiable": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "Nice-to-have or preferred requirements.",
					},
				},
				"required": []string{"most_important", "less_important", "negotiable"},
			},
			"qualification_matches": map[string]interface{}{
				"type":        "array",
				"description": "Exactly 2 matches: each maps a top job requirement to a candidate qualification via a story theme.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"requirement": map[string]interface{}{
							"type":        "string",
							"description": "The job requirement being addressed.",
						},
						"qualification": map[string]interface{}{
							"type":        "string",
							"description": "The candidate's matching experience from their resume.",
						},
						"story_theme": map[string]interface{}{
							"type":        "string",
							"description": "The narrative theme framing this qualification.",
							"enum":        storyThemes,
						},
						"connection": map[string]interface{}{
							"type":        "string",
							"description": "One sentence: theme context -> achievement -> result tied to requirement.",
						},
					},
					"required": []string{"requirement", "qualification", "story_theme", "connection"},
				},
				"minItems": 2,
				"maxItems": 2,
			},
			"company_reasons": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Exactly 2 reasons: first value-driven, second industry/topical.",
				"minItems":    2,
				"maxItems":    2,
			},
			"summary_statement": map[string]interface{}{
				"type":        "string",
				"description": "One quantified sentence: candidate's top accomplishment + what they bring.",
			},
			"cover_letter": map[string]interface{}{
				"type":        "string",
				"description": "The complete cover letter. Exactly 3 paragraphs separated by one blank line. Max 450 words.",
			},
		},
		"required": []string{"tasks_or_skills", "qualification_matches", "company_reasons", "summary_statement", "cover_letter"},
	}
}

// getEditCoverLetterResponseSchema is the lightweight schema for edit mode: just
// the revised cover letter, no analysis fields.
func getEditCoverLetterResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"cover_letter": map[string]interface{}{
				"type":        "string",
				"description": "The revised cover letter. Exactly 3 paragraphs separated by one blank line. Max 450 words.",
			},
		},
		"required": []string{"cover_letter"},
	}
}
