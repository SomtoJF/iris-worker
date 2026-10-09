package rod

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/SomtoJF/iris-worker/browser/types"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const (
	captureWindow = 12 * time.Second
	capturePoll   = 500 * time.Millisecond
	maxBodyBytes  = 8 * 1024
)

var ignoredRequestHosts = []string{
	"google-analytics.com", "googletagmanager.com", "doubleclick.net", "facebook.com",
	"facebook.net", "segment.io", "segment.com", "sentry.io", "mixpanel.com", "amplitude.com",
	"hotjar.com", "clarity.ms", "linkedin.com/px", "bat.bing.com", "fullstory.com",
	"datadoghq.com", "intercom.io", "launchdarkly.com", "newrelic.com", "nr-data.net", "posthog.com",
}

type networkCapture struct {
	mu       sync.Mutex
	requests map[proto.NetworkRequestID]*types.CapturedRequest
	order    []proto.NetworkRequestID
	page     *rod.Page
	cancel   context.CancelFunc
	done     chan struct{}
}

func (c *RodBrowserClient) ClickSubmit(ctx context.Context, id types.ApplicationBrowserID, elementIndex int) (types.SubmissionAttempt, error) {
	s, err := c.get(ctx, id)
	if err != nil {
		return types.SubmissionAttempt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ensureSessionOpen(s); err != nil {
		return types.SubmissionAttempt{}, err
	}
	page := s.page.Context(ctx)
	if len(s.taggedNodes) == 0 {
		_, nodes, inputs, err := screenshotForLLM(page, c.screenshotPath("temp.png"))
		if err != nil {
			return types.SubmissionAttempt{}, fmt.Errorf("get tagged nodes: %w", err)
		}
		s.taggedNodes, s.fileInputs = nodes, inputs
	}
	element, err := findTaggedNode(s.taggedNodes, elementIndex)
	if err != nil {
		return types.SubmissionAttempt{}, err
	}
	element = element.Context(ctx)
	info, err := page.Info()
	if err != nil {
		return types.SubmissionAttempt{}, fmt.Errorf("read pre-submit URL: %w", err)
	}
	beforePages, err := s.browser.Pages()
	if err != nil {
		return types.SubmissionAttempt{}, fmt.Errorf("enumerate tabs before submit: %w", err)
	}
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return types.SubmissionAttempt{}, fmt.Errorf("enable request capture: %w", err)
	}
	capture := newNetworkCapture(page)
	defer capture.stop()
	if err := element.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return types.SubmissionAttempt{}, fmt.Errorf("failed to click submit element: %w", err)
	}
	waitForCapturedResponse(capture)
	afterPages, err := s.browser.Pages()
	if err != nil {
		return types.SubmissionAttempt{}, fmt.Errorf("enumerate tabs after submit: %w", err)
	}
	newTab := len(afterPages) > len(beforePages)
	if newTab {
		newPage := afterPages[len(afterPages)-1]
		if err := newPage.WaitLoad(); err == nil {
			s.page = newPage.Context(context.Background())
			s.taggedNodes = nil
			s.fileInputs = nil
		}
	}
	return types.SubmissionAttempt{BeforeURL: info.URL, NewTabOpened: newTab, Requests: capture.results()}, nil
}

func waitForCapturedResponse(capture *networkCapture) {
	deadline := time.Now().Add(captureWindow)
	for time.Now().Before(deadline) {
		time.Sleep(capturePoll)
		if capture.hasResponse() {
			time.Sleep(time.Second)
			return
		}
	}
}

func newNetworkCapture(page *rod.Page) *networkCapture {
	ctx, cancel := context.WithCancel(page.GetContext())
	capturePage := page.Context(ctx)
	capture := &networkCapture{
		requests: make(map[proto.NetworkRequestID]*types.CapturedRequest),
		page:     capturePage, cancel: cancel, done: make(chan struct{}),
	}
	go func() {
		defer close(capture.done)
		capturePage.EachEvent(
			func(event *proto.NetworkRequestWillBeSent) {
				if !isSubmitShaped(event.Request.Method, event.Request.URL, event.Type) {
					return
				}
				capture.mu.Lock()
				defer capture.mu.Unlock()
				if _, exists := capture.requests[event.RequestID]; !exists {
					capture.requests[event.RequestID] = &types.CapturedRequest{URL: event.Request.URL, Method: event.Request.Method, ResourceType: string(event.Type)}
					capture.order = append(capture.order, event.RequestID)
				}
			},
			func(event *proto.NetworkResponseReceived) {
				capture.mu.Lock()
				if req := capture.requests[event.RequestID]; req != nil {
					req.StatusCode = event.Response.Status
				}
				capture.mu.Unlock()
			},
			func(event *proto.NetworkLoadingFinished) {
				capture.mu.Lock()
				req := capture.requests[event.RequestID]
				capture.mu.Unlock()
				if req == nil {
					return
				}
				body, err := (proto.NetworkGetResponseBody{RequestID: event.RequestID}).Call(capture.page)
				if err == nil {
					capture.mu.Lock()
					req.ResponseBody = truncate(body.Body, maxBodyBytes)
					capture.mu.Unlock()
				}
			},
		)()
	}()
	return capture
}

func (c *networkCapture) hasResponse() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, request := range c.requests {
		if request.StatusCode > 0 {
			return true
		}
	}
	return false
}

func (c *networkCapture) results() []types.CapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]types.CapturedRequest, 0, len(c.order))
	for _, id := range c.order {
		out = append(out, *c.requests[id])
	}
	return out
}

func (c *networkCapture) stop() {
	c.cancel()
	<-c.done
}

func isSubmitShaped(method, requestURL string, resourceType proto.NetworkResourceType) bool {
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
	for _, ignored := range ignoredRequestHosts {
		if strings.Contains(lowerURL, ignored) {
			return false
		}
	}
	return true
}
