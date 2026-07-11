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

type GetPageTextInput struct {
	WorkflowID string `json:"workflow_id"`
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

type CapturedRequest struct {
	URL          string `json:"url"`
	Method       string `json:"method"`
	ResourceType string `json:"resource_type"`
	StatusCode   int    `json:"status_code"`
	ResponseBody string `json:"response_body"`
}

type ClickSubmitInput struct {
	WorkflowID   string `json:"workflow_id"`
	ElementIndex int    `json:"element_index"`
}

type ClickSubmitOutput struct {
	BeforeURL    string            `json:"before_url"`
	NewTabOpened bool              `json:"new_tab_opened"`
	Requests     []CapturedRequest `json:"requests"`
}

type VerifySubmissionStateInput struct {
	WorkflowID string `json:"workflow_id"`
	BeforeURL  string `json:"before_url"`
}

type VerifySubmissionStateOutput struct {
	CurrentURL       string   `json:"current_url"`
	URLChanged       bool     `json:"url_changed"`
	FormPresent      bool     `json:"form_present"`
	SuccessText      string   `json:"success_text"`
	ValidationErrors []string `json:"validation_errors"`
	PageText         string   `json:"page_text"`
}

// Captcha types. Type values returned by DetectCaptcha.
const (
	CaptchaTypeNone        = "none"
	CaptchaTypeRecaptchaV2 = "recaptcha_v2"
	CaptchaTypeRecaptchaV3 = "recaptcha_v3"
	CaptchaTypeTurnstile   = "turnstile"
)

type DetectCaptchaInput struct {
	WorkflowID string `json:"workflow_id"`
}

type DetectCaptchaOutput struct {
	Type      string            `json:"type"`       // one of CaptchaType* constants
	SiteKey   string            `json:"site_key"`   // empty when Type == none
	PageURL   string            `json:"page_url"`   // URL to submit to the solver
	Action    string            `json:"action"`     // reCAPTCHA v3 action (best-effort)
	Invisible bool              `json:"invisible"`  // v2 invisible / v3
	Extra     map[string]string `json:"extra"`      // turnstile cData etc.
}

type InjectCaptchaTokenInput struct {
	WorkflowID string `json:"workflow_id"`
	Type       string `json:"type"`
	Token      string `json:"token"`
}

type InjectCaptchaTokenOutput struct {
	CallbackFired bool `json:"callback_fired"`
}

type ClickCaptchaButtonInput struct {
	WorkflowID string `json:"workflow_id"`
	Selector   string `json:"selector"` // CSS selector; searched on page and inside iframes
}

type ClickCaptchaButtonOutput struct {
	Clicked bool `json:"clicked"`
}
