package jobdiscovery

import (
	"context"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestFilterJobHitsWithJev(t *testing.T) {
	hits := []mergedSearchHit{
		{Title: "Backend Engineer", Link: "https://jobs.lever.co/acme/123"},
		{Title: "Careers", Link: "https://jobs.lever.co/acme"},
	}
	var received types.JevRequest
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, request types.JevRequest) (types.JevResponse, error) {
		received = request
		return types.JevResponse{Answers: map[string]types.JevAnswer{
			jobHitQuestionKey(0): {Type: "noul", Noul: floatPointer(0.9)},
			jobHitQuestionKey(1): {Type: "noul", Noul: floatPointer(0.1)},
		}}, nil
	}, activity.RegisterOptions{Name: "CallJev"})

	env.ExecuteWorkflow(func(ctx workflow.Context) ([]mergedSearchHit, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		return filterJobHitsWithJev(ctx, hits, 42)
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var approved []mergedSearchHit
	if err := env.GetWorkflowResult(&approved); err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	if len(approved) != 1 || approved[0].Link != hits[0].Link {
		t.Fatalf("approved hits = %#v, want only first hit", approved)
	}
	if received.IdUser != 42 || len(received.Questions) != len(hits) {
		t.Fatalf("JEV request user/questions = %d/%d, want 42/%d", received.IdUser, len(received.Questions), len(hits))
	}
	if received.Questions[jobHitQuestionKey(0)].Type != "noul" {
		t.Fatalf("JEV question = %#v, want noul", received.Questions[jobHitQuestionKey(0)])
	}
}

func TestFilterJobHitsWithJevFailsClosedOnInvalidAnswer(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(context.Context, types.JevRequest) (types.JevResponse, error) {
		return types.JevResponse{Answers: map[string]types.JevAnswer{
			jobHitQuestionKey(0): {Type: "choice", Choice: "yes"},
		}}, nil
	}, activity.RegisterOptions{Name: "CallJev"})

	env.ExecuteWorkflow(func(ctx workflow.Context) ([]mergedSearchHit, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		return filterJobHitsWithJev(ctx, []mergedSearchHit{
			{Title: "Backend Engineer", Link: "https://jobs.lever.co/acme/123"},
		}, 42)
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var approved []mergedSearchHit
	if err := env.GetWorkflowResult(&approved); err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	if len(approved) != 0 {
		t.Fatalf("approved hits = %#v, want none for invalid answer", approved)
	}
}

func TestPostFilterExtractedJobsAllowsOnlyKnownConcreteHits(t *testing.T) {
	hits := []mergedSearchHit{
		{
			Title: "Backend Engineer",
			Link:  "https://jobs.lever.co/acme/123",
			Date:  "2025-01-02",
		},
		{
			Title: "Engineering jobs",
			Link:  "https://jobs.lever.co/acme",
		},
	}
	extracted := []extractedJob{
		{HitIndex: 0, Title: "Backend Engineer", CompanyName: "Acme"},
		{HitIndex: 0, Title: "Duplicate", CompanyName: "Acme"},
		{HitIndex: 1, Title: "Invented role", CompanyName: "Acme"},
		{HitIndex: 2, Title: "Unknown hit", CompanyName: "Acme"},
		{HitIndex: 0, Title: "   ", CompanyName: "Acme"},
	}

	got := postFilterExtractedJobs(extracted, hits)
	if len(got) != 1 {
		t.Fatalf("jobs = %#v, want exactly one valid deduplicated result", got)
	}
	if got[0].Url != hits[0].Link || got[0].DatePosted != hits[0].Date ||
		got[0].Title != "Backend Engineer" || got[0].CompanyName != "Acme" {
		t.Errorf("job = %#v, want source URL/date and extracted fields", got[0])
	}
}

func floatPointer(value float64) *float64 {
	return &value
}
