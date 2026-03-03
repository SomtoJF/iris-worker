package browser

import (
	"context"
	"fmt"
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

	screenshotPath, taggedNodes, err := a.browserFactory.ScreenshotForLLM(page, input.FileName)
	if err != nil {
		return TakeScreenshotOutput{}, err
	}

	serializableNodes := make([]browserfactory.SerializableTaggedNode, len(taggedNodes))
	for i, node := range taggedNodes {
		serializableNodes[i] = node.ToSerializable()
	}

	return TakeScreenshotOutput{
		Path:        screenshotPath,
		TaggedNodes: serializableNodes,
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

	_, taggedNodes, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	if input.ElementIndex < 0 || input.ElementIndex >= len(taggedNodes) {
		return fmt.Errorf("element index %d out of range (0-%d)", input.ElementIndex, len(taggedNodes)-1)
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
		Replace:      input.Replace,
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
	_, taggedNodes, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	if field.ElementIndex < 0 || field.ElementIndex >= len(taggedNodes) {
		return fmt.Errorf("element index %d out of range (0-%d)",
			field.ElementIndex, len(taggedNodes)-1)
	}

	element := taggedNodes[field.ElementIndex].Element
	if element == nil {
		return fmt.Errorf("element at index %d has no DOM element", field.ElementIndex)
	}

	if field.Replace {
		if err := element.Input(""); err != nil {
			return fmt.Errorf("failed to clear field: %w", err)
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

type UploadFileInput struct {
	WorkflowID   string `json:"workflow_id"`
	FilePath     string `json:"file_path"`
	ElementIndex int    `json:"element_index"`
}

func (a *Activity) UploadFile(ctx context.Context, input UploadFileInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	_, taggedNodes, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
	if err != nil {
		return fmt.Errorf("failed to get tagged nodes: %w", err)
	}

	if input.ElementIndex < 0 || input.ElementIndex >= len(taggedNodes) {
		return fmt.Errorf("element index %d out of range (0-%d)", input.ElementIndex, len(taggedNodes)-1)
	}

	element := taggedNodes[input.ElementIndex].Element
	if element == nil {
		return fmt.Errorf("element at index %d has no DOM element", input.ElementIndex)
	}

	err = element.SetFiles([]string{input.FilePath})
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	return nil

}
