package jobapplication

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	browseractivity "github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestMatchingReplayTargetUsesFreshSemanticTags(t *testing.T) {
	target := &sqldb.BrowserMutationTarget{Role: "textbox", Name: "Email"}
	nodes := []browseractivity.SerializableTaggedNode{
		{Index: 6, Role: "button", Name: "Continue"},
		{Index: 11, Role: "textbox", Name: "Email"},
	}
	got := matchingReplayTargetIndices(nodes, nil, target)
	if len(got) != 1 || got[0] != 11 {
		t.Fatalf("semantic matches = %v, want [11]", got)
	}
}

func TestMatchingReplayTargetRejectsAmbiguityAndSubmitDrift(t *testing.T) {
	target := &sqldb.BrowserMutationTarget{Role: "button", Name: "Continue"}
	nodes := []browseractivity.SerializableTaggedNode{
		{Index: 1, Role: "button", Name: "Continue"},
		{Index: 2, Role: "button", Name: "Continue"},
	}
	if got := matchingReplayTargetIndices(nodes, nil, target); len(got) != 2 {
		t.Fatalf("ambiguous matches = %v, want 2", got)
	}
	nodes[1].Submit = true
	if got := matchingReplayTargetIndices(nodes[1:], nil, target); len(got) != 0 {
		t.Fatalf("submit target mismatch = %v, want no matches", got)
	}
}

func TestSafeReplayStateOmitsFieldValuesAndFileHTML(t *testing.T) {
	screenshot := browseractivity.TakeScreenshotOutput{
		TaggedNodes: []browseractivity.SerializableTaggedNode{{
			Index: 1, Description: "Email", Name: "Email", Role: "textbox",
			Value: replayStringPointer("sensitive-user-value"),
		}},
		TaggedFileInputNodes: []browseractivity.SerializableTaggedFileInputNode{{
			Index: 2, HTML: `<input value="sensitive-file-value">`, Name: "resume",
			Label: replayStringPointer("Resume"), Value: replayStringPointer("sensitive-file-value"),
		}},
	}
	state, err := safeReplayState(screenshot)
	if err != nil {
		t.Fatalf("safeReplayState: %v", err)
	}
	for _, secret := range []string{"sensitive-user-value", "sensitive-file-value", "<input"} {
		if strings.Contains(state, secret) {
			t.Fatalf("serialized JEV state contains %q: %s", secret, state)
		}
	}
	if !strings.Contains(state, "Email") || !strings.Contains(state, "Resume") {
		t.Fatalf("serialized JEV state omitted semantic accessibility data: %s", state)
	}
}

func replayStringPointer(value string) *string { return &value }

func TestRandomReplayPacingSecondsWithinRequestedBounds(t *testing.T) {
	for i := 0; i < 1000; i++ {
		if seconds := randomReplayPacingSeconds(); seconds < 1 || seconds > 5 {
			t.Fatalf("random replay pause = %d seconds, want 1..5", seconds)
		}
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return waitReplayPacingInterval(ctx)
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("Temporal-compatible replay wait: %v", err)
	}
}

func TestReplayBrowserMutationsRunsCatchUpAndFinalVerification(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	var sequence []string
	var jevRequest types.JevRequest
	env.RegisterActivityWithOptions(func(_ context.Context, _ browseractivity.ListBrowserMutationsForReplayInput) (browseractivity.ListBrowserMutationsForReplayOutput, error) {
		sequence = append(sequence, "list")
		return browseractivity.ListBrowserMutationsForReplayOutput{
			Provider:         "rod",
			ReplayGeneration: 3,
			Mutations: []browseractivity.ReplayMutationRef{{
				IdBrowserMutationChangelog: 17,
				Operation:                  "input_text",
				Context: sqldb.BrowserMutationContext{
					Target:       &sqldb.BrowserMutationTarget{Role: "textbox", Name: "Email"},
					ElementIndex: intPointer(4),
					ReplaySafe:   true,
				},
				Status: sqldb.BrowserMutationApplied,
			}},
		}, nil
	}, activity.RegisterOptions{Name: "ListBrowserMutationsForReplay"})
	env.RegisterActivityWithOptions(func(_ context.Context, _ browseractivity.TakeScreenshotInput) (browseractivity.TakeScreenshotOutput, error) {
		sequence = append(sequence, "screenshot")
		return browseractivity.TakeScreenshotOutput{
			CurrentURL: "https://example.test/apply",
			TaggedNodes: []browseractivity.SerializableTaggedNode{{
				Index: 4, Role: "textbox", Name: "Email", Value: replayStringPointer("filled"),
			}},
		}, nil
	}, activity.RegisterOptions{Name: "TakeScreenshot"})
	env.RegisterActivityWithOptions(func(_ context.Context, _ browseractivity.ReplayBrowserMutationInput) error {
		sequence = append(sequence, "replay")
		return nil
	}, activity.RegisterOptions{Name: "ReplayBrowserMutation"})
	env.RegisterActivityWithOptions(func(_ context.Context, _ browseractivity.DetectCaptchaInput) (browseractivity.DetectCaptchaOutput, error) {
		sequence = append(sequence, "captcha")
		return browseractivity.DetectCaptchaOutput{Type: browseractivity.CaptchaTypeNone}, nil
	}, activity.RegisterOptions{Name: "DetectCaptcha"})
	env.RegisterActivityWithOptions(func(_ context.Context, request types.JevRequest) (types.JevResponse, error) {
		sequence = append(sequence, "jev")
		jevRequest = request
		return types.JevResponse{Answers: map[string]types.JevAnswer{
			"all_expected_fields_present": {Type: "noul", Noul: floatPointer(0.95)},
			"no_error_messages":           {Type: "noul", Noul: floatPointer(0.95)},
			"correct_page_loaded":         {Type: "noul", Noul: floatPointer(0.95)},
		}}, nil
	}, activity.RegisterOptions{Name: "CallJev"})
	env.RegisterActivityWithOptions(func(_ context.Context, _ browseractivity.CompleteBrowserReplayInput) error {
		sequence = append(sequence, "complete")
		return nil
	}, activity.RegisterOptions{Name: "CompleteBrowserReplay"})

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		return replayBrowserMutations(ctx, "browser-id", 7, 9, "https://example.test/apply")
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("replay workflow failed: %v", err)
	}
	want := []string{"list", "screenshot", "replay", "captcha", "screenshot", "jev", "complete"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("replay activity sequence = %v, want %v", sequence, want)
	}
	if jevRequest.IdUser != 7 || jevRequest.IdJobApplication == nil || *jevRequest.IdJobApplication != 9 {
		t.Fatalf("JEV request identity = user %d, application %v", jevRequest.IdUser, jevRequest.IdJobApplication)
	}
	for _, name := range []string{"all_expected_fields_present", "no_error_messages", "correct_page_loaded"} {
		criteria, ok := jevRequest.Questions[name].Criteria.(map[string]interface{})
		if !ok {
			t.Errorf("JEV question %q criteria = %#v, want true/false descriptions", name, jevRequest.Questions[name].Criteria)
			continue
		}
		trueDescription, hasTrue := criteria["true"].(string)
		falseDescription, hasFalse := criteria["false"].(string)
		if !hasTrue || trueDescription == "" || !hasFalse || falseDescription == "" {
			t.Errorf("JEV question %q criteria = %#v, want true/false descriptions", name, jevRequest.Questions[name].Criteria)
		}
	}
}

func intPointer(value int) *int           { return &value }
func floatPointer(value float64) *float64 { return &value }
