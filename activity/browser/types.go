package browser

import "github.com/SomtoJF/iris-worker/browserfactory"

type OpenWebpageInput struct {
	Url        string `json:"url"`
	WorkflowID string `json:"workflow_id"`
}

type TakeScreenshotInput struct {
	WorkflowID string `json:"workflow_id"`
	FileName   string `json:"file_name"`
}

type TakeScreenshotOutput struct {
	Path        string                                   `json:"path"`
	TaggedNodes []browserfactory.SerializableTaggedNode `json:"tagged_nodes"`
}

type ClickInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
}

type TypeInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
	Text         string `json:"text"`
}

type FieldInput struct {
	ElementIndex int    `json:"element_index"`
	Text         string `json:"text"`
}

type TypeMultipleInput struct {
	WorkflowID string       `json:"workflow_id"`
	Fields     []FieldInput `json:"fields"`
}

type ScrollInput struct {
	WorkflowID string  `json:"workflow_id"`
	Direction  string  `json:"direction"` // "up" or "down"
	Ratio      float64 `json:"ratio"`     // 0.1 to 1.0
}

type NavigateInput struct {
	WorkflowID string `json:"workflow_id"`
	Url        string `json:"url"`
}

type ClosePageInput struct {
	WorkflowID string `json:"workflow_id"`
}
