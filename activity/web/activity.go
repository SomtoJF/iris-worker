package web

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/JohannesKaufmann/html-to-markdown"
	"github.com/gocolly/colly/v2"
)

type Activity struct{}

func NewActivity() *Activity {
	return &Activity{}
}

type ScrapeWebPageInput struct {
	Url string `json:"url"`
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
		result, err := scrapeWithColly(input.Url, parsedURL.Host)
		if err != nil {
			errChan <- err
			return
		}
		resultChan <- result
	}()

	select {
	case result := <-resultChan:
		return result, nil
	case err := <-errChan:
		return ScrapeWebPageOutput{}, err
	case <-ctx.Done():
		return ScrapeWebPageOutput{}, fmt.Errorf("scraping cancelled: %w", ctx.Err())
	}
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
