package jobapplication

import (
	"testing"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/aipi/types"
)

func TestFindSubmitNode(t *testing.T) {
	nodes := []browser.SerializableTaggedNode{{Index: 1}, {Index: 7, Submit: true}}
	if n := findSubmitNode(nodes); n == nil || n.Index != 7 {
		t.Fatalf("expected submit node 7, got %+v", n)
	}
	if findSubmitNode(nodes[:1]) != nil {
		t.Fatal("expected no submit node")
	}
}

func TestPlannerToolsExcludeSubmit(t *testing.T) {
	tools := plannerTools()
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if tool.Name == "submit_application" {
			t.Fatal("planner must not expose submit_application")
		}
		if !tool.Strict || tool.Description == "" {
			t.Fatalf("tool %s must be strict and described", tool.Name)
		}
	}
	for _, want := range []string{"click", "write_cover_letter", toolCompleteApplication, toolFailApplication} {
		if !names[want] {
			t.Fatalf("missing planner tool %s", want)
		}
	}
}

func TestPlannerResponseFromToolCalls(t *testing.T) {
	resp, err := plannerResponseFromToolCalls([]types.ToolCall{{
		Name:      "click",
		Arguments: `{"element_index":3,"reasoning":"next","questions_answered":[{"question":"Name","answer":"A"}]}`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ToolCall == nil || resp.ToolCall.Name != "click" || resp.Reasoning != "next" || len(resp.QuestionsAnswered) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if _, leaked := resp.ToolCall.Arguments["reasoning"]; leaked {
		t.Fatal("shared properties must be stripped from tool arguments")
	}

	resp, err = plannerResponseFromToolCalls([]types.ToolCall{{
		Name:      toolFailApplication,
		Arguments: `{"failure_reason":"Login needed","failure_status":"LOGIN_REQUIRED","reasoning":"x","questions_answered":[]}`,
	}})
	if err != nil || !resp.IsApplicationFailed || resp.FailureStatus == nil || *resp.FailureStatus != PlannerFailureStatusLoginRequired {
		t.Fatalf("unexpected failure response: %+v, %v", resp, err)
	}

	if _, err := plannerResponseFromToolCalls(nil); err == nil {
		t.Fatal("expected error when no tool call is returned")
	}
}
