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
		if request["model"] != "custom/decider-model" {
			t.Errorf("model = %v, want %q", request["model"], "custom/decider-model")
		}
		if _, ok := request["state"]; !ok {
			t.Error("request missing state")
		}
		state, ok := request["state"].(map[string]any)
		if !ok {
			t.Fatalf("state = %T, want original state object", request["state"])
		}
		if state["page"] != "company overview" {
			t.Errorf("state.page = %v, want company overview", state["page"])
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
			"model":"~perplexity/pplx-decider-latest",
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
		Model: "custom/decider-model",
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
	if response.Model != "~perplexity/pplx-decider-latest" {
		t.Errorf("model = %q", response.Model)
	}
	if got := response.Answers["relevant"].Noul; got == nil || *got != 0.82 {
		t.Errorf("Noul answer = %v, want 0.82", got)
	}
	if response.Usage.Cost != 0.0000042 {
		t.Errorf("cost = %v, want 0.0000042", response.Usage.Cost)
	}
}

func TestGetDecisionsCompletionEncodesScreenshotAsTopLevelStateItem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request body: %v", err)
			return
		}

		state, ok := request["state"].([]any)
		if !ok {
			t.Fatalf("state = %T, want array", request["state"])
		}
		if len(state) != 2 {
			t.Fatalf("state length = %d, want 2", len(state))
		}
		pageState, ok := state[0].(map[string]any)
		if !ok || pageState["page"] != "company overview" {
			t.Errorf("state[0] = %v, want existing state", state[0])
		}
		image, ok := state[1].(map[string]any)
		if !ok || image["type"] != "image_url" {
			t.Errorf("state[1] = %v, want image_url item", state[1])
		}
		imageURL, ok := image["image_url"].(map[string]any)
		if !ok || imageURL["url"] != "data:image/png;base64,abc123" {
			t.Errorf("state[1].image_url = %v, want screenshot data URL", image["image_url"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"decider","answers":{}}`))
	}))
	defer server.Close()

	provider := &OpenRouterProvider{
		jevHTTPClient:     server.Client(),
		decisionsEndpoint: server.URL,
	}
	_, err := provider.GetDecisionsCompletion(context.Background(), types.JevRequest{
		Model:             "custom/decider-model",
		State:             map[string]string{"page": "company overview"},
		ScreenshotDataURL: "data:image/png;base64,abc123",
	})
	if err != nil {
		t.Fatalf("GetDecisionsCompletion() error = %v", err)
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
	_, err := provider.GetDecisionsCompletion(context.Background(), types.JevRequest{Model: "custom/decider-model"})
	if err == nil {
		t.Fatal("GetDecisionsCompletion() expected an error")
	}
}

func TestGetDecisionsCompletionRejectsEmptyModelBeforeHTTP(t *testing.T) {
	called := false
	provider := &OpenRouterProvider{
		jevHTTPClient: roundTripFunc(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, nil
		}),
	}

	_, err := provider.GetDecisionsCompletion(context.Background(), types.JevRequest{})
	if err == nil || err.Error() != "JEV Decisions request missing model" {
		t.Fatalf("GetDecisionsCompletion() error = %v, want missing model error", err)
	}
	if called {
		t.Fatal("HTTP client called for empty model")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }
