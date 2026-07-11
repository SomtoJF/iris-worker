package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/gocolly/colly/v2"
	"gorm.io/gorm"
)

type Activity struct {
	db *gorm.DB
}

func NewActivity(db *gorm.DB) *Activity {
	return &Activity{db: db}
}

type ScrapeWebPageInput struct {
	Url              string `json:"url"`
	IdUser           uint   `json:"id_user"`
	IdJobApplication *uint  `json:"id_job_application,omitempty"`
	Advanced         bool   `json:"advanced"`
}

type ScrapeWebPageOutput struct {
	Data string `json:"data"`
}

func (a *Activity) ScrapeWebPage(ctx context.Context, input ScrapeWebPageInput) (ScrapeWebPageOutput, error) {
	// Validate input
	if input.Url == "" {
		return ScrapeWebPageOutput{}, fmt.Errorf("url cannot be empty")
	}

	parsedURL, err := url.Parse(input.Url)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return ScrapeWebPageOutput{}, fmt.Errorf("invalid url format: %s", input.Url)
	}

	// Scrape with timeout
	resultChan := make(chan ScrapeWebPageOutput, 1)
	errChan := make(chan error, 1)

	go func() {
		var result ScrapeWebPageOutput
		var err error
		if input.Advanced {
			result, err = scrapeWithSerper(input.Url)
			if err != nil {
				log.Printf("serper scrape failed, falling back to colly: %v", err)
				result, err = scrapeWithColly(input.Url, parsedURL.Host)
			}
		} else {
			result, err = scrapeWithColly(input.Url, parsedURL.Host)
		}
		if err != nil {
			errChan <- err
			return
		}
		resultChan <- result
	}()

	select {
	case result := <-resultChan:
		if input.Advanced {
			a.saveScrapesCostTracking(input)
		}
		return result, nil
	case err := <-errChan:
		return ScrapeWebPageOutput{}, err
	case <-ctx.Done():
		return ScrapeWebPageOutput{}, fmt.Errorf("scraping cancelled: %w", ctx.Err())
	}
}

func (a *Activity) saveScrapesCostTracking(input ScrapeWebPageInput) {
	record := sqldb.CostTracking{
		UserId:           input.IdUser,
		JobApplicationId: input.IdJobApplication,
		Type:             sqldb.CostTrackingTypeWebScraping,
		OutputCost:       0.001,
		TotalCost:        0.001,
	}
	if err := a.db.Create(&record).Error; err != nil {
		log.Printf("failed to save scrape cost tracking record: %v", err)
	}
}

func scrapeWithSerper(targetURL string) (ScrapeWebPageOutput, error) {
	apiKey := os.Getenv("SERPER_API_KEY")
	if apiKey == "" {
		return ScrapeWebPageOutput{}, fmt.Errorf("SERPER_API_KEY env var not set")
	}

	body, err := json.Marshal(map[string]string{"url": targetURL})
	if err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, "https://scrape.serper.dev", bytes.NewReader(body))
	if err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("serper request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("failed to read serper response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ScrapeWebPageOutput{}, fmt.Errorf("serper returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return ScrapeWebPageOutput{Data: string(respBody)}, nil
}

func scrapeWithColly(targetURL, domain string) (ScrapeWebPageOutput, error) {
	var htmlContent string
	var scrapeErr error
	linksSet := map[string]struct{}{}
	var links []string

	baseURL, err := url.Parse(targetURL)
	if err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("invalid url format: %s", targetURL)
	}

	// Create collector
	c := colly.NewCollector(
		colly.AllowedDomains(domain),
		colly.UserAgent("Mozilla/5.0 (compatible; IrisBot/1.0)"),
	)

	c.SetRequestTimeout(30 * time.Second)

	// Handle errors
	c.OnError(func(_ *colly.Response, err error) {
		scrapeErr = fmt.Errorf("failed to scrape page: %w", err)
	})

	// Extract main content
	c.OnHTML("body", func(e *colly.HTMLElement) {
		htmlContent = tryExtractMainContent(e)
	})

	// Extract links (before converting to markdown)
	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		href := strings.TrimSpace(e.Attr("href"))
		if href == "" || strings.HasPrefix(href, "#") {
			return
		}

		u, err := url.Parse(href)
		if err != nil {
			return
		}

		resolved := baseURL.ResolveReference(u)
		resolved.Fragment = ""

		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			return
		}
		if resolved.Host != baseURL.Host {
			return
		}

		normalized := resolved.String()
		if _, ok := linksSet[normalized]; ok {
			return
		}
		linksSet[normalized] = struct{}{}
		links = append(links, normalized)
	})

	// Visit URL
	if err := c.Visit(targetURL); err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("failed to visit %s: %w", targetURL, err)
	}

	if scrapeErr != nil {
		return ScrapeWebPageOutput{}, scrapeErr
	}

	if htmlContent == "" {
		return ScrapeWebPageOutput{}, fmt.Errorf("no content found at %s", targetURL)
	}

	// Convert to markdown
	converter := md.NewConverter("", true, nil)
	markdown, err := converter.ConvertString(htmlContent)
	if err != nil {
		return ScrapeWebPageOutput{}, fmt.Errorf("failed to convert html to markdown: %w", err)
	}

	// Clean up markdown
	markdown = strings.TrimSpace(markdown)
	markdown = sanitizeMarkdown(markdown)

	return ScrapeWebPageOutput{
		Data: markdown,
	}, nil
}

// WebSearchInput is sent to Serper's Google Search API. Query must be the Google q string
// (e.g. site:host keywords) without date-range text; DateCutoff is mapped to tbs inside the activity.
// Location: ISO 3166-1 alpha-2 (e.g. ng) sets gl; otherwise it is appended to q for regional context.
type WebSearchInput struct {
	Query      string `json:"query"`
	Location   string `json:"location"`
	DateCutoff string `json:"date_cutoff"`
}

type SerperOrganicResult struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
	Date    string `json:"date"`
}

type WebSearchOutput struct {
	Organic []SerperOrganicResult `json:"organic"`
	RawJSON string                `json:"raw_json,omitempty"`
}

type serperGoogleSearchRequest struct {
	Q   string `json:"q"`
	Gl  string `json:"gl,omitempty"`
	Tbs string `json:"tbs,omitempty"`
}

type serperGoogleSearchResponse struct {
	Organic []SerperOrganicResult `json:"organic"`
}

// WebSearchBatchInput sends multiple Google searches in one Serper request (JSON array body).
type WebSearchBatchInput struct {
	Queries []WebSearchInput `json:"queries"`
}

// WebSearchBatchOutput holds one WebSearchOutput per input query, in the same order.
type WebSearchBatchOutput struct {
	Results []WebSearchOutput `json:"results"`
}

func (a *Activity) WebSearch(ctx context.Context, input WebSearchInput) (WebSearchOutput, error) {
	apiKey := os.Getenv("SERPER_API_KEY")
	if apiKey == "" {
		return WebSearchOutput{}, fmt.Errorf("SERPER_API_KEY env var not set")
	}

	reqBody, err := buildSerperGoogleSearchRequest(input)
	if err != nil {
		return WebSearchOutput{}, err
	}

	respBody, err := postSerperSearch(ctx, apiKey, reqBody)
	if err != nil {
		return WebSearchOutput{}, err
	}

	var parsed serperGoogleSearchResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return WebSearchOutput{}, fmt.Errorf("unmarshal serper search response: %w", err)
	}

	if parsed.Organic == nil {
		parsed.Organic = []SerperOrganicResult{}
	}

	return WebSearchOutput{
		Organic: parsed.Organic,
		RawJSON: string(respBody),
	}, nil
}

// WebSearchBatch runs multiple Serper Google searches in a single HTTP call.
// Serper accepts a JSON array of {q, gl, tbs} objects and returns a matching array of results.
func (a *Activity) WebSearchBatch(ctx context.Context, input WebSearchBatchInput) (WebSearchBatchOutput, error) {
	if len(input.Queries) == 0 {
		return WebSearchBatchOutput{Results: []WebSearchOutput{}}, nil
	}

	apiKey := os.Getenv("SERPER_API_KEY")
	if apiKey == "" {
		return WebSearchBatchOutput{}, fmt.Errorf("SERPER_API_KEY env var not set")
	}

	reqs := make([]serperGoogleSearchRequest, 0, len(input.Queries))
	for i, q := range input.Queries {
		reqBody, err := buildSerperGoogleSearchRequest(q)
		if err != nil {
			return WebSearchBatchOutput{}, fmt.Errorf("query[%d]: %w", i, err)
		}
		reqs = append(reqs, reqBody)
	}

	respBody, err := postSerperSearch(ctx, apiKey, reqs)
	if err != nil {
		return WebSearchBatchOutput{}, err
	}

	var parsed []serperGoogleSearchResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return WebSearchBatchOutput{}, fmt.Errorf("unmarshal serper batch search response: %w", err)
	}
	if len(parsed) != len(input.Queries) {
		return WebSearchBatchOutput{}, fmt.Errorf("serper batch returned %d results for %d queries", len(parsed), len(input.Queries))
	}

	results := make([]WebSearchOutput, len(parsed))
	for i, p := range parsed {
		organic := p.Organic
		if organic == nil {
			organic = []SerperOrganicResult{}
		}
		results[i] = WebSearchOutput{Organic: organic}
	}

	return WebSearchBatchOutput{Results: results}, nil
}

func postSerperSearch(ctx context.Context, apiKey string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal serper search request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://google.serper.dev/search", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create serper search request: %w", err)
	}
	req.Header.Set("X-API-KEY", apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serper search request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read serper search response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("serper search returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func buildSerperGoogleSearchRequest(input WebSearchInput) (serperGoogleSearchRequest, error) {
	base := strings.TrimSpace(input.Query)
	if base == "" {
		return serperGoogleSearchRequest{}, fmt.Errorf("query cannot be empty")
	}

	loc := strings.TrimSpace(input.Location)
	gl := ""
	q := base
	if loc != "" {
		if isLikelyISO3166Alpha2(loc) {
			gl = strings.ToLower(loc)
		} else {
			q = strings.TrimSpace(base + " " + loc)
		}
	}

	tbs := dateCutoffToSerperTbs(input.DateCutoff)
	return serperGoogleSearchRequest{Q: q, Gl: gl, Tbs: tbs}, nil
}

func isLikelyISO3166Alpha2(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, r := range s {
		if r < 'A' || (r > 'Z' && r < 'a') || r > 'z' {
			return false
		}
	}
	return true
}

// dateCutoffToSerperTbs maps workflow date_cutoff to Google's tbs parameter (e.g. qdr:d).
// Accepts shorthand or an already-valid tbs value prefixed with qdr:.
func dateCutoffToSerperTbs(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "qdr:") || strings.HasPrefix(s, "cdr:") {
		return s
	}
	switch s {
	case "d", "day", "1d":
		return "qdr:d"
	case "w", "week", "7d":
		return "qdr:w"
	case "m", "month", "30d":
		return "qdr:m"
	case "y", "year":
		return "qdr:y"
	default:
		return ""
	}
}

func tryExtractMainContent(e *colly.HTMLElement) string {
	// Try common content selectors in priority order
	selectors := []string{
		"article",
		"[role='main']",
		"main",
		".content",
		"#content",
		".post-content",
		".entry-content",
		".article-content",
	}

	for _, selector := range selectors {
		if html, err := e.DOM.Find(selector).First().Html(); err == nil && html != "" {
			return html
		}
	}

	// Fallback to body
	html, _ := e.DOM.Html()
	return html
}

var (
	reMarkdownImage = regexp.MustCompile(`!\[[^\]]*]\([^)]+\)`)
	reMarkdownLink  = regexp.MustCompile(`\[(?P<text>[^\]]*)]\([^)]+\)`)
)

// sanitizeMarkdown aggressively strips link + image markdown to keep scraped text compact.
func sanitizeMarkdown(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}

	// Drop images entirely.
	s = reMarkdownImage.ReplaceAllString(s, "")

	// Convert links to visible text only.
	// Example: [Log in](https://...) => Log in
	s = reMarkdownLink.ReplaceAllString(s, "${text}")

	// Remove any leftover empty links from nested patterns like [![](...)](/)
	s = strings.ReplaceAll(s, "[]", "")

	// Collapse excessive blank lines introduced by removals.
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blankRun := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun > 2 {
				continue
			}
			out = append(out, "")
			continue
		}
		blankRun = 0
		out = append(out, line)
	}

	return strings.TrimSpace(strings.Join(out, "\n"))
}
