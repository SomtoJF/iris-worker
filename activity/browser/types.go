package browser

import (
	"time"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
)

type SerializableTaggedNode struct {
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

type SerializableTaggedFileInputNode struct {
	Index int     `json:"index"`
	HTML  string  `json:"html,omitempty"`
	Name  string  `json:"name,omitempty"`
	Label *string `json:"label,omitempty"`
	Value *string `json:"value,omitempty"`
}

type OpenWebpageInput struct {
	Url        string `json:"url"`
	WorkflowID string `json:"workflow_id"`
}

type OpenWebpageOutput struct {
	ReplayRequired bool `json:"replay_required"`
}

type TakeScreenshotInput struct {
	WorkflowID string `json:"workflow_id"`
	FileName   string `json:"file_name"`
}

type TakeScreenshotOutput struct {
	Path                 string                            `json:"path"`
	CurrentURL           string                            `json:"current_url,omitempty"`
	HasVisibleAlerts     bool                              `json:"has_visible_alerts"`
	TaggedNodes          []SerializableTaggedNode          `json:"tagged_nodes"`
	TaggedFileInputNodes []SerializableTaggedFileInputNode `json:"tagged_file_input_nodes"`
}

type ListBrowserMutationsForReplayInput struct {
	WorkflowID string `json:"workflow_id"`
}

type ReplayMutationRef struct {
	IdBrowserMutationChangelog uint                         `json:"id_browser_mutation_changelog"`
	Operation                  string                       `json:"operation"`
	Context                    sqldb.BrowserMutationContext `json:"context"`
	Status                     sqldb.BrowserMutationStatus  `json:"status"`
	CreatedAt                  time.Time                    `json:"created_at"`
}

type ListBrowserMutationsForReplayOutput struct {
	Provider         string              `json:"provider"`
	ReplayGeneration uint64              `json:"replay_generation"`
	Mutations        []ReplayMutationRef `json:"mutations"`
}

type ReplayBrowserMutationInput struct {
	WorkflowID string `json:"workflow_id"`
	MutationID uint   `json:"mutation_id"`
}

type CompleteBrowserReplayInput struct {
	WorkflowID string `json:"workflow_id"`
}

type GetBase64ScreenshotInput struct {
	Path string `json:"path"`
}

type ClickInput struct {
	WorkflowID   string                       `json:"workflow_id"`
	ElementIndex int                          `json:"element_index"`
	Target       *sqldb.BrowserMutationTarget `json:"target,omitempty"`
}

type TypeInput struct {
	WorkflowID   string                       `json:"workflow_id"`
	ElementIndex int                          `json:"element_index"`
	Text         string                       `json:"text"`
	Replace      bool                         `json:"replace"`
	Target       *sqldb.BrowserMutationTarget `json:"target,omitempty"`
}

type FieldInput struct {
	ElementIndex int                          `json:"element_index"`
	Text         string                       `json:"text"`
	Replace      bool                         `json:"replace"`
	Target       *sqldb.BrowserMutationTarget `json:"target,omitempty"`
}

type TypeMultipleInput struct {
	WorkflowID string       `json:"workflow_id"`
	Fields     []FieldInput `json:"fields"`
}

type SecureTypeInput struct {
	ActionID     string                       `json:"action_id"`
	Operation    string                       `json:"operation"`
	Ciphertext   []byte                       `json:"ciphertext"`
	WorkflowID   string                       `json:"workflow_id,omitempty"`
	ElementIndex int                          `json:"element_index,omitempty"`
	ValueIndex   int                          `json:"value_index,omitempty"`
	Target       *sqldb.BrowserMutationTarget `json:"target,omitempty"`
}

type ScrollInput struct {
	WorkflowID string  `json:"workflow_id"`
	Direction  string  `json:"direction"`
	Ratio      float64 `json:"ratio"`
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

const (
	CaptchaTypeNone        = "none"
	CaptchaTypeRecaptchaV2 = "recaptcha_v2"
	CaptchaTypeRecaptchaV3 = "recaptcha_v3"
	CaptchaTypeTurnstile   = "turnstile"
	CaptchaTypeHcaptcha    = "hcaptcha"
)

type DetectCaptchaInput struct {
	WorkflowID string `json:"workflow_id"`
}

type DetectCaptchaOutput struct {
	Type      string            `json:"type"`
	SiteKey   string            `json:"site_key"`
	PageURL   string            `json:"page_url"`
	Action    string            `json:"action"`
	Invisible bool              `json:"invisible"`
	Extra     map[string]string `json:"extra"`
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
	Selector   string `json:"selector"`
}

type ClickCaptchaButtonOutput struct {
	Clicked bool `json:"clicked"`
}

type ScrapeRenderedPageInput struct {
	WorkflowID string `json:"workflow_id"`
}

type ScrapeRenderedPageOutput struct {
	Data string `json:"data"`
}
