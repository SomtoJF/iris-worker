package rod

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/go-rod/rod"
)

const renderedScrapeTimeout = 20 * time.Second

var (
	renderedBlankLines = regexp.MustCompile(`\n[ \t]*\n[ \t\n]*`)
	renderedTrailingWS = regexp.MustCompile(`[ \t]+\n`)
	renderedInlineWS   = regexp.MustCompile(`[ \t]{2,}`)
)

func (c *RodBrowserClient) ScrapeRenderedPage(ctx context.Context, id types.ApplicationBrowserID) (string, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return "", err
	}
	page := s.page.Context(ctx).Timeout(renderedScrapeTimeout)
	obj, err := page.Eval(`() => { const doc=document.body?document.body.cloneNode(true):null;if(!doc)return "";doc.querySelectorAll('script,style,noscript,svg,nav,header,footer,aside,[role="navigation"],[role="banner"],[role="contentinfo"]').forEach(el=>el.remove());const pick=doc.querySelector('main')||doc.querySelector('article')||doc;return pick.innerText||""; }`)
	if err != nil {
		return "", fmt.Errorf("extract rendered text: %w", err)
	}
	text := cleanRenderedText(obj.Value.Str())
	if text == "" {
		return "", fmt.Errorf("rendered page produced no text")
	}
	return text, nil
}

func cleanRenderedText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = renderedTrailingWS.ReplaceAllString(text, "\n")
	text = renderedInlineWS.ReplaceAllString(text, " ")
	text = renderedBlankLines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

var successKeywords = []string{
	"application submitted", "application received", "received your application",
	"thank you for applying", "thank you for your application", "thanks for applying",
	"successfully submitted", "application complete", "application has been submitted",
	"we have received", "we've received",
}

var validationKeywords = []string{
	"field is required", "please fill", "please complete", "please correct",
	"fix the errors", "fix the following", "there was a problem submitting",
	"error submitting", "submission failed", "failed to submit",
}

func (c *RodBrowserClient) VerifySubmission(ctx context.Context, id types.ApplicationBrowserID, beforeURL string) (types.SubmissionState, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return types.SubmissionState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return types.SubmissionState{}, err
	}
	page := s.page.Context(ctx)
	if err := page.WaitIdle(time.Second); err != nil {
		return types.SubmissionState{}, fmt.Errorf("wait for page activity: %w", err)
	}
	info, err := page.Info()
	if err != nil {
		return types.SubmissionState{}, fmt.Errorf("read current page URL: %w", err)
	}
	pageText := getPageText(page)
	forms, formErr := page.Elements("form")
	state := types.SubmissionState{
		CurrentURL: info.URL, URLChanged: info.URL != beforeURL,
		FormPresent:      formErr == nil && len(forms) > 0,
		SuccessText:      findSuccessText(pageText),
		ValidationErrors: findValidationErrors(page, pageText),
		PageText:         truncate(pageText, 6*1024),
	}
	return state, nil
}

func getPageText(page *rod.Page) string {
	body, err := page.Element("body")
	if err != nil {
		return ""
	}
	text, err := body.Text()
	if err != nil {
		return ""
	}
	return text
}

func findSuccessText(pageText string) string {
	lower := strings.ToLower(pageText)
	for _, keyword := range successKeywords {
		if strings.Contains(lower, keyword) {
			return keyword
		}
	}
	return ""
}

func findValidationErrors(page *rod.Page, pageText string) []string {
	var out []string
	if invalid, err := page.Elements(`[aria-invalid="true"]`); err == nil && len(invalid) > 0 {
		out = append(out, fmt.Sprintf("%d fields marked aria-invalid", len(invalid)))
	}
	if alerts, err := page.Elements(`[role="alert"]`); err == nil {
		for _, alert := range alerts {
			text, err := alert.Text()
			if err == nil && strings.TrimSpace(text) != "" {
				out = append(out, "alert: "+truncate(strings.TrimSpace(text), 200))
			}
		}
	}
	lower := strings.ToLower(pageText)
	for _, keyword := range validationKeywords {
		if strings.Contains(lower, keyword) {
			out = append(out, "error text: "+keyword)
		}
	}
	return out
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "...[truncated]"
}
