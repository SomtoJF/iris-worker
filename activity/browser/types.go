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
	Path                 string                                           `json:"path"`
	TaggedNodes          []browserfactory.SerializableTaggedNode          `json:"tagged_nodes"`
	TaggedFileInputNodes []browserfactory.SerializableTaggedFileInputNode `json:"tagged_file_input_nodes"`
}

type GetBase64ScreenshotInput struct {
	Path string `json:"path"`
}

type ClickInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
}

type TypeInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
	Text         string `json:"text"`
	Replace      bool   `json:"replace"`
}

type FieldInput struct {
	ElementIndex int    `json:"element_index"`
	Text         string `json:"text"`
	Replace      bool   `json:"replace"`
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

type GetFormActionInput struct {
	WorkflowID string `json:"workflow_id"`
}

type GetFormActionOutput struct {
	Action     string `json:"action"`
	HasAction  bool   `json:"has_action"`
	CurrentURL string `json:"current_url"`
}

type HijackSubmitClickInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
	ActionURL    string `json:"action_url"`
}

type HijackSubmitClickOutput struct {
	StatusCode   int    `json:"status_code"`
	ResponseBody string `json:"response_body"`
	TimedOut     bool   `json:"timed_out"`
}

type CheckSubmissionFallbackInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
	BeforeURL    string `json:"before_url"`
	SkipClick    bool   `json:"skip_click"`
}

type CheckSubmissionFallbackOutput struct {
	Submitted       bool   `json:"submitted"`
	DetectionMethod string `json:"detection_method"`
	Message         string `json:"message"`
}
