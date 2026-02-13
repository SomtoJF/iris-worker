package browserfactory

import (
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type BrowserClient interface {
	GetBrowser() *rod.Browser
	ScreenshotForLLM(*rod.Page, string) (string, []*TaggedAccessibilityNode, error)
	OpenUrl(page *rod.Page, url string) *rod.Page
	OpenPageNewTab(browser *rod.Browser, url string) *rod.Page
}

type TaggedAccessibilityNode struct {
	Node        *proto.AccessibilityAXNode
	Element     *rod.Element
	Bounds      *proto.DOMRect
	Index       int
	Description string
}
