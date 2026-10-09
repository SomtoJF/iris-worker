package openrouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SomtoJF/iris-worker/aipi/types"
)

func TestGetDecisionsCompletionUsesDecisionsAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", r.Method)
		}
		if r.URL.Path != "/api/alpha/decisions" {
			t.Errorf("path = %q, want /api/alpha/decisions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q, want Bearer test-key", got)
		}

		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if request["model"] != jevModel {
			t.Errorf("model = %v, want %q", request["model"], jevModel)
		}
		if _, ok := request["state"]; !ok {
			t.Error("request missing state")
		}
		if _, ok := request["questions"]; !ok {
			t.Error("request missing questions")
		}
		if _, ok := request["id_user"]; ok {
			t.Error("internal user metadata must not be sent to OpenRouter")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"decision-123",
			"model":"perplexity/pplx-decider-v1.1-27b-20261006",
			"provider":"TypeSafe",
			"answers":{"relevant":{"type":"noul","noul":0.82}},
			"usage":{"input_tokens":100,"output_tokens":5,"cost":0.0000042}
		}`))
	}))
	defer server.Close()

	provider := &OpenRouterProvider{
		apiKey:            "test-key",
		jevHTTPClient:     server.Client(),
		decisionsEndpoint: server.URL + "/api/alpha/decisions",
	}
	response, err := provider.GetDecisionsCompletion(context.Background(), types.JevRequest{
		State: map[string]string{"page": "company overview"},
		Questions: map[string]types.JevQuestion{
			"relevant": {
				Type:         "noul",
				Instructions: "Is this relevant?",
				Criteria:     map[string]string{"true": "yes", "false": "no"},
			},
		},
		IdUser: 99,
	})
	if err != nil {
		t.Fatalf("GetDecisionsCompletion() error = %v", err)
	}
	if response.Model != "perplexity/pplx-decider-v1.1-27b-20261006" {
		t.Errorf("model = %q", response.Model)
	}
	if got := response.Answers["relevant"].Noul; got == nil || *got != 0.82 {
		t.Errorf("Noul answer = %v, want 0.82", got)
	}
	if response.Usage.Cost != 0.0000042 {
		t.Errorf("cost = %v, want 0.0000042", response.Usage.Cost)
	}
}

func TestGetDecisionsCompletionReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"invalid request"}}`, http.StatusBadRequest)
	}))
	defer server.Close()

	provider := &OpenRouterProvider{
		apiKey:            "test-key",
		jevHTTPClient:     server.Client(),
		decisionsEndpoint: server.URL + "/api/alpha/decisions",
	}
	_, err := provider.GetDecisionsCompletion(context.Background(), types.JevRequest{})
	if err == nil {
		t.Fatal("GetDecisionsCompletion() expected an error")
	}
}
