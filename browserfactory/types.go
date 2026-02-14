package browserfactory

import (
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

type BrowserClient interface {
	GetBrowser() *rod.Browser
	ScreenshotForLLM(*rod.Page, string) (string, []*TaggedAccessibilityNode, error)
	OpenPageNewTab(browser *rod.Browser, url string) *rod.Page
}

type TaggedAccessibilityNode struct {
	Node        *proto.AccessibilityAXNode
	Element     *rod.Element
	Bounds      *proto.DOMRect
	Index       int
	Description string
}

// SerializableTaggedNode is a JSON-serializable version of TaggedAccessibilityNode
// Used to pass tagged nodes between Temporal activities
type SerializableTaggedNode struct {
	Index       int     `json:"index"`
	Description string  `json:"description"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
}

// ToSerializable converts TaggedAccessibilityNode to SerializableTaggedNode
func (t *TaggedAccessibilityNode) ToSerializable() SerializableTaggedNode {
	return SerializableTaggedNode{
		Index:       t.Index,
		Description: t.Description,
		X:           t.Bounds.X,
		Y:           t.Bounds.Y,
		Width:       t.Bounds.Width,
		Height:      t.Bounds.Height,
	}
}
