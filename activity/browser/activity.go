package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type Activity struct {
	browserFactory    browserfactory.BrowserClient
	activeSessions    map[string]*rod.Page
	incognitoContexts map[string]*rod.Browser
	mu                sync.Mutex
}

func NewActivities(browserFactory browserfactory.BrowserClient) *Activity {
	return &Activity{
		browserFactory:    browserFactory,
		activeSessions:    make(map[string]*rod.Page),
		incognitoContexts: make(map[string]*rod.Browser),
	}
}

func (a *Activity) OpenWebpage(ctx context.Context, input OpenWebpageInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	incognitoCtx, exists := a.incognitoContexts[input.WorkflowID]
	if !exists {
		incognitoCtx = a.browserFactory.GetBrowser().MustIncognito()
		a.incognitoContexts[input.WorkflowID] = incognitoCtx
	}

	page := a.browserFactory.OpenPageNewTab(incognitoCtx, input.Url)
	a.activeSessions[input.WorkflowID] = page

	return nil
}

func (a *Activity) TakeScreenshot(ctx context.Context, input TakeScreenshotInput) (TakeScreenshotOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return TakeScreenshotOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	screenshotPath, taggedNodes, taggedFileInputNodes, err := a.browserFactory.ScreenshotForLLM(page, input.FileName)
	if err != nil {
		return TakeScreenshotOutput{}, err
	}

	serializableNodes := make([]browserfactory.SerializableTaggedNode, len(taggedNodes))
	for i, node := range taggedNodes {
		serializableNodes[i] = node.ToSerializable()
	}

	serializableFileInputNodes := make([]browserfactory.SerializableTaggedFileInputNode, len(taggedFileInputNodes))
	for i, node := range taggedFileInputNodes {
		serializableFileInputNodes[i] = node.ToSerializable()
	}

	return TakeScreenshotOutput{
		Path:                 screenshotPath,
		TaggedNodes:          serializableNodes,
		TaggedFileInputNodes: serializableFileInputNodes,
	}, nil
}

func (a *Activity) Click(ctx context.Context, input ClickInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}
	if !ctxExists {
		return fmt.Errorf("no incognito context for workflow %s", input.WorkflowID)
	}

	_, taggedNodes, _, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	element := taggedNodes[input.ElementIndex].Element
	if element == nil {
		return fmt.Errorf("element at index %d has no DOM element", input.ElementIndex)
	}

	pagesBefore := incognitoCtx.MustPages()
	beforeCount := len(pagesBefore)

	err = element.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return fmt.Errorf("failed to click element: %w", err)
	}

	time.Sleep(1000 * time.Millisecond)

	pagesAfter := incognitoCtx.MustPages()
	afterCount := len(pagesAfter)

	if afterCount > beforeCount {
		newPage := pagesAfter[afterCount-1]
		newPage.MustWaitLoad()

		a.mu.Lock()
		a.activeSessions[input.WorkflowID] = newPage
		a.mu.Unlock()

		page = newPage
	}

	page.MustWaitIdle()
	return nil
}

func (a *Activity) Type(ctx context.Context, input TypeInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	return a.typeSingleField(page, FieldInput{
		ElementIndex: input.ElementIndex,
		Text:         input.Text,
		Replace:      true,
	})
}

func (a *Activity) TypeMultiple(ctx context.Context, input TypeMultipleInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if len(input.Fields) == 0 {
		return nil
	}

	var errorMessages []string
	for i, field := range input.Fields {
		// Always replace current text with new text
		field.Replace = true
		if err := a.typeSingleField(page, field); err != nil {
			errorMessages = append(errorMessages,
				fmt.Sprintf("field %d (index %d): %s", i, field.ElementIndex, err.Error()))
			continue
		}

		if i < len(input.Fields)-1 {
			time.Sleep(150 * time.Millisecond)
		}
	}

	if len(errorMessages) > 0 {
		return fmt.Errorf("failed to type %d/%d fields: %v",
			len(errorMessages), len(input.Fields), errorMessages)
	}

	return nil
}

func (a *Activity) typeSingleField(page *rod.Page, field FieldInput) error {
	_, taggedNodes, _, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	// if field.ElementIndex < 0 || field.ElementIndex >= len(taggedNodes) {
	// 	return fmt.Errorf("element index %d out of range (0-%d)",
	// 		field.ElementIndex, len(taggedNodes)-1)
	// }

	element := taggedNodes[field.ElementIndex].Element
	if element == nil {
		return fmt.Errorf("element at index %d has no DOM element", field.ElementIndex)
	}

	if field.Replace {
		if err := element.SelectAllText(); err != nil {
			return fmt.Errorf("failed to select all text: %w", err)
		}
	}
	if err := element.Input(field.Text); err != nil {
		return fmt.Errorf("failed to type text: %w", err)
	}

	page.MustWaitIdle()
	return nil
}

func (a *Activity) Scroll(ctx context.Context, input ScrollInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if input.Ratio < 0.1 || input.Ratio > 1.0 {
		return fmt.Errorf("scroll ratio must be between 0.1 and 1.0, got %f", input.Ratio)
	}

	multiplier := 1.0
	if input.Direction == "up" {
		multiplier = -1.0
	} else if input.Direction != "down" {
		return fmt.Errorf("scroll direction must be 'up' or 'down', got %s", input.Direction)
	}

	_, err := page.Eval(`(ratio, mult) => {
		const amount = window.innerHeight * ratio * mult;
		window.scrollBy({
			top: amount,
			behavior: 'instant'
		});
	}`, input.Ratio, multiplier)

	if err != nil {
		return fmt.Errorf("failed to scroll: %w", err)
	}

	page.MustWaitIdle()
	return nil
}

func (a *Activity) Navigate(ctx context.Context, input NavigateInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	err := page.Navigate(input.Url)
	if err != nil {
		return fmt.Errorf("failed to navigate to %s: %w", input.Url, err)
	}

	page.MustWaitLoad()
	return nil
}

func (a *Activity) ClosePage(ctx context.Context, input ClosePageInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	page, pageExists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]

	if pageExists {
		delete(a.activeSessions, input.WorkflowID)
	}
	if ctxExists {
		delete(a.incognitoContexts, input.WorkflowID)
	}

	if !pageExists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if err := page.Close(); err != nil {
		return fmt.Errorf("failed to close page: %w", err)
	}

	if ctxExists {
		if err := incognitoCtx.Close(); err != nil {
			return fmt.Errorf("failed to close incognito context: %w", err)
		}
	}

	return nil
}

func (a *Activity) GetFormAction(ctx context.Context, input GetFormActionInput) (GetFormActionOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return GetFormActionOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	currentURL := page.MustInfo().URL

	form, err := page.Timeout(5 * time.Second).Element("form")
	if err != nil {
		return GetFormActionOutput{CurrentURL: currentURL}, nil
	}
	if form == nil {
		return GetFormActionOutput{CurrentURL: currentURL}, nil
	}

	actionAttr, err := form.Attribute("action")
	if err != nil || actionAttr == nil || *actionAttr == "" {
		return GetFormActionOutput{CurrentURL: currentURL}, nil
	}

	action := resolveActionURL(currentURL, *actionAttr)

	return GetFormActionOutput{
		Action:     action,
		HasAction:  true,
		CurrentURL: currentURL,
	}, nil
}

func resolveActionURL(pageURL string, action string) string {
	if strings.HasPrefix(action, "http://") || strings.HasPrefix(action, "https://") {
		return action
	}
	base, err := url.Parse(pageURL)
	if err != nil {
		return action
	}
	ref, err := url.Parse(action)
	if err != nil {
		return action
	}
	return base.ResolveReference(ref).String()
}

func (a *Activity) HijackSubmitClick(ctx context.Context, input HijackSubmitClickInput) (HijackSubmitClickOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return HijackSubmitClickOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}
	if !ctxExists {
		return HijackSubmitClickOutput{}, fmt.Errorf("no incognito context for workflow %s", input.WorkflowID)
	}

	element, err := a.resolveElement(page, input.ElementIndex)
	if err != nil {
		return HijackSubmitClickOutput{}, err
	}

	type hijackResult struct {
		statusCode   int
		responseBody string
	}
	resultCh := make(chan hijackResult, 1)

	router := page.HijackRequests()
	err = router.Add(input.ActionURL, "", func(h *rod.Hijack) {
		err := h.LoadResponse(&http.Client{}, true)
		if err != nil {
			h.Response.SetBody([]byte(""))
		}
		statusCode := h.Response.Payload().ResponseCode
		body := string(h.Response.Payload().Body)

		select {
		case resultCh <- hijackResult{statusCode: statusCode, responseBody: body}:
		default:
		}
	})
	if err != nil {
		return HijackSubmitClickOutput{}, fmt.Errorf("failed to set up hijack: %w", err)
	}

	go router.Run()
	defer router.Stop()

	pagesBefore := incognitoCtx.MustPages()
	beforeCount := len(pagesBefore)

	err = element.Click(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return HijackSubmitClickOutput{}, fmt.Errorf("failed to click element: %w", err)
	}

	time.Sleep(1 * time.Second)

	a.handleNewTab(input.WorkflowID, incognitoCtx, beforeCount)

	select {
	case result := <-resultCh:
		return HijackSubmitClickOutput{
			StatusCode:   result.statusCode,
			ResponseBody: result.responseBody,
		}, nil
	case <-time.After(15 * time.Second):
		return HijackSubmitClickOutput{TimedOut: true}, nil
	}
}

func (a *Activity) CheckSubmissionFallback(ctx context.Context, input CheckSubmissionFallbackInput) (CheckSubmissionFallbackOutput, error) {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return CheckSubmissionFallbackOutput{}, fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}
	if !ctxExists {
		return CheckSubmissionFallbackOutput{}, fmt.Errorf("no incognito context for workflow %s", input.WorkflowID)
	}

	pagesBefore := incognitoCtx.MustPages()
	beforeCount := len(pagesBefore)
	newTabOpened := false

	if !input.SkipClick {
		element, err := a.resolveElement(page, input.ElementIndex)
		if err != nil {
			return CheckSubmissionFallbackOutput{}, err
		}

		err = element.Click(proto.InputMouseButtonLeft, 1)
		if err != nil {
			return CheckSubmissionFallbackOutput{}, fmt.Errorf("failed to click element: %w", err)
		}

		time.Sleep(3 * time.Second)

		newTabOpened = a.handleNewTab(input.WorkflowID, incognitoCtx, beforeCount)
	}

	// Re-read active page (may have changed if new tab)
	a.mu.Lock()
	page = a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	page.MustWaitIdle()

	// Check 1: Navigation detection (URL change or new tab)
	if newTabOpened {
		return CheckSubmissionFallbackOutput{
			Submitted:       true,
			DetectionMethod: "url_navigation",
			Message:         "New tab opened after submit",
		}, nil
	}

	currentURL := page.MustInfo().URL
	if currentURL != input.BeforeURL {
		return CheckSubmissionFallbackOutput{
			Submitted:       true,
			DetectionMethod: "url_navigation",
			Message:         fmt.Sprintf("URL changed to %s", currentURL),
		}, nil
	}

	// Check 2: Form absence
	forms, err := page.Elements("form")
	if err == nil && len(forms) == 0 {
		return CheckSubmissionFallbackOutput{
			Submitted:       true,
			DetectionMethod: "form_absence",
			Message:         "Form elements no longer present on page",
		}, nil
	}

	// Check 3: Success element
	if submitted, msg := checkSuccessText(page); submitted {
		return CheckSubmissionFallbackOutput{
			Submitted:       true,
			DetectionMethod: "success_element",
			Message:         msg,
		}, nil
	}

	return CheckSubmissionFallbackOutput{
		Submitted:       false,
		DetectionMethod: "none",
		Message:         "No submission confirmation detected",
	}, nil
}

func (a *Activity) resolveElement(page *rod.Page, elementIndex int) (*rod.Element, error) {
	_, taggedNodes, _, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return nil, fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	// if elementIndex < 0 || elementIndex >= len(taggedNodes) {
	// 	return nil, fmt.Errorf("element index %d out of range (0-%d)", elementIndex, len(taggedNodes)-1)
	// }

	element := taggedNodes[elementIndex].Element
	if element == nil {
		return nil, fmt.Errorf("element at index %d has no DOM element", elementIndex)
	}

	return element, nil
}

func (a *Activity) handleNewTab(workflowID string, incognitoCtx *rod.Browser, beforeCount int) bool {
	pagesAfter := incognitoCtx.MustPages()
	afterCount := len(pagesAfter)

	if afterCount > beforeCount {
		newPage := pagesAfter[afterCount-1]
		newPage.MustWaitLoad()

		a.mu.Lock()
		a.activeSessions[workflowID] = newPage
		a.mu.Unlock()

		return true
	}
	return false
}

func checkSuccessText(page *rod.Page) (bool, string) {
	body, err := page.Element("body")
	if err != nil {
		return false, ""
	}
	text, err := body.Text()
	if err != nil {
		return false, ""
	}

	lowerText := strings.ToLower(text)
	keywords := []string{
		"application submitted",
		"received your application",
		"thank you for applying",
		"thank you for your application",
		"successfully submitted",
		"application complete",
		"we have received",
	}

	for _, kw := range keywords {
		if strings.Contains(lowerText, kw) {
			return true, fmt.Sprintf("Found success text: %s", kw)
		}
	}
	return false, ""
}

type UploadFileInput struct {
	WorkflowID     string `json:"workflow_id"`
	FilePath       string `json:"file_path"`
	FileInputIndex int    `json:"file_input_index"`
}

func (a *Activity) UploadFile(ctx context.Context, input UploadFileInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	_, _, taggedFileInputNodes, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	if input.FileInputIndex < 0 || input.FileInputIndex >= len(taggedFileInputNodes) {
		return fmt.Errorf("element index %d out of range (0-%d)", input.FileInputIndex, len(taggedFileInputNodes)-1)
	}

	element := taggedFileInputNodes[input.FileInputIndex].Element
	if element == nil {
		return fmt.Errorf("element at index %d has no DOM element", input.FileInputIndex)
	}

	err = element.SetFiles([]string{input.FilePath})
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	return nil

}
