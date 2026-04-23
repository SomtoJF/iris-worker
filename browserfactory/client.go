package browserfactory

import (
	"fmt"
	"strings"

	"github.com/SomtoJF/iris-worker/initializers/fs"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

const existingSessionPort = "37712" // UserMode default port; connect here if launch fails

type BrowserFactory struct {
	browser *rod.Browser
	fs      *fs.TemporaryFileSystem
}

// NewBrowserFactory tries to launch a new browser first; on failure it tries to connect
// to an existing session on port 37712 (e.g. Chrome started with --remote-debugging-port=37712).
func NewBrowserFactory(fs *fs.TemporaryFileSystem) (*BrowserFactory, error) {
	// 1. Try to launch a new browser (default launcher, fresh instance).
	u, launchErr := launcher.New().Launch()
	if launchErr == nil {
		browser := rod.New().ControlURL(u)
		if err := browser.Connect(); err != nil {
			return nil, fmt.Errorf("launched browser but failed to connect: %w", err)
		}
		return &BrowserFactory{browser: browser.NoDefaultDevice(), fs: fs}, nil
	}

	// 2. Fallback: connect to existing session (e.g. user started Chrome with --remote-debugging-port=37712).
	existingURL, resolveErr := launcher.ResolveURL(existingSessionPort)
	if resolveErr != nil {
		return nil, fmt.Errorf("failed to launch browser: %w; failed to connect to existing session on port %s: %w", launchErr, existingSessionPort, resolveErr)
	}
	browser := rod.New().ControlURL(existingURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("failed to launch browser: %w; existing session on port %s unreachable: %w", launchErr, existingSessionPort, err)
	}
	return &BrowserFactory{browser: browser.NoDefaultDevice(), fs: fs}, nil
}

func (b *BrowserFactory) GetBrowser() *rod.Browser {
	return b.browser
}

func (b *BrowserFactory) ScreenshotForLLM(page *rod.Page, fileName string) (string, []*TaggedAccessibilityNode, []*TaggedFileInputNode, error) {
	screenshotPath := b.fs.ConcatenatePath(fileName)

	var taggedNodes []*TaggedAccessibilityNode
	var taggedFileInputNodes []*TaggedFileInputNode

	err := rod.Try(func() {
		page.MustWaitLoad()

		// Get the accessibility tree for the page
		accessibilityTree, _ := getPageAccessibilityTree(page)

		fileInputElements, err := getFileInputElements(page)
		if err != nil {
			panic(err)
		}

		// Draw transparent grid lines over the page
		drawTransparentGrid(page)

		taggedNodes = tagAccessibilityNodes(page, accessibilityTree)

		page.MustScreenshot(screenshotPath)

		// Remove the grid and tags from the page to avoid accumulation
		page.MustEval(`() => {
			document.getElementById('agent-grid')?.remove();
			document.querySelectorAll('.agent-tag').forEach(el => el.remove());
		}`)

		taggedFileInputNodes = tagFileInputNodes(page, fileInputElements)

	})

	if err != nil {
		return "", nil, nil, err
	}

	return screenshotPath, taggedNodes, taggedFileInputNodes, nil
}

func (b *BrowserFactory) OpenUrl(page *rod.Page, url string) *rod.Page {
	return page.MustNavigate(url)
}

func (b *BrowserFactory) OpenPageNewTab(browser *rod.Browser, url string) *rod.Page {
	page := stealth.MustPage(browser).MustWindowFullscreen()
	page.MustNavigate(url)
	return page
}

func getPageAccessibilityTree(page *rod.Page) ([]*proto.AccessibilityAXNode, error) {
	res, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return nil, err
	}
	return res.Nodes, nil
}

func getFileInputElements(page *rod.Page) ([]*rod.Element, error) {
	page.MustWaitStable()
	fileInputs, err := page.Elements("input[type='file']")
	if err != nil {
		return nil, err
	}

	return fileInputs, nil
}

// getLabelHTMLForInput returns the inner HTML of the <label> whose "for"
// attribute matches the given file input's id, or "" if none.
func getLabelHTMLForInput(page *rod.Page, input *rod.Element) string {
	id, err := input.Attribute("id")
	if err != nil || id == nil || *id == "" {
		return ""
	}
	labels, err := page.Elements("label")
	if err != nil || len(labels) == 0 {
		return ""
	}
	for _, label := range labels {
		forAttr, err := label.Attribute("for")
		if err != nil || forAttr == nil {
			continue
		}
		if *forAttr == *id {
			html, _ := label.HTML()
			return html
		}
	}
	return ""
}

func tagFileInputNodes(page *rod.Page, fileInputNodes []*rod.Element) []*TaggedFileInputNode {
	var taggedFileInputNodes []*TaggedFileInputNode
	for i, node := range fileInputNodes {
		labelHTML := getLabelHTMLForInput(page, node)
		var labelPtr *string
		if labelHTML != "" {
			labelPtr = &labelHTML
		}
		html, _ := node.HTML()
		value, _ := node.Attribute("value")
		var valuePtr *string
		if value != nil {
			valuePtr = value
		}
		taggedFileInputNodes = append(taggedFileInputNodes, &TaggedFileInputNode{
			Index: i, HTML: html, Label: labelPtr, Value: valuePtr, Element: node,
		})
	}
	return taggedFileInputNodes
}

func drawTransparentGrid(page *rod.Page) {
	page.MustEval(`() => {
		const canvas = document.createElement('canvas');
		canvas.id = 'agent-grid';
		canvas.style = 'position:fixed; top:0; left:0; pointer-events:none; z-index:9999;';
		canvas.width = window.innerWidth;
		canvas.height = window.innerHeight;
		const ctx = canvas.getContext('2d');
		ctx.strokeStyle = 'rgba(255, 0, 0, 0.2)'; // Faint red lines
		// Draw horizontal/vertical lines every 100px
		for(let i=0; i<canvas.width; i+=100) { ctx.strokeRect(i, 0, 0, canvas.height); }
		for(let i=0; i<canvas.height; i+=100) { ctx.strokeRect(0, i, canvas.width, 0); }
		document.body.appendChild(canvas);
	}`)
}

func tagAccessibilityNodes(page *rod.Page, accessibilityTree []*proto.AccessibilityAXNode) []*TaggedAccessibilityNode {
	// Filter for focusable nodes with valid BackendDOMNodeID
	var focusableNodes []*proto.AccessibilityAXNode
	for _, node := range accessibilityTree {
		if !node.Ignored && isInteractive(node) && node.BackendDOMNodeID != 0 {
			focusableNodes = append(focusableNodes, node)
		}
	}

	var taggedNodes []*TaggedAccessibilityNode

	// Inject tagging script for each focusable element
	for i, node := range focusableNodes {
		bounds := getNodeBounds(page, node)
		if bounds != nil {
			page.MustEval(`(x, y, w, h, i) => {
				const tag = document.createElement('div');
				tag.className = 'agent-tag';
				tag.innerText = i;
				tag.style = `+"`"+`
					position: fixed;
					left: ${x}px;
					top: ${y}px;
					background: #ff0000;
					color: white;
					padding: 2px 4px;
					font-size: 10px;
					font-weight: bold;
					border-radius: 3px;
					z-index: 1000000;
					pointer-events: none;
				`+"`"+`;
				document.body.appendChild(tag);
			}`, bounds.X, bounds.Y, bounds.Width, bounds.Height, i)
		} else {
			continue
		}

		element := getElementFromNode(page, node)
		description := getDescriptionFromNode(node, i, element)
		role := getNodeRole(node, element)
		value := getNodeValue(node, element)

		interactiveFormRoles := map[string]struct{}{
			"checkbox":  {},
			"radio":     {},
			"switch":    {},
			"textbox":   {},
			"searchbox": {},
			"textarea":  {},
			"select":    {},
			"combobox":  {},
		}

		var requiredPtr *bool
		if _, ok := interactiveFormRoles[role]; ok {
			required := isNodeRequired(node, element)
			requiredPtr = &required
		}
		var checkedPtr *string

		if role == "checkbox" || role == "radio" || role == "switch" {
			if checked := getCheckboxCheckedState(node); checked != "" {
				checkedPtr = &checked
			}
		}

		taggedNodes = append(taggedNodes, &TaggedAccessibilityNode{
			Node:        node,
			Element:     element,
			Bounds:      bounds,
			Index:       i,
			Description: description,
			Role:        role,
			Value:       &value,
			Required:    requiredPtr,
			Checked:     checkedPtr,
		})
	}

	return taggedNodes
}

func getNodeRole(node *proto.AccessibilityAXNode, element *rod.Element) string {
	role := ""
	if node.Role != nil && !node.Role.Value.Nil() {
		role = node.Role.Value.String()
	}
	return role
}

func getNodeValue(node *proto.AccessibilityAXNode, element *rod.Element) string {
	role := ""
	if node.Role != nil && !node.Role.Value.Nil() {
		role = node.Role.Value.String()
	}

	value := ""
	if v := node.Value; v != nil && !v.Value.Nil() {
		value = v.Value.String()
	} else {
		if role == "combobox" && element != nil {
			reactVal, err := getReactSelectComboboxValue(element)
			if err == nil && reactVal != "" {
				value = reactVal
			}
		}
	}
	return value
}

func isNodeRequired(node *proto.AccessibilityAXNode, element *rod.Element) bool {
	if element != nil {
		// HTML boolean attribute: presence means true.
		if v, err := element.Attribute("required"); err == nil && v != nil {
			return true
		}

		// ARIA attribute: explicit boolean string.
		if v, err := element.Attribute("aria-required"); err == nil && v != nil {
			switch strings.ToLower(strings.TrimSpace(*v)) {
			case "true", "1":
				return true
			case "false", "0":
				return false
			}
		}
	}

	// Fallback to AX properties if present.
	if node.Properties != nil {
		for _, prop := range node.Properties {
			if prop.Name == "required" && prop.Value != nil {
				return prop.Value.Value.Bool()
			}
		}
	}
	return false
}

func getDescriptionFromNode(node *proto.AccessibilityAXNode, index int, element *rod.Element) string {
	name := ""
	if node.Name != nil && !node.Name.Value.Nil() {
		name = node.Name.Value.String()
	}
	role := ""
	if node.Role != nil && !node.Role.Value.Nil() {
		role = node.Role.Value.String()
	}

	// Default description for non-file inputs
	desc := fmt.Sprintf("Tag %d: Role: %s - Name: %s", index, strings.ToUpper(role), name)

	return desc
}

func getCheckboxCheckedState(node *proto.AccessibilityAXNode) string {
	for _, prop := range node.Properties {
		if prop.Name == "checked" && prop.Value != nil {
			return prop.Value.Value.String()
		}
	}
	return ""
}

func getReactSelectComboboxValue(element *rod.Element) (string, error) {
	// We start from the input and look for the "Value Container" sibling
	val, err := element.Eval(`() => {
        const container = this.closest('.select__control');
        if (!container) return "NOT_FOUND";
        
        // Look for the specific class Greenhouse uses for the selected text
        const displayValue = container.querySelector('[class*="single-value"]');
        return displayValue ? displayValue.innerText : "";
    }`)

	if err != nil {
		return "", err
	}
	return val.Value.String(), nil
}

// isInteractive checks if node has interactive role
func isInteractive(node *proto.AccessibilityAXNode) bool {
	// Check focusable property first
	// if node.Properties != nil {
	// 	for _, prop := range node.Properties {
	// 		if prop.Name == "focusable" && prop.Value != nil {
	// 			if prop.Value.Value.Bool() {
	// 				return true
	// 			}
	// 		}
	// 	}
	// }

	interactiveRoles := map[string]bool{
		"button":    true,
		"link":      true,
		"textbox":   true,
		"checkbox":  true,
		"radio":     true,
		"combobox":  true,
		"menuitem":  true,
		"searchbox": true,
		"switch":    true,
		"slider":    true,
		"tab":       true,
		"option":    true,
		"select":    true,
		"label":     true,
		"textarea":  true,
		"input":     true,
	}

	uninteractiveRoles := map[string]bool{
		"rootwebarea": true,
	}

	// Fallback: check for interactive role
	if node.Role != nil && !node.Role.Value.Nil() {
		roleValue := strings.ToLower(node.Role.Value.String())

		if uninteractiveRoles[roleValue] {
			return false
		}

		return interactiveRoles[roleValue]
	}

	return false
}

// getNodeBounds retrieves element bounds using DOM.getBoxModel
func getNodeBounds(page *rod.Page, node *proto.AccessibilityAXNode) *proto.DOMRect {
	if node.BackendDOMNodeID == 0 {
		return nil
	}

	res, err := proto.DOMGetBoxModel{BackendNodeID: node.BackendDOMNodeID}.Call(page)
	if err != nil || res.Model == nil || len(res.Model.Border) < 4 {
		return nil
	}

	// Model.Border is [x1, y1, x2, y2, x3, y3, x4, y4] - use top-left corner
	x := res.Model.Border[0]
	y := res.Model.Border[1]
	// Calculate width/height from quad points
	width := res.Model.Border[2] - res.Model.Border[0]  // x2 - x1
	height := res.Model.Border[5] - res.Model.Border[1] // y3 - y1

	return &proto.DOMRect{
		X:      x,
		Y:      y,
		Width:  width,
		Height: height,
	}
}

func getElementFromNode(page *rod.Page, node *proto.AccessibilityAXNode) *rod.Element {
	if node.BackendDOMNodeID == 0 {
		return nil
	}

	el, err := page.ElementFromNode(&proto.DOMNode{
		BackendNodeID: node.BackendDOMNodeID,
	})
	if err != nil {
		return nil
	}

	return el
}
