package browserfactory

import (
	"fmt"
	"strings"

	"github.com/SomtoJF/iris-worker/initializers/fs"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type BrowserFactory struct {
	browser *rod.Browser
	fs      *fs.TemporaryFileSystem
}

func NewBrowserFactory(fs *fs.TemporaryFileSystem) *BrowserFactory {
	return &BrowserFactory{
		browser: rod.New().MustConnect().NoDefaultDevice(),
		fs:      fs,
	}
}

func (b *BrowserFactory) GetBrowser() *rod.Browser {
	return b.browser
}

func (b *BrowserFactory) ScreenshotForLLM(page *rod.Page, fileName string) (string, []*TaggedAccessibilityNode, error) {
	screenshotPath := b.fs.ConcatenatePath(fileName)

	var taggedNodes []*TaggedAccessibilityNode

	err := rod.Try(func() {
		page.MustWaitStable()
		// Get the accessibility tree for the page
		accessibilityTree, _ := getPageAccessibilityTree(page)

		// Draw transparent grid lines over the page
		drawTransparentGrid(page)

		taggedNodes = tagAccessibilityNodes(page, accessibilityTree)

		page.MustScreenshot(screenshotPath)

	})

	if err != nil {
		return "", nil, err
	}

	return screenshotPath, taggedNodes, nil
}

func (b *BrowserFactory) OpenUrl(page *rod.Page, url string) *rod.Page {
	return page.MustNavigate(url)
}

func (b *BrowserFactory) OpenPageNewTab(browser *rod.Browser, url string) *rod.Page {
	return browser.MustPage(url).MustWindowFullscreen()
}

func getPageAccessibilityTree(page *rod.Page) ([]*proto.AccessibilityAXNode, error) {
	res, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		return nil, err
	}
	return res.Nodes, nil
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
		description := getDescriptionFromNode(node, i)

		fmt.Println("description: ", description)

		taggedNodes = append(taggedNodes, &TaggedAccessibilityNode{
			Node:        node,
			Element:     element,
			Bounds:      bounds,
			Index:       i,
			Description: description,
		})
	}

	return taggedNodes
}

func getDescriptionFromNode(node *proto.AccessibilityAXNode, index int) string {
	name := ""
	if node.Name != nil && !node.Name.Value.Nil() {
		name = node.Name.Value.String()
	}
	role := ""
	if node.Role != nil && !node.Role.Value.Nil() {
		role = node.Role.Value.String()
	}
	value := ""
	if v := node.Value; v != nil && !v.Value.Nil() {
		value = v.Value.String()
	}

	desc := fmt.Sprintf("Tag %d: %s %s", index, name, role)
	if value != "" {
		desc += fmt.Sprintf(" with value %s", value)
	}
	return desc
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
