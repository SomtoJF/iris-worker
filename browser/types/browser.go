package types

type ApplicationBrowserID string

type MutationNotExecutedError struct {
	err error
}

func (e MutationNotExecutedError) Error() string {
	return e.err.Error()
}

func (e MutationNotExecutedError) Unwrap() error {
	return e.err
}

func MutationNotExecuted(err error) error {
	if err == nil {
		return nil
	}
	return MutationNotExecutedError{err: err}
}

type BrowserOptions struct {
	StartingURL string `json:"starting_url"`
}

type TaggedNode struct {
	Index       int     `json:"index"`
	Description string  `json:"description"`
	Name        string  `json:"name,omitempty"`
	Label       string  `json:"label,omitempty"`
	Selector    string  `json:"selector,omitempty"`
	Submit      bool    `json:"submit,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Width       float64 `json:"width"`
	Height      float64 `json:"height"`
	Role        string  `json:"role"`
	Value       *string `json:"value"`
	Required    *bool   `json:"required"`
	Checked     *string `json:"checked"`
}

type TaggedFileInput struct {
	Index int     `json:"index"`
	HTML  string  `json:"html"`
	Name  string  `json:"name,omitempty"`
	Label *string `json:"label"`
	Value *string `json:"value"`
}

type Screenshot struct {
	Path                 string            `json:"path"`
	CurrentURL           string            `json:"current_url,omitempty"`
	HasVisibleAlerts     bool              `json:"has_visible_alerts"`
	TaggedNodes          []TaggedNode      `json:"tagged_nodes"`
	TaggedFileInputNodes []TaggedFileInput `json:"tagged_file_input_nodes"`
}

type FieldInput struct {
	ElementIndex int    `json:"element_index"`
	Text         string `json:"text"`
	Replace      bool   `json:"replace"`
}

type Captcha struct {
	Type      string            `json:"type"`
	SiteKey   string            `json:"site_key"`
	PageURL   string            `json:"page_url"`
	Action    string            `json:"action"`
	Invisible bool              `json:"invisible"`
	Extra     map[string]string `json:"extra"`
}

type CaptchaResult struct {
	CallbackFired bool `json:"callback_fired"`
}

type CapturedRequest struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	ResourceType string `json:"resource_type"`
	StatusCode   int    `json:"status_code"`
	ResponseBody string `json:"response_body"`
}

type SubmissionAttempt struct {
	BeforeURL    string            `json:"before_url"`
	NewTabOpened bool              `json:"new_tab_opened"`
	Requests     []CapturedRequest `json:"requests"`
}

type SubmissionState struct {
	CurrentURL       string   `json:"current_url"`
	URLChanged       bool     `json:"url_changed"`
	FormPresent      bool     `json:"form_present"`
	SuccessText      string   `json:"success_text"`
	ValidationErrors []string `json:"validation_errors"`
	PageText         string   `json:"page_text"`
}

const (
	BrowserProviderRod    = "rod"
	BrowserProviderKernel = "kernel"
)
