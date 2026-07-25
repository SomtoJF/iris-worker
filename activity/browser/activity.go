package browser

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"go.temporal.io/sdk/temporal"
)

type Activity struct {
	browserFactory       browserfactory.BrowserClient
	activeSessions       map[string]*rod.Page
	incognitoContexts    map[string]*rod.Browser
	lastTaggedNodes      map[string][]*browserfactory.TaggedAccessibilityNode
	lastTaggedFileInputs map[string][]*browserfactory.TaggedFileInputNode
	mu                   sync.Mutex
}

func NewActivities(browserFactory browserfactory.BrowserClient) *Activity {
	return &Activity{
		browserFactory:       browserFactory,
		activeSessions:       make(map[string]*rod.Page),
		incognitoContexts:    make(map[string]*rod.Browser),
		lastTaggedNodes:      make(map[string][]*browserfactory.TaggedAccessibilityNode),
		lastTaggedFileInputs: make(map[string][]*browserfactory.TaggedFileInputNode),
	}
}

func (a *Activity) OpenWebpage(ctx context.Context, input OpenWebpageInput) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	incognitoCtx, exists := a.incognitoContexts[input.WorkflowID]
	if !exists {
		browser, err := a.browserFactory.GetBrowser()
		if err != nil {
			return fmt.Errorf("browser unavailable: %w", err)
		}
		incognitoCtx = browser.MustIncognito()
		a.incognitoContexts[input.WorkflowID] = incognitoCtx
	}

	page := a.browserFactory.OpenPageNewTab(incognitoCtx, input.Url)
	a.activeSessions[input.WorkflowID] = page
	delete(a.lastTaggedNodes, input.WorkflowID)
	delete(a.lastTaggedFileInputs, input.WorkflowID)

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

	a.mu.Lock()
	a.lastTaggedNodes[input.WorkflowID] = taggedNodes
	a.lastTaggedFileInputs[input.WorkflowID] = taggedFileInputNodes
	a.mu.Unlock()

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

func (a *Activity) GetBase64Screenshot(ctx context.Context, input GetBase64ScreenshotInput) (string, error) {
	screenshot, err := os.ReadFile(input.Path)
	if err != nil {
		return "", err
	}

	base64Screenshot := base64.StdEncoding.EncodeToString(screenshot)
	return fmt.Sprintf("data:image/jpeg;base64,%s", base64Screenshot), nil
}

func (a *Activity) Click(ctx context.Context, input ClickInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]
	taggedNodes := a.lastTaggedNodes[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}
	if !ctxExists {
		return fmt.Errorf("no incognito context for workflow %s", input.WorkflowID)
	}

	element, err := elementByIndex(taggedNodes, input.ElementIndex)
	if err != nil {
		return err
	}

	pagesBefore := incognitoCtx.MustPages()
	beforeCount := len(pagesBefore)

	if err := clickElement(element); err != nil {
		return fmt.Errorf("failed to click element: %w", err)
	}

	time.Sleep(1000 * time.Millisecond)

	pagesAfter := incognitoCtx.MustPages()
	afterCount := len(pagesAfter)

	if afterCount > beforeCount {
		newPage := pagesAfter[afterCount-1]
		_ = newPage.Timeout(10 * time.Second).WaitLoad()

		a.mu.Lock()
		a.activeSessions[input.WorkflowID] = newPage
		delete(a.lastTaggedNodes, input.WorkflowID)
		delete(a.lastTaggedFileInputs, input.WorkflowID)
		a.mu.Unlock()

		page = newPage
	}

	_ = page.Timeout(3 * time.Second).WaitIdle(time.Second)
	return nil
}

// clickElement clicks without hanging forever on rod's interactable/stable waits
// (common on Ashby pages with continuous layout/network activity).
func clickElement(element *rod.Element) error {
	timed := element.Timeout(8 * time.Second)
	if err := timed.Click(proto.InputMouseButtonLeft, 1); err == nil {
		return nil
	}

	// Fallback: DOM click bypasses WaitInteractable/stable RAF loops.
	_, err := element.Eval(`() => { this.click(); return true; }`)
	return err
}

func (a *Activity) Type(ctx context.Context, input TypeInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	taggedNodes := a.lastTaggedNodes[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	return a.typeSingleField(page, taggedNodes, FieldInput{
		ElementIndex: input.ElementIndex,
		Text:         input.Text,
		Replace:      true,
	})
}

func (a *Activity) TypeMultiple(ctx context.Context, input TypeMultipleInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	taggedNodes := a.lastTaggedNodes[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if len(input.Fields) == 0 {
		return nil
	}

	var errorMessages []string
	plannerMistake := false
	for i, field := range input.Fields {
		field.Replace = true
		if err := a.typeSingleField(page, taggedNodes, field); err != nil {
			errorMessages = append(errorMessages,
				fmt.Sprintf("field %d (index %d): %s", i, field.ElementIndex, err.Error()))
			if isPlannerElementMistake(err) {
				plannerMistake = true
			}
			continue
		}

		if i < len(input.Fields)-1 {
			time.Sleep(150 * time.Millisecond)
		}
	}

	if len(errorMessages) > 0 {
		msg := fmt.Sprintf("failed to type %d/%d fields: %v",
			len(errorMessages), len(input.Fields), errorMessages)
		if plannerMistake {
			return temporal.NewNonRetryableApplicationError(msg, "ElementNotEditable", nil)
		}
		return fmt.Errorf("%s", msg)
	}

	return nil
}

func (a *Activity) typeSingleField(page *rod.Page, taggedNodes []*browserfactory.TaggedAccessibilityNode, field FieldInput) error {
	element, err := elementByIndex(taggedNodes, field.ElementIndex)
	if err != nil {
		return err
	}

	editable, err := elementIsEditable(element)
	if err != nil {
		return fmt.Errorf("failed to inspect element %d: %w", field.ElementIndex, err)
	}
	if !editable {
		role := ""
		for _, node := range taggedNodes {
			if node != nil && node.Index == field.ElementIndex {
				role = node.Role
				break
			}
		}
		return fmt.Errorf("element index %d is not editable (role=%q tag); use click for buttons/radios/checkboxes",
			field.ElementIndex, role)
	}

	if field.Replace {
		if err := clearElementText(element); err != nil {
			return fmt.Errorf("failed to clear text: %w", err)
		}
	}
	// Bound interactable waits — continuous layout/network activity can stall Input forever.
	if err := element.Timeout(8 * time.Second).Input(field.Text); err != nil {
		// Fallback: set value via JS for stubborn controls.
		_, evalErr := element.Eval(`(text) => {
			const el = this;
			el.focus();
			if ('value' in el) {
				el.value = text;
				el.dispatchEvent(new Event('input', { bubbles: true }));
				el.dispatchEvent(new Event('change', { bubbles: true }));
				return 'value';
			}
			if (el.isContentEditable) {
				el.textContent = text;
				el.dispatchEvent(new Event('input', { bubbles: true }));
				return 'contenteditable';
			}
			return 'noop';
		}`, field.Text)
		if evalErr != nil {
			return fmt.Errorf("failed to type text: %w (js fallback: %v)", err, evalErr)
		}
	}

	// Bound idle wait — Ashby/recaptcha pages often never go fully idle.
	_ = page.Timeout(3 * time.Second).WaitIdle(time.Second)
	return nil
}

func isPlannerElementMistake(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "is not editable") ||
		strings.Contains(msg, "not found among")
}

func elementIsEditable(element *rod.Element) (bool, error) {
	res, err := element.Eval(`() => {
		const el = this;
		const tag = (el.tagName || '').toLowerCase();
		if (el.isContentEditable) return true;
		if (tag === 'textarea') return true;
		if (tag === 'input') {
			const type = (el.getAttribute('type') || 'text').toLowerCase();
			return !['button','submit','reset','checkbox','radio','file','image','hidden'].includes(type);
		}
		if ('value' in el && (tag === 'select' || el.getAttribute('role') === 'textbox')) return true;
		return false;
	}`)
	if err != nil {
		return false, err
	}
	return res.Value.Bool(), nil
}

// clearElementText clears an editable control before replace-typing.
// rod's SelectAllText uses HTMLInputElement.select(), which throws on
// contenteditable nodes and some custom widgets.
func clearElementText(element *rod.Element) error {
	_, err := element.Eval(`() => {
		const el = this;
		if (typeof el.select === 'function') {
			el.focus();
			el.select();
			return 'select';
		}
		if (el.isContentEditable) {
			el.focus();
			const range = document.createRange();
			range.selectNodeContents(el);
			const sel = window.getSelection();
			sel.removeAllRanges();
			sel.addRange(range);
			return 'contenteditable';
		}
		if ('value' in el) {
			el.focus();
			el.value = '';
			el.dispatchEvent(new Event('input', { bubbles: true }));
			el.dispatchEvent(new Event('change', { bubbles: true }));
			return 'value';
		}
		return 'noop';
	}`)
	if err != nil {
		return err
	}
	return nil
}

// elementByIndex finds a tagged node by its painted Index label, not by slice
// position. Index values are assigned at tag time and must not be assumed to
// equal 0..len(taggedNodes)-1 when resolving planner element_index values.
func elementByIndex(taggedNodes []*browserfactory.TaggedAccessibilityNode, elementIndex int) (*rod.Element, error) {
	if len(taggedNodes) == 0 {
		return nil, fmt.Errorf("no tagged nodes cached for this page; call TakeScreenshot before interacting")
	}
	for _, node := range taggedNodes {
		if node == nil || node.Index != elementIndex {
			continue
		}
		if node.Element == nil {
			return nil, fmt.Errorf("element at index %d has no DOM element", elementIndex)
		}
		return node.Element, nil
	}

	indices := make([]int, 0, len(taggedNodes))
	for _, node := range taggedNodes {
		if node != nil {
			indices = append(indices, node.Index)
		}
	}
	return nil, fmt.Errorf("element index %d not found among %d tagged nodes (indices=%v)",
		elementIndex, len(taggedNodes), indices)
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

	_ = page.Timeout(3 * time.Second).WaitIdle(time.Second)
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
	page, pageExists := a.activeSessions[input.WorkflowID]
	incognitoCtx, ctxExists := a.incognitoContexts[input.WorkflowID]
	delete(a.activeSessions, input.WorkflowID)
	delete(a.incognitoContexts, input.WorkflowID)
	delete(a.lastTaggedNodes, input.WorkflowID)
	delete(a.lastTaggedFileInputs, input.WorkflowID)
	a.mu.Unlock()

	if !pageExists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	// Best-effort: a hung CDP socket must not block other activities.
	if err := page.Close(); err != nil {
		slog.Warn("failed to close page", "workflowID", input.WorkflowID, "error", err)
	}
	if ctxExists {
		if err := incognitoCtx.Close(); err != nil {
			slog.Warn("failed to close incognito context", "workflowID", input.WorkflowID, "error", err)
		}
	}

	return nil
}

func (a *Activity) resolveElement(page *rod.Page, elementIndex int) (*rod.Element, error) {
	// Prefer the tagged set from the last TakeScreenshot for this page so
	// planner element_index values match the painted labels the LLM saw.
	a.mu.Lock()
	var taggedNodes []*browserfactory.TaggedAccessibilityNode
	for workflowID, p := range a.activeSessions {
		if p == page {
			taggedNodes = a.lastTaggedNodes[workflowID]
			break
		}
	}
	a.mu.Unlock()

	if len(taggedNodes) == 0 {
		_, retagged, _, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
		if err != nil {
			return nil, fmt.Errorf("failed to get tagged nodes: %w", err)
		}
		taggedNodes = retagged
	}

	return elementByIndex(taggedNodes, elementIndex)
}

func (a *Activity) handleNewTab(workflowID string, incognitoCtx *rod.Browser, beforeCount int) bool {
	pagesAfter := incognitoCtx.MustPages()
	afterCount := len(pagesAfter)

	if afterCount > beforeCount {
		newPage := pagesAfter[afterCount-1]
		newPage.MustWaitLoad()

		a.mu.Lock()
		a.activeSessions[workflowID] = newPage
		delete(a.lastTaggedNodes, workflowID)
		delete(a.lastTaggedFileInputs, workflowID)
		a.mu.Unlock()

		return true
	}
	return false
}

type UploadFileInput struct {
	WorkflowID     string `json:"workflow_id"`
	FilePath       string `json:"file_path"`
	FileInputIndex int    `json:"file_input_index"`
}

func (a *Activity) UploadFile(ctx context.Context, input UploadFileInput) error {
	a.mu.Lock()
	page, exists := a.activeSessions[input.WorkflowID]
	taggedFileInputNodes := a.lastTaggedFileInputs[input.WorkflowID]
	a.mu.Unlock()

	if !exists {
		return fmt.Errorf("no active page for workflow %s", input.WorkflowID)
	}

	if len(taggedFileInputNodes) == 0 {
		_, _, retagged, err := a.browserFactory.ScreenshotForLLM(page, "temp.png")
		if err != nil {
			return fmt.Errorf("failed to get tagged nodes: %w", err)
		}
		taggedFileInputNodes = retagged
	}

	element, err := fileInputByIndex(taggedFileInputNodes, input.FileInputIndex)
	if err != nil {
		return err
	}

	err = element.SetFiles([]string{input.FilePath})
	if err != nil {
		return fmt.Errorf("failed to upload file: %w", err)
	}

	return nil
}

func fileInputByIndex(taggedFileInputNodes []*browserfactory.TaggedFileInputNode, fileInputIndex int) (*rod.Element, error) {
	for _, node := range taggedFileInputNodes {
		if node == nil || node.Index != fileInputIndex {
			continue
		}
		if node.Element == nil {
			return nil, fmt.Errorf("file input at index %d has no DOM element", fileInputIndex)
		}
		return node.Element, nil
	}

	indices := make([]int, 0, len(taggedFileInputNodes))
	for _, node := range taggedFileInputNodes {
		if node != nil {
			indices = append(indices, node.Index)
		}
	}
	return nil, fmt.Errorf("file input index %d not found among %d tagged file inputs (indices=%v)",
		fileInputIndex, len(taggedFileInputNodes), indices)
}
