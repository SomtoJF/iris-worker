package browser

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// renderedScrapeTimeout bounds the DOM/JS work; a busy SPA can keep network alive
// indefinitely, so we do not wait for full idle before reading text.
const renderedScrapeTimeout = 20 * time.Second

// ScrapeRenderedPage is the final fallback for retrieveJobDetails when Serper and
// Colly both fail (Serper 5xx / Colly can't render a client-side SPA like Ashby,
// Lever, Workable). It reads the page the workflow already opened via OpenWebpage
// and returns cleaned, visible text — no markup, CSS, or JS.
func (a *Activity) ScrapeRenderedPage(ctx context.Context, input ScrapeRenderedPageInput) (ScrapeRenderedPageOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return ScrapeRenderedPageOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	page = page.Timeout(renderedScrapeTimeout)

	text, err := extractRenderedText(page)
	if err != nil {
		return ScrapeRenderedPageOutput{}, fmt.Errorf("extract rendered text: %w", err)
	}

	text = cleanRenderedText(text)
	if text == "" {
		return ScrapeRenderedPageOutput{}, fmt.Errorf("rendered page produced no text")
	}

	return ScrapeRenderedPageOutput{Data: text}, nil
}

// extractRenderedText pulls the visible innerText of the main job-content region.
// innerText already excludes <script>/<style> content and returns rendered text
// only. We drop chrome (nav/header/footer/aside) inside the page context, then
// prefer <main>/<article> over <body> so boilerplate is minimized. Cloning avoids
// mutating the live page the agent still drives.
func extractRenderedText(page *rod.Page) (string, error) {
	obj, err := page.Eval(`() => {
		const doc = document.body ? document.body.cloneNode(true) : null;
		if (!doc) return "";
		doc.querySelectorAll('script,style,noscript,svg,nav,header,footer,aside,[role="navigation"],[role="banner"],[role="contentinfo"]').forEach(el => el.remove());
		const pick = doc.querySelector('main') || doc.querySelector('article') || doc;
		return pick.innerText || "";
	}`)
	if err != nil {
		return "", err
	}
	if obj == nil {
		return "", nil
	}
	return obj.Value.Str(), nil
}

var (
	renderedBlankLines = regexp.MustCompile(`\n[ \t]*\n[ \t\n]*`)
	renderedTrailingWS = regexp.MustCompile(`[ \t]+\n`)
	renderedInlineWS   = regexp.MustCompile(`[ \t]{2,}`)
)

// cleanRenderedText normalizes innerText whitespace: trims trailing spaces, collapses
// runs of blank lines to a single blank line, and squeezes long inline space runs.
func cleanRenderedText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = renderedTrailingWS.ReplaceAllString(s, "\n")
	s = renderedInlineWS.ReplaceAllString(s, " ")
	s = renderedBlankLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
