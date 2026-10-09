package jobapplication

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SomtoJF/iris-worker/activity/browser"
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

func TestPlannerSchemaExcludesSubmit(t *testing.T) {
	for _, v := range plannerToolCallVariants() {
		if strings.Contains(strings.ToLower(toJSONForTest(v)), "submit_application") {
			t.Fatal("planner must not expose submit_application")
		}
	}
}

func toJSONForTest(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
