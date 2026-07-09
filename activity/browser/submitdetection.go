package browser

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const (
	captureWindow    = 12 * time.Second
	capturePoll      = 500 * time.Millisecond
	maxBodyBytes     = 8 * 1024
	maxPageTextBytes = 6 * 1024
)

// Hosts whose requests are never application submissions (analytics, tracking).
var ignoredRequestHosts = []string{
	"google-analytics.com",
	"googletagmanager.com",
	"doubleclick.net",
	"facebook.com",
	"facebook.net",
	"segment.io",
	"segment.com",
	"sentry.io",
	"mixpanel.com",
	"amplitude.com",
	"hotjar.com",
	"clarity.ms",
	"linkedin.com/px",
	"bat.bing.com",
	"fullstory.com",
	"datadoghq.com",
	"intercom.io",
	"launchdarkly.com",
	"newrelic.com",
	"nr-data.net",
	"posthog.com",
}

// ClickSubmitAndCapture clicks the submit element exactly once while recording
// every submit-shaped network request (POST/PUT/PATCH document/XHR/fetch) and
// its response. It never guesses a form action URL. NOT retry-safe: register
// with MaximumAttempts=1.
func (a *Activity) ClickSubmitAndCapture(ctx context.Context, input ClickSubmitInput) (ClickSubmitOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return ClickSubmitOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}
	if !ctxExists {
		return ClickSubmitOutput{}, fmt.Errorf("no incognito context for workflow %s", input.WorkflowID)
	}

	element, err := a.resolveElement(page, input.ElementIndex)
	if err != nil {
		return ClickSubmitOutput{}, err
	}

	beforeURL := page.MustInfo().URL
	pagesBefore := incognitoCtx.MustPages()
	beforeCount := len(pagesBefore)

	capture := newNetworkCapture(page)
	defer capture.stop()

	if err := element.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return ClickSubmitOutput{}, fmt.Errorf("failed to click element: %w", err)
	}

	waitForCapturedResponse(capture)

	newTabOpened := a.handleNewTab(input.WorkflowID, incognitoCtx, beforeCount)

	return ClickSubmitOutput{
		BeforeURL:    beforeURL,
		NewTabOpened: newTabOpened,
		Requests:     capture.results(),
	}, nil
}

// waitForCapturedResponse polls until at least one submit-shaped request has a
// response (then grants a short grace period for its body), or the window ends.
func waitForCapturedResponse(capture *networkCapture) {
	deadline := time.Now().Add(captureWindow)
	for time.Now().Before(deadline) {
		time.Sleep(capturePoll)
		if capture.hasResponse() {
			time.Sleep(1 * time.Second)
			return
		}
	}
}

type networkCapture struct {
	mu       sync.Mutex
	requests map[proto.NetworkRequestID]*CapturedRequest
	order    []proto.NetworkRequestID
	page     *rod.Page
	cancel   func()
	done     chan struct{}
}

func newNetworkCapture(page *rod.Page) *networkCapture {
	ctx, cancel := context.WithCancel(page.GetContext())
	capturePage := page.Context(ctx)

	c := &networkCapture{
		requests: make(map[proto.NetworkRequestID]*CapturedRequest),
		page:     capturePage,
		cancel:   cancel,
		done:     make(chan struct{}),
	}

	go func() {
		defer close(c.done)
		capturePage.EachEvent(
			func(e *proto.NetworkRequestWillBeSent) {
				if !isSubmitShaped(e.Request.Method, e.Request.URL, e.Type) {
					return
				}
				c.mu.Lock()
				defer c.mu.Unlock()
				if _, ok := c.requests[e.RequestID]; !ok {
					c.requests[e.RequestID] = &CapturedRequest{
						URL:          e.Request.URL,
						Method:       e.Request.Method,
						ResourceType: string(e.Type),
					}
					c.order = append(c.order, e.RequestID)
				}
			},
			func(e *proto.NetworkResponseReceived) {
				c.mu.Lock()
				req, ok := c.requests[e.RequestID]
				if ok {
					req.StatusCode = e.Response.Status
				}
				c.mu.Unlock()
			},
			func(e *proto.NetworkLoadingFinished) {
				c.mu.Lock()
				req, ok := c.requests[e.RequestID]
				c.mu.Unlock()
				if !ok {
					return
				}
				// Body fetch fails after document navigations; the status code alone is enough then.
				body, err := proto.NetworkGetResponseBody{RequestID: e.RequestID}.Call(c.page)
				if err != nil {
					return
				}
				c.mu.Lock()
				req.ResponseBody = truncate(body.Body, maxBodyBytes)
				c.mu.Unlock()
			},
		)()
	}()

	return c
}

func (c *networkCapture) hasResponse() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, req := range c.requests {
		if req.StatusCode > 0 {
			return true
		}
	}
	return false
}

func (c *networkCapture) results() []CapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CapturedRequest, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, *c.requests[id])
	}
	return out
}

func (c *networkCapture) stop() {
	c.cancel()
	<-c.done
}

func isSubmitShaped(method string, requestURL string, resourceType proto.NetworkResourceType) bool {
	switch method {
	case "POST", "PUT", "PATCH":
	default:
		return false
	}

	switch resourceType {
	case proto.NetworkResourceTypeDocument, proto.NetworkResourceTypeXHR, proto.NetworkResourceTypeFetch:
	default:
		return false
	}

	lowerURL := strings.ToLower(requestURL)
	for _, host := range ignoredRequestHosts {
		if strings.Contains(lowerURL, host) {
			return false
		}
	}
	return true
}

// VerifySubmissionState inspects the current page without clicking anything,
// so it is safe to retry. It reports raw signals; the workflow decides.
func (a *Activity) VerifySubmissionState(ctx context.Context, input VerifySubmissionStateInput) (VerifySubmissionStateOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return VerifySubmissionStateOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	page.MustWaitIdle()

	currentURL := page.MustInfo().URL
	pageText := getPageText(page)

	forms, err := page.Elements("form")
	formPresent := err == nil && len(forms) > 0

	return VerifySubmissionStateOutput{
		CurrentURL:       currentURL,
		URLChanged:       currentURL != input.BeforeURL,
		FormPresent:      formPresent,
		SuccessText:      findSuccessText(pageText),
		ValidationErrors: findValidationErrors(page, pageText),
		PageText:         truncate(pageText, maxPageTextBytes),
	}, nil
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
	lowerText := strings.ToLower(pageText)
	keywords := []string{
		"application submitted",
		"application received",
		"received your application",
		"thank you for applying",
		"thank you for your application",
		"thanks for applying",
		"successfully submitted",
		"application complete",
		"application has been submitted",
		"we have received",
		"we've received",
	}

	for _, kw := range keywords {
		if strings.Contains(lowerText, kw) {
			return kw
		}
	}
	return ""
}

func findValidationErrors(page *rod.Page, pageText string) []string {
	var errors []string

	// DOM signals are stronger than text: fields flagged invalid, visible alerts.
	if invalid, err := page.Elements(`[aria-invalid="true"]`); err == nil && len(invalid) > 0 {
		errors = append(errors, fmt.Sprintf("%d fields marked aria-invalid", len(invalid)))
	}
	if alerts, err := page.Elements(`[role="alert"]`); err == nil {
		for _, alert := range alerts {
			text, err := alert.Text()
			if err != nil {
				continue
			}
			text = strings.TrimSpace(text)
			if text != "" {
				errors = append(errors, fmt.Sprintf("alert: %s", truncate(text, 200)))
			}
		}
	}

	lowerText := strings.ToLower(pageText)
	keywords := []string{
		"field is required",
		"please fill",
		"please complete",
		"please correct",
		"fix the errors",
		"fix the following",
		"there was a problem submitting",
		"error submitting",
		"submission failed",
		"failed to submit",
	}
	for _, kw := range keywords {
		if strings.Contains(lowerText, kw) {
			errors = append(errors, fmt.Sprintf("error text: %s", kw))
		}
	}

	return errors
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
