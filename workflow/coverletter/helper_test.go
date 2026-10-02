package coverletter

import (
	"context"
	"testing"
	"time"

	"github.com/SomtoJF/iris-worker/activity/web"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestLLMFilterSearchResultsUsesJevChoiceAndNoul(t *testing.T) {
	results := []web.SerperOrganicResult{
		{Title: "About Acme", Link: "https://www.acme.com/about"},
		{Title: "Our values", Link: "https://team.acme.com/values"},
		{Title: "Careers", Link: "https://acme.com/careers"},
		{Title: "About Acme", Link: "https://notacme.com/about"},
	}

	var received types.JevRequest
	type filterResult struct {
		Domain  string
		Results []web.SerperOrganicResult
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(_ context.Context, req types.JevRequest) (types.JevResponse, error) {
		received = req
		answers := map[string]types.JevAnswer{
			"company_domain": {Type: "choice", Choice: "acme.com"},
			results[0].Link:  {Type: "noul", Noul: floatPointer(0.8)},
			results[1].Link:  {Type: "noul", Noul: floatPointer(0.65)},
			results[2].Link:  {Type: "noul", Noul: floatPointer(0.99)},
			results[3].Link:  {Type: "noul", Noul: floatPointer(0.99)},
		}
		return types.JevResponse{Answers: answers}, nil
	}, activity.RegisterOptions{Name: "CallJev"})

	env.ExecuteWorkflow(func(ctx workflow.Context) (filterResult, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		domain, valid := llmFilterSearchResults(ctx, results, "Acme", "Engineering role", 1, nil)
		return filterResult{Domain: domain, Results: programmaticFilterResults(valid, domain)}, nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result filterResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	if result.Domain != "acme.com" {
		t.Errorf("domain = %q, want acme.com", result.Domain)
	}
	if len(result.Results) != 3 ||
		result.Results[0].Link != results[0].Link ||
		result.Results[1].Link != results[1].Link ||
		result.Results[2].Link != results[3].Link {
		t.Errorf("valid results = %#v, want JEV-positive results including non-company domains", result.Results)
	}
	if len(received.Questions) != len(results)+1 {
		t.Fatalf("JEV questions = %d, want %d", len(received.Questions), len(results)+1)
	}
	if received.Questions["company_domain"].Type != "choice" {
		t.Errorf("company_domain question type = %q, want choice", received.Questions["company_domain"].Type)
	}
	if _, ok := received.Questions["company_domain"].Criteria.(map[string]interface{})["acme.com"]; !ok {
		t.Errorf("Choice options missing normalized acme.com host: %#v", received.Questions["company_domain"].Criteria)
	}
	if received.Questions[results[1].Link].Type != "noul" {
		t.Errorf("URL-keyed result question type = %q, want noul", received.Questions[results[1].Link].Type)
	}
}

func TestLLMFilterSearchResultsFailsClosedForMissingAnswer(t *testing.T) {
	type filterResult struct {
		Results []web.SerperOrganicResult
	}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterActivityWithOptions(func(context.Context, types.JevRequest) (types.JevResponse, error) {
		return types.JevResponse{Answers: map[string]types.JevAnswer{
			"company_domain": {Type: "choice", Choice: "acme.com"},
		}}, nil
	}, activity.RegisterOptions{Name: "CallJev"})

	env.ExecuteWorkflow(func(ctx workflow.Context) (filterResult, error) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
		_, valid := llmFilterSearchResults(ctx, []web.SerperOrganicResult{
			{Title: "About Acme", Link: "https://acme.com/about"},
		}, "Acme", "", 1, nil)
		return filterResult{Results: valid}, nil
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}

	var result filterResult
	if err := env.GetWorkflowResult(&result); err != nil {
		t.Fatalf("get workflow result: %v", err)
	}
	if len(result.Results) != 0 {
		t.Errorf("valid results = %#v, want none", result.Results)
	}
}

func TestNormalizeSearchResultHostAndCompanyDomainMatch(t *testing.T) {
	host, err := normalizeSearchResultHost("https://WWW.Acme.com./about")
	if err != nil {
		t.Fatalf("normalizeSearchResultHost() error = %v", err)
	}
	if host != "acme.com" {
		t.Errorf("host = %q, want acme.com", host)
	}

	for _, tc := range []struct {
		host string
		want bool
	}{
		{host: "acme.com", want: true},
		{host: "jobs.acme.com", want: true},
		{host: "notacme.com", want: false},
	} {
		if got := hostMatchesCompanyDomain(tc.host, "acme.com"); got != tc.want {
			t.Errorf("hostMatchesCompanyDomain(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestProgrammaticFilterPrioritizesCompanyAndCapsAtFive(t *testing.T) {
	results := []web.SerperOrganicResult{
		{Link: "https://external.example/about-1"},
		{Link: "https://jobs.acme.com/team"},
		{Link: "https://external.example/about-2"},
		{Link: "https://acme.com/company"},
		{Link: "https://external.example/about-3"},
		{Link: "https://www.acme.com/mission"},
		{Link: "https://external.example/about-4"},
		{Link: "https://acme.com/careers"},
	}

	got := programmaticFilterResults(results, "acme.com")
	if len(got) != MAX_PAGES_TO_SCRAPE {
		t.Fatalf("page count = %d, want max %d", len(got), MAX_PAGES_TO_SCRAPE)
	}

	want := []string{
		"https://jobs.acme.com/team",
		"https://acme.com/company",
		"https://www.acme.com/mission",
		"https://external.example/about-1",
		"https://external.example/about-2",
	}
	for i := range want {
		if got[i].Link != want[i] {
			t.Errorf("page[%d] = %q, want %q", i, got[i].Link, want[i])
		}
	}
}

func floatPointer(value float64) *float64 {
	return &value
}
