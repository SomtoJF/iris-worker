package coverletter

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/workflow"
)

// ── LLM filter types ──

type llmFilterDecisionState struct {
	CompanyName    string
	JobDescription string
	Results        []llmFilterResultItem
}

type llmFilterResultItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

// ── concurrent scrape types ──

type indexedScrapeResult struct {
	Index int
	Page  sqldb.WebsiteCachePage
}

// ── constants ──

const MAX_SCRAPED_CONTENT_LEN = 3000
const MAX_PAGES_TO_SCRAPE = 5
const COVER_LETTER_MODEL = "deepseek/deepseek-v4-pro"
const companyPageJevThreshold = 0.65

var blockedPathSegments = []string{
	"investor", "career", "partner", "product", "feature",
	"leadership", "privacy", "conditions", "policy",
}

// ── data fetching ──

type coverLetterFetchedData struct {
	Resume             sqldb.Resume
	CompanyName        string
	JobTitle           string
	JobDescription     string
	CurrentCoverLetter *string
}

func fetchCoverLetterData(ctx workflow.Context, input CoverLetterWorkflowInput, isEditMode bool) (coverLetterFetchedData, error) {
	var data coverLetterFetchedData

	if input.IdCoverLetter != 0 {
		var coverLetter sqldb.CoverLetter
		if err := workflow.ExecuteActivity(ctx, "GetCoverLetter", sqldb.GetCoverLetterInput{
			IdCoverLetter: input.IdCoverLetter,
		}).Get(ctx, &coverLetter); err != nil {
			return data, fmt.Errorf("get cover letter: %w", err)
		}

		if err := workflow.ExecuteActivity(ctx, "GetResumeByID", coverLetter.ResumeId).Get(ctx, &data.Resume); err != nil {
			return data, fmt.Errorf("get resume by id: %w", err)
		}

		data.CompanyName = coverLetter.CompanyName
		data.JobTitle = coverLetter.JobTitle
		data.JobDescription = coverLetter.JobDescription
		if isEditMode {
			data.CurrentCoverLetter = coverLetter.Body
		}
		return data, nil
	}

	if input.IdJobApplication == nil {
		return data, fmt.Errorf("cover letter workflow requires id_cover_letter or id_job_application")
	}

	var jobApplication sqldb.JobApplication
	if err := workflow.ExecuteActivity(ctx, "GetJobApplication", sqldb.GetJobApplicationInput{
		IdJobApplication:          *input.IdJobApplication,
		IncludeJobApplicationData: isEditMode,
		IncludeCoverLetter:        isEditMode,
	}).Get(ctx, &jobApplication); err != nil {
		return data, fmt.Errorf("get job application: %w", err)
	}

	if err := workflow.ExecuteActivity(ctx, "GetResumeByID", jobApplication.ResumeId).Get(ctx, &data.Resume); err != nil {
		return data, fmt.Errorf("get resume by id: %w", err)
	}

	data.CompanyName = jobApplication.CompanyName
	data.JobTitle = jobApplication.JobTitle
	data.JobDescription = jobApplication.JobDescription
	if isEditMode {
		if jobApplication.CoverLetter != nil && jobApplication.CoverLetter.Body != nil {
			data.CurrentCoverLetter = jobApplication.CoverLetter.Body
		} else if jobApplication.JobApplicationData != nil {
			data.CurrentCoverLetter = jobApplication.JobApplicationData.CoverLetter
		}
	}

	return data, nil
}

func derefUint(v *uint) uint {
	if v == nil {
		return 0
	}
	return *v
}

// ── orchestrator ──
/*
Gather company info from search results and website cache.
Filter out irrelevant pages with LLM filter.
Scrape relevant pages.
Cache pages.
*/
func gatherCompanyInfo(ctx workflow.Context, companyName, jobDescription string, idUser uint, idJobApplication *uint) []sqldb.WebsiteCachePage {
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

func llmFilterSearchResults(ctx workflow.Context, results []web.SerperOrganicResult, companyName, jobDescription string, idUser uint, idJobApplication *uint) (string, []web.SerperOrganicResult) {
	logger := workflow.GetLogger(ctx)

	items := make([]llmFilterResultItem, len(results))
	domainCriteria := make(map[string]string, len(results))
	resultHosts := make([]string, len(results))
	resultQuestionKeys := make([]string, len(results))
	for i, r := range results {
		items[i] = llmFilterResultItem{
			Title:   r.Title,
			Link:    r.Link,
			Snippet: r.Snippet,
		}
		resultQuestionKeys[i] = r.Link
		host, err := normalizeSearchResultHost(r.Link)
		if err != nil {
			continue
		}
		resultHosts[i] = host
		domainCriteria[host] = "The company's official website domain represented by this search result host."
	}

	if len(domainCriteria) == 0 {
		return "", nil
	}

	state := llmFilterDecisionState{
		CompanyName:    companyName,
		JobDescription: jobDescription,
		Results:        items,
	}

	questions := map[string]types.JevQuestion{
		"company_domain": {
			Type:         "choice",
			Instructions: "Which candidate hostname is the company's official primary website? Choose only from the listed candidate hosts. Use the company name, job description, and result metadata to disambiguate.",
			Criteria:     domainCriteria,
		},
	}
	for i := range items {
		if resultHosts[i] == "" {
			continue
		}
		questions[resultQuestionKeys[i]] = types.JevQuestion{
			Type:         "noul",
			Instructions: "Does this search result likely lead to a page containing useful company overview, about, mission, vision, values, or culture information? The homepage is useful. News, blogs, jobs, careers indexes, product/pricing pages, and documentation are not useful. Do not reject a page only because it is hosted outside the company's official domain.",
			Criteria: map[string]string{
				"true":  "The result is likely a relevant company overview/about/culture page.",
				"false": "The result is a job listing, careers index, news/blog, product, pricing, documentation, or unrelated page.",
			},
		}
	}

	var jevResp types.JevResponse
	if err := workflow.ExecuteActivity(ctx, "CallDecisions", types.JevRequest{
		State: map[string]any{
			"company_name":    state.CompanyName,
			"job_description": state.JobDescription,
			"search_results":  state.Results,
		},
		Questions:        questions,
		IdUser:           idUser,
		IdJobApplication: idJobApplication,
	}).Get(ctx, &jevResp); err != nil {
		logger.Warn("JEV company-page filter failed", "error", err)
		return "", nil
	}

	domainAnswer, ok := jevResp.Answers["company_domain"]
	if !ok || domainAnswer.Type != "choice" || domainAnswer.Choice == "" {
		logger.Warn("JEV company-page filter returned invalid company-domain answer")
		return "", nil
	}
	_, ok = domainCriteria[domainAnswer.Choice]
	if !ok {
		logger.Warn("JEV company-page filter selected unknown company domain", "domain", domainAnswer.Choice)
		return "", nil
	}
	domain := domainAnswer.Choice

	var validResults []web.SerperOrganicResult
	for i, result := range results {
		if resultHosts[i] == "" {
			continue
		}
		answer, ok := jevResp.Answers[resultQuestionKeys[i]]
		if !ok || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
			logger.Warn("JEV company-page filter returned invalid result judgment", "url", result.Link)
			return "", nil
		}
		if *answer.Noul >= companyPageJevThreshold {
			validResults = append(validResults, result)
		}
	}

	return domain, validResults
}

func normalizeSearchResultHost(link string) (string, error) {
	parsed, err := url.Parse(link)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q", parsed.Scheme)
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	host = strings.TrimPrefix(host, "www.")
	if host == "" {
		return "", fmt.Errorf("URL has no hostname")
	}
	return host, nil
}

func hostMatchesCompanyDomain(host, companyDomain string) bool {
	return host == companyDomain || strings.HasSuffix(host, "."+companyDomain)
}

/* Prioritize official-domain results, discard blocked pages, and cap scrape candidates. */
func programmaticFilterResults(results []web.SerperOrganicResult, companyDomain string) []web.SerperOrganicResult {
	type rankedResult struct {
		result  web.SerperOrganicResult
		company bool
	}
	ranked := make([]rankedResult, 0, len(results))
	for _, r := range results {
		host, err := normalizeSearchResultHost(r.Link)
		if err != nil {
			continue
		}

		parsed, err := url.Parse(r.Link)
		if err != nil {
			continue
		}
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

		ranked = append(ranked, rankedResult{
			result:  r,
			company: companyDomain != "" && hostMatchesCompanyDomain(host, strings.ToLower(companyDomain)),
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].company && !ranked[j].company
	})

	if len(ranked) > MAX_PAGES_TO_SCRAPE {
		ranked = ranked[:MAX_PAGES_TO_SCRAPE]
	}

	filtered := make([]web.SerperOrganicResult, len(ranked))
	for i, item := range ranked {
		filtered[i] = item.result
	}
	return filtered
}

func concurrentScrapePages(ctx workflow.Context, results []web.SerperOrganicResult, idUser uint, idJobApplication *uint) []sqldb.WebsiteCachePage {
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
				IdJobApplication: idJobApplication,
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

func generateCoverLetter(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication *uint) (coverLetterLLMResponse, error) {
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

func callCoverLetterLLM(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication *uint) (coverLetterLLMResponse, error) {
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            COVER_LETTER_MODEL,
		ResponseSchema:   getCoverLetterResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: idJobApplication,
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
func editCoverLetter(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication *uint) (coverLetterLLMResponse, error) {
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

func callEditCoverLetterLLM(ctx workflow.Context, systemPrompt, userPrompt string, idUser uint, idJobApplication *uint) (coverLetterLLMResponse, error) {
	llmRequest := types.AIPIRequest{
		SystemMessage:    systemPrompt,
		UserMessage:      userPrompt,
		Model:            COVER_LETTER_MODEL,
		ResponseSchema:   getEditCoverLetterResponseSchema(),
		IdUser:           idUser,
		IdJobApplication: idJobApplication,
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
				"description": "One quantified sentence: candidate's top accomplishment + what they bring. Must be distinct from the opener.",
			},
			"opener": map[string]interface{}{
				"type":        "string",
				"description": "First 1-2 sentences of the letter, filled from the opener bank (style A, B, C, or D). The cover letter must start with this opener.",
			},
			"cover_letter": map[string]interface{}{
				"type":        "string",
				"description": "The complete cover letter. Exactly 3 paragraphs separated by one blank line. Max 450 words. Must start with the opener.",
			},
		},
		"required": []string{"tasks_or_skills", "qualification_matches", "company_reasons", "summary_statement", "opener", "cover_letter"},
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
