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
	Advanced         string `json:"advanced"`
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
		if input.Advanced == "true" {
			result, err = scrapeWithSerper(input.Url)
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
		if input.Advanced == "true" {
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
		IdUser:           input.IdUser,
		IdJobApplication: input.IdJobApplication,
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

	return ScrapeWebPageOutput{
		Data: markdown,
	}, nil
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
