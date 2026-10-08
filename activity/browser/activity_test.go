package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	browserclient "github.com/SomtoJF/iris-worker/browser"
	browsertype "github.com/SomtoJF/iris-worker/browser/types"
)

type browserCall struct {
	method string
	id     browsertype.ApplicationBrowserID
	args   []any
}

type fakeBrowserClient struct {
	calls         []browserCall
	operationErr  error
	typeErrors    []error
	typeFields    []browsertype.FieldInput
	startOpts     browsertype.BrowserOptions
	screenshot    browsertype.Screenshot
	captcha       browsertype.Captcha
	captchaResult browsertype.CaptchaResult
	clicked       bool
	scrapeText    string
	attempt       browsertype.SubmissionAttempt
	state         browsertype.SubmissionState
}

var _ browserclient.BrowserClient = (*fakeBrowserClient)(nil)

func (f *fakeBrowserClient) GetBrowserProvider() string { return "fake" }

func (f *fakeBrowserClient) record(method string, id browsertype.ApplicationBrowserID, args ...any) {
	f.calls = append(f.calls, browserCall{method: method, id: id, args: args})
}

func (f *fakeBrowserClient) StartBrowser(_ context.Context, id browsertype.ApplicationBrowserID, opts browsertype.BrowserOptions) error {
	f.record("start", id, opts)
	f.startOpts = opts
	return f.operationErr
}

func (f *fakeBrowserClient) Navigate(_ context.Context, id browsertype.ApplicationBrowserID, url string) error {
	f.record("navigate", id, url)
	return f.operationErr
}

func (f *fakeBrowserClient) ScreenshotForLLM(_ context.Context, id browsertype.ApplicationBrowserID, fileName string) (browsertype.Screenshot, error) {
	f.record("screenshot", id, fileName)
	return f.screenshot, f.operationErr
}

func (f *fakeBrowserClient) Click(_ context.Context, id browsertype.ApplicationBrowserID, elementIndex int) error {
	f.record("click", id, elementIndex)
	return f.operationErr
}

func (f *fakeBrowserClient) Type(_ context.Context, id browsertype.ApplicationBrowserID, field browsertype.FieldInput) error {
	f.record("type", id, field)
	f.typeFields = append(f.typeFields, field)
	if len(f.typeErrors) > 0 {
		err := f.typeErrors[0]
		f.typeErrors = f.typeErrors[1:]
		return err
	}
	return f.operationErr
}

func (f *fakeBrowserClient) Scroll(_ context.Context, id browsertype.ApplicationBrowserID, direction string, ratio float64) error {
	f.record("scroll", id, direction, ratio)
	return f.operationErr
}

func (f *fakeBrowserClient) UploadFile(_ context.Context, id browsertype.ApplicationBrowserID, fileInputIndex int, filePath string) error {
	f.record("upload", id, fileInputIndex, filePath)
	return f.operationErr
}

func (f *fakeBrowserClient) ScrapeRenderedPage(_ context.Context, id browsertype.ApplicationBrowserID) (string, error) {
	f.record("scrape", id)
	return f.scrapeText, f.operationErr
}

func (f *fakeBrowserClient) DetectCaptcha(_ context.Context, id browsertype.ApplicationBrowserID) (browsertype.Captcha, error) {
	f.record("detect-captcha", id)
	return f.captcha, f.operationErr
}

func (f *fakeBrowserClient) InjectCaptchaToken(_ context.Context, id browsertype.ApplicationBrowserID, captchaType, token string) (browsertype.CaptchaResult, error) {
	f.record("inject-captcha", id, captchaType, token)
	return f.captchaResult, f.operationErr
}

func (f *fakeBrowserClient) ClickCaptchaButton(_ context.Context, id browsertype.ApplicationBrowserID, selector string) (bool, error) {
	f.record("click-captcha-button", id, selector)
	return f.clicked, f.operationErr
}

func (f *fakeBrowserClient) ClickSubmit(_ context.Context, id browsertype.ApplicationBrowserID, elementIndex int) (browsertype.SubmissionAttempt, error) {
	f.record("click-submit", id, elementIndex)
	return f.attempt, f.operationErr
}

func (f *fakeBrowserClient) VerifySubmission(_ context.Context, id browsertype.ApplicationBrowserID, beforeURL string) (browsertype.SubmissionState, error) {
	f.record("verify-submission", id, beforeURL)
	return f.state, f.operationErr
}

func (f *fakeBrowserClient) CloseBrowser(_ context.Context, id browsertype.ApplicationBrowserID) error {
	f.record("close", id)
	return f.operationErr
}

func TestActivityAdaptersForwardIDsAndMapDTOs(t *testing.T) {
	ctx := context.Background()
	client := &fakeBrowserClient{
		screenshot: browsertype.Screenshot{
			Path:                 "shot.png",
			TaggedNodes:          []browsertype.TaggedNode{{Index: 3, Description: "name", X: 1, Y: 2, Width: 3, Height: 4, Role: "textbox", Value: stringPointer("v"), Required: boolPointer(true), Checked: stringPointer("mixed")}},
			TaggedFileInputNodes: []browsertype.TaggedFileInput{{Index: 5, HTML: "<input>", Label: stringPointer("resume"), Value: stringPointer("/tmp/file")}},
		},
		captcha:       browsertype.Captcha{Type: CaptchaTypeHcaptcha, SiteKey: "site", PageURL: "https://example.test", Action: "submit", Invisible: true, Extra: map[string]string{"cdata": "data"}},
		captchaResult: browsertype.CaptchaResult{CallbackFired: true},
		clicked:       true,
		scrapeText:    "rendered text",
		attempt: browsertype.SubmissionAttempt{
			BeforeURL: "https://example.test/form", NewTabOpened: true,
			Requests: []browsertype.CapturedRequest{{URL: "https://example.test/apply", Method: "POST", ResourceType: "XHR", StatusCode: 201, ResponseBody: "ok"}},
		},
		state: browsertype.SubmissionState{
			CurrentURL: "https://example.test/done", URLChanged: true, FormPresent: false,
			SuccessText: "application received", ValidationErrors: []string{"required"}, PageText: "thanks",
		},
	}
	a := NewActivities(client)
	const workflowID = "wf-forwarding"

	opened, err := a.OpenWebpage(ctx, OpenWebpageInput{WorkflowID: workflowID, Url: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if opened.Provider != "fake" {
		t.Fatalf("browser provider = %q, want configured provider fake", opened.Provider)
	}
	if client.startOpts.StartingURL != "https://example.test" {
		t.Fatalf("starting URL = %q", client.startOpts.StartingURL)
	}

	screenshot, err := a.TakeScreenshot(ctx, TakeScreenshotInput{WorkflowID: workflowID, FileName: "shot.png"})
	if err != nil {
		t.Fatal(err)
	}
	if screenshot.Path != "shot.png" || !reflect.DeepEqual(screenshot.TaggedNodes, []SerializableTaggedNode{{Index: 3, Description: "name", X: 1, Y: 2, Width: 3, Height: 4, Role: "textbox", Value: stringPointer("v"), Required: boolPointer(true), Checked: stringPointer("mixed")}}) {
		t.Fatalf("screenshot DTO was not mapped: %+v", screenshot)
	}
	if !reflect.DeepEqual(screenshot.TaggedFileInputNodes, []SerializableTaggedFileInputNode{{Index: 5, HTML: "<input>", Label: stringPointer("resume"), Value: stringPointer("/tmp/file")}}) {
		t.Fatalf("file-input DTO was not mapped: %+v", screenshot.TaggedFileInputNodes)
	}
	if err := a.Click(ctx, ClickInput{WorkflowID: workflowID, ElementIndex: 3}); err != nil {
		t.Fatal(err)
	}
	if err := a.Type(ctx, TypeInput{WorkflowID: workflowID, ElementIndex: 3, Text: "Jane", Replace: false}); err != nil {
		t.Fatal(err)
	}
	if err := a.TypeMultiple(ctx, TypeMultipleInput{WorkflowID: workflowID, Fields: []FieldInput{{ElementIndex: 4, Text: "A"}, {ElementIndex: 5, Text: "B", Replace: false}}}); err != nil {
		t.Fatal(err)
	}
	if err := a.Scroll(ctx, ScrollInput{WorkflowID: workflowID, Direction: "down", Ratio: 0.5}); err != nil {
		t.Fatal(err)
	}
	if err := a.Navigate(ctx, NavigateInput{WorkflowID: workflowID, Url: "https://example.test/next"}); err != nil {
		t.Fatal(err)
	}
	if err := a.UploadFile(ctx, UploadFileInput{WorkflowID: workflowID, FilePath: "/tmp/resume", FileInputIndex: 5}); err != nil {
		t.Fatal(err)
	}
	captcha, err := a.DetectCaptcha(ctx, DetectCaptchaInput{WorkflowID: workflowID})
	if err != nil || captcha.Type != CaptchaTypeHcaptcha || captcha.Extra["cdata"] != "data" {
		t.Fatalf("captcha mapping = %+v, err = %v", captcha, err)
	}
	injected, err := a.InjectCaptchaToken(ctx, InjectCaptchaTokenInput{WorkflowID: workflowID, Type: captcha.Type, Token: "token"})
	if err != nil || !injected.CallbackFired {
		t.Fatalf("inject result = %+v, err = %v", injected, err)
	}
	clicked, err := a.ClickCaptchaButton(ctx, ClickCaptchaButtonInput{WorkflowID: workflowID, Selector: "button"})
	if err != nil || !clicked.Clicked {
		t.Fatalf("captcha button result = %+v, err = %v", clicked, err)
	}
	scraped, err := a.ScrapeRenderedPage(ctx, ScrapeRenderedPageInput{WorkflowID: workflowID})
	if err != nil || scraped.Data != "rendered text" {
		t.Fatalf("scrape result = %+v, err = %v", scraped, err)
	}
	attempt, err := a.ClickSubmitAndCapture(ctx, ClickSubmitInput{WorkflowID: workflowID, ElementIndex: 9})
	if err != nil || attempt.BeforeURL != "https://example.test/form" || !attempt.NewTabOpened || !reflect.DeepEqual(attempt.Requests, []CapturedRequest{{URL: "https://example.test/apply", Method: "POST", ResourceType: "XHR", StatusCode: 201, ResponseBody: "ok"}}) {
		t.Fatalf("submit mapping = %+v, err = %v", attempt, err)
	}
	state, err := a.VerifySubmissionState(ctx, VerifySubmissionStateInput{WorkflowID: workflowID, BeforeURL: "https://example.test/form"})
	if err != nil || state.CurrentURL != "https://example.test/done" || !state.URLChanged || state.SuccessText != "application received" || !reflect.DeepEqual(state.ValidationErrors, []string{"required"}) {
		t.Fatalf("submission state mapping = %+v, err = %v", state, err)
	}
	if err := a.ClosePage(ctx, ClosePageInput{WorkflowID: workflowID}); err != nil {
		t.Fatal(err)
	}
	if err := a.ClosePage(ctx, ClosePageInput{WorkflowID: workflowID}); err != nil {
		t.Fatalf("repeated ClosePage should be idempotent: %v", err)
	}

	wantCalls := []browserCall{
		{method: "start", args: []any{browsertype.BrowserOptions{StartingURL: "https://example.test"}}},
		{method: "screenshot", args: []any{"shot.png"}},
		{method: "click", args: []any{3}},
		{method: "type", args: []any{browsertype.FieldInput{ElementIndex: 3, Text: "Jane"}}},
		{method: "type", args: []any{browsertype.FieldInput{ElementIndex: 4, Text: "A", Replace: true}}},
		{method: "type", args: []any{browsertype.FieldInput{ElementIndex: 5, Text: "B", Replace: true}}},
		{method: "scroll", args: []any{"down", 0.5}},
		{method: "navigate", args: []any{"https://example.test/next"}},
		{method: "upload", args: []any{5, "/tmp/resume"}},
		{method: "detect-captcha"},
		{method: "inject-captcha", args: []any{CaptchaTypeHcaptcha, "token"}},
		{method: "click-captcha-button", args: []any{"button"}},
		{method: "scrape"},
		{method: "click-submit", args: []any{9}},
		{method: "verify-submission", args: []any{"https://example.test/form"}},
		{method: "close"},
		{method: "close"},
	}
	if len(client.calls) != len(wantCalls) {
		t.Fatalf("calls = %+v", client.calls)
	}
	for i, call := range client.calls {
		want := wantCalls[i]
		if call.method != want.method || call.id != browsertype.ApplicationBrowserID(workflowID) || !reflect.DeepEqual(call.args, want.args) {
			t.Errorf("call %d = %+v, want %+v with application ID %q", i, call, want, workflowID)
		}
	}
	if len(client.typeFields) != 3 || client.typeFields[0].Replace || !client.typeFields[1].Replace || !client.typeFields[2].Replace {
		t.Fatalf("field mapping/TypeMultiple replace semantics = %+v", client.typeFields)
	}
}

func TestTypeMultiplePreservesAggregationAndPlannerClassification(t *testing.T) {
	backendErr := errors.New("provider failure")
	plannerErr := errors.New("element index 7 is not editable")
	client := &fakeBrowserClient{typeErrors: []error{backendErr, plannerErr, nil}}
	err := NewActivities(client).TypeMultiple(context.Background(), TypeMultipleInput{
		WorkflowID: "wf-types",
		Fields:     []FieldInput{{ElementIndex: 2, Text: "a"}, {ElementIndex: 7, Text: "b"}, {ElementIndex: 8, Text: "c"}},
	})
	if err == nil || !strings.Contains(err.Error(), "failed to type 2/3 fields") || !strings.Contains(err.Error(), "provider failure") || !strings.Contains(err.Error(), "not editable") {
		t.Fatalf("aggregated error = %v", err)
	}
	var applicationError interface {
		Type() string
		NonRetryable() bool
	}
	if !errors.As(err, &applicationError) || applicationError.Type() != "ElementNotEditable" || !applicationError.NonRetryable() {
		t.Fatalf("planner error classification = %T %v", err, err)
	}
	if len(client.typeFields) != 3 {
		t.Fatalf("continued after field errors: got %d type calls", len(client.typeFields))
	}

	empty := &fakeBrowserClient{}
	if err := NewActivities(empty).TypeMultiple(context.Background(), TypeMultipleInput{WorkflowID: "wf-empty"}); err != nil || len(empty.calls) != 0 {
		t.Fatalf("empty TypeMultiple = %v, calls %v", err, empty.calls)
	}
}

func TestActivityReturnsBrowserOperationErrors(t *testing.T) {
	wantErr := errors.New("browser operation failed")
	client := &fakeBrowserClient{operationErr: wantErr}
	err := NewActivities(client).Click(context.Background(), ClickInput{WorkflowID: "wf-error", ElementIndex: 1})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Click error = %v, want original error", err)
	}
}

func TestResolveTaggedTargetUsesUniqueSemanticMatch(t *testing.T) {
	nodes := []browsertype.TaggedNode{
		{Index: 8, Role: "textbox", Name: "Email"},
		{Index: 9, Role: "button", Name: "Continue"},
	}
	index, err := resolveTaggedTarget(nodes, &sqldb.BrowserMutationTarget{Role: "textbox", Name: "Email"})
	if err != nil {
		t.Fatalf("resolve semantic target: %v", err)
	}
	if index != 8 {
		t.Fatalf("resolved index = %d, want fresh index 8", index)
	}
}

func TestResolveTaggedTargetRejectsMissingOrAmbiguousMatches(t *testing.T) {
	nodes := []browsertype.TaggedNode{
		{Index: 1, Role: "button", Name: "Continue"},
		{Index: 4, Role: "button", Name: "Continue"},
	}
	for _, target := range []*sqldb.BrowserMutationTarget{
		{Role: "button", Name: "Missing"},
		{Role: "button", Name: "Continue"},
	} {
		if _, err := resolveTaggedTarget(nodes, target); err == nil {
			t.Fatalf("target %+v unexpectedly resolved", target)
		}
	}
}

func TestReplaySafetyRequiresSemanticNonSecretTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		target *sqldb.BrowserMutationTarget
		want   bool
	}{
		{name: "stable textbox", target: &sqldb.BrowserMutationTarget{Role: "textbox", Name: "Email"}, want: true},
		{name: "password", target: &sqldb.BrowserMutationTarget{Role: "password", Name: "Password"}},
		{name: "submit", target: &sqldb.BrowserMutationTarget{Role: "button", Name: "Submit application", Submit: true}},
		{name: "missing semantics", target: &sqldb.BrowserMutationTarget{Role: "button"}},
		{name: "missing target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isReplaySafeTarget("input_text", test.target); got != test.want {
				t.Fatalf("isReplaySafeTarget = %t, want %t", got, test.want)
			}
		})
	}
}

func TestReconcileBrowserMutationRecognizesObservableEffects(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		arguments  string
		context    sqldb.BrowserMutationContext
		screenshot browsertype.Screenshot
	}{
		{
			name:       "navigation",
			operation:  "navigate",
			arguments:  `{"url":"https://example.test/done"}`,
			screenshot: browsertype.Screenshot{CurrentURL: "https://example.test/done"},
		},
		{
			name:      "text input",
			operation: "input_text",
			arguments: `{"text":"user@example.test"}`,
			context: sqldb.BrowserMutationContext{
				Target: &sqldb.BrowserMutationTarget{Role: "textbox", Name: "Email"},
			},
			screenshot: browsertype.Screenshot{TaggedNodes: []browsertype.TaggedNode{
				{Index: 4, Role: "textbox", Name: "Email", Value: stringPointer("user@example.test")},
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity := NewActivities(&fakeBrowserClient{screenshot: test.screenshot})
			applied, err := activity.reconcileBrowserMutation(
				context.Background(),
				"workflow-id",
				test.operation,
				sqldb.BrowserMutationChangelog{Context: test.context},
				[]byte(test.arguments),
			)
			if err != nil || !applied {
				t.Fatalf("reconciliation = (%t, %v), want (true, nil)", applied, err)
			}
		})
	}
}

func TestReconcileBrowserMutationLeavesUnobservableOutcomesUncertain(t *testing.T) {
	activity := NewActivities(&fakeBrowserClient{})
	applied, err := activity.reconcileBrowserMutation(
		context.Background(),
		"workflow-id",
		"click",
		sqldb.BrowserMutationChangelog{},
		[]byte(`{"element_index":1}`),
	)
	if err != nil || applied {
		t.Fatalf("reconciliation = (%t, %v), want (false, nil)", applied, err)
	}
}

func TestGetBase64ScreenshotFilesystemBehavior(t *testing.T) {
	path := t.TempDir() + "/shot.jpg"
	if err := os.WriteFile(path, []byte("image-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := NewActivities(nil).GetBase64Screenshot(context.Background(), GetBase64ScreenshotInput{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	want := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("image-bytes"))
	if got != want {
		t.Fatalf("base64 screenshot = %q, want %q", got, want)
	}
	if _, err := NewActivities(nil).GetBase64Screenshot(context.Background(), GetBase64ScreenshotInput{Path: path + ".missing"}); err == nil {
		t.Fatal("expected filesystem read error")
	}
}

func stringPointer(value string) *string { return &value }
func boolPointer(value bool) *bool       { return &value }
