package jobapplication

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/realtimeevent"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/google/uuid"
	sdkactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestResolveApplicationBrowserIDReusesPersistedUUID(t *testing.T) {
	want := uuid.NewString()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, _ uint) (applicationBrowserIDResult, error) {
		return applicationBrowserIDResult{Found: true, ID: want}, nil
	}, sdkactivity.RegisterOptions{Name: "GetApplicationBrowserID"})
	env.ExecuteWorkflow(testResolveApplicationBrowserID, sqldb.JobApplication{
		IdJobApplication: 17,
	})

	var got string
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("browser ID = %q, want persisted ID %q", got, want)
	}
}

func TestCaptureSolvedScreenshotSkipsCaptchaForKernelProvider(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	captchaDetectionCalled := false
	env.RegisterActivityWithOptions(func(_ context.Context, _ browser.TakeScreenshotInput) (browser.TakeScreenshotOutput, error) {
		return browser.TakeScreenshotOutput{Path: "screenshot.png"}, nil
	}, sdkactivity.RegisterOptions{Name: "TakeScreenshot"})
	env.RegisterActivityWithOptions(func(_ context.Context, _ browser.DetectCaptchaInput) (browser.DetectCaptchaOutput, error) {
		captchaDetectionCalled = true
		return browser.DetectCaptchaOutput{}, nil
	}, sdkactivity.RegisterOptions{Name: "DetectCaptcha"})
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		state := &agentLoopState{
			sessionCtx:      ctx,
			browserProvider: string(sqldb.BrowserProviderKernel),
		}
		_, err := captureSolvedScreenshot(state, 0)
		return err
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("capture screenshot: %v", err)
	}
	if captchaDetectionCalled {
		t.Fatal("captcha detection was called for the Kernel browser provider")
	}
}

func TestResolveApplicationBrowserIDPersistsGeneratedUUID(t *testing.T) {
	var mu sync.Mutex
	var created sqldb.CreateApplicationBrowserIDInput
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, _ uint) (applicationBrowserIDResult, error) {
		return applicationBrowserIDResult{Found: false}, nil
	}, sdkactivity.RegisterOptions{Name: "GetApplicationBrowserID"})
	env.RegisterActivityWithOptions(func(_ context.Context, input sqldb.CreateApplicationBrowserIDInput) (string, error) {
		mu.Lock()
		created = input
		mu.Unlock()
		return input.ID, nil
	}, sdkactivity.RegisterOptions{Name: "CreateApplicationBrowserID"})
	env.ExecuteWorkflow(testResolveApplicationBrowserID, sqldb.JobApplication{
		IdJobApplication: 23,
	})

	var browserID string
	if err := env.GetWorkflowResult(&browserID); err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(browserID); err != nil {
		t.Fatalf("generated browser ID %q is not a UUID: %v", browserID, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if created.IdJobApplication != 23 || created.ID != browserID {
		t.Fatalf("persisted creation = %+v, want ID %q for application 23", created, browserID)
	}
}

func TestResolveApplicationBrowserIDUsesPersistedWinner(t *testing.T) {
	want := uuid.NewString()
	var proposed string
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, _ uint) (applicationBrowserIDResult, error) {
		return applicationBrowserIDResult{Found: false}, nil
	}, sdkactivity.RegisterOptions{Name: "GetApplicationBrowserID"})
	env.RegisterActivityWithOptions(func(_ context.Context, input sqldb.CreateApplicationBrowserIDInput) (string, error) {
		proposed = input.ID
		return want, nil
	}, sdkactivity.RegisterOptions{Name: "CreateApplicationBrowserID"})
	env.ExecuteWorkflow(testResolveApplicationBrowserID, sqldb.JobApplication{
		IdJobApplication: 29,
	})

	var got string
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("browser ID = %q, want persisted winner %q", got, want)
	}
	if proposed == "" || proposed == got {
		t.Fatalf("create proposal = %q, want a distinct ID from concurrent persisted winner", proposed)
	}
}

func TestBeginBrowserReplayGenerationForExplicitRetry(t *testing.T) {
	var got sqldb.BeginBrowserReplayGenerationInput
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, input sqldb.BeginBrowserReplayGenerationInput) (uint64, error) {
		got = input
		return 2, nil
	}, sdkactivity.RegisterOptions{Name: "BeginBrowserReplayGeneration"})
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		return beginBrowserReplayGeneration(ctx, true, "browser-id")
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if got.ApplicationBrowserID != "browser-id" || got.WorkflowID == "" {
		t.Fatalf("retry generation input = %+v", got)
	}
}

func TestBeginBrowserReplayGenerationSkipsContinuation(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return beginBrowserReplayGeneration(ctx, false, "browser-id")
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseApplicationBrowserUsesPersistedID(t *testing.T) {
	want := uuid.NewString()
	var got browser.ClosePageInput
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, input browser.ClosePageInput) error {
		got = input
		return nil
	}, sdkactivity.RegisterOptions{Name: "ClosePage"})
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return closeApplicationBrowser(ctx, want)
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	if got.WorkflowID != want {
		t.Fatalf("close WorkflowID = %q, want %q", got.WorkflowID, want)
	}
}

func TestHandleApplicationCancelledPersistsStatusAndPublishes(t *testing.T) {
	var mu sync.Mutex
	var update sqldb.UpdateJobApplicationInput
	var eventType string
	var eventData map[string]interface{}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, input sqldb.UpdateJobApplicationInput) error {
		mu.Lock()
		update = input
		mu.Unlock()
		return nil
	}, sdkactivity.RegisterOptions{Name: "UpdateJobApplication"})
	env.RegisterActivityWithOptions(func(_ context.Context, _ uint, event string, data interface{}) error {
		mu.Lock()
		defer mu.Unlock()
		eventType = event
		eventData, _ = data.(map[string]interface{})
		return nil
	}, sdkactivity.RegisterOptions{Name: "PublishRedisEvent"})
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		handleApplicationCancelled(ctx, jobApplicationRuntimeInput{
			IdJobApplication:      31,
			ApplicationExternalId: "external-id",
			IdUser:                4,
		}, JobDetails{JobTitle: "Engineer"}, "user requested cancellation")
		return nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if update.IdJobApplication != 31 || fmt.Sprint(update.Data["status"]) != string(sqldb.JobApplicationStatusCancelled) || update.Data["cancellation_reason"] != "user requested cancellation" {
		t.Fatalf("cancellation update = %+v", update)
	}
	if eventType != string(realtimeevent.EventApplicationCancelled) || eventData["reason"] != "user requested cancellation" {
		t.Fatalf("cancellation event = %q %+v", eventType, eventData)
	}
}

func testResolveApplicationBrowserID(ctx workflow.Context, application sqldb.JobApplication) (string, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	return resolveApplicationBrowserID(ctx, application)
}
