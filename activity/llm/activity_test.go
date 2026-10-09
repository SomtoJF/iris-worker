package llm

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/google/uuid"
)

type captureAIPI struct {
	request         types.AIPIRequest
	responseContent string
}

func (c *captureAIPI) GetCompletion(_ context.Context, request types.AIPIRequest) (types.AIPIResponse, error) {
	c.request = request
	content := c.responseContent
	if content == "" {
		content = `{"ok":true}`
	}
	return types.AIPIResponse{Content: content}, nil
}

func (*captureAIPI) GetJevCompletion(context.Context, types.JevRequest) (types.JevResponse, error) {
	return types.JevResponse{}, nil
}

func TestCallLLMDecryptsUserActionOnlyInsideActivity(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("BROWSER_DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	actionID := uuid.New()
	plaintext := `[{"field_name":"OTP","value":"secret-code"}]`
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(strings.NewReader(strings.Repeat("n", len(nonce))), nonce); err != nil {
		t.Fatal(err)
	}
	ciphertext := append(nonce, aead.Seal(nil, nonce, []byte(plaintext), []byte(actionID.String()))...)
	aipi := &captureAIPI{}
	activity := NewActivity(aipi)

	response, err := activity.CallLLM(context.Background(), types.AIPIRequest{
		UserMessage:                "Continue the application.",
		UserActionID:               actionID.String(),
		UserActionResultCiphertext: ciphertext,
	})
	if err != nil {
		t.Fatalf("CallLLM: %v", err)
	}
	if !strings.Contains(aipi.request.UserMessage, "secret-code") {
		t.Fatalf("user answer missing from provider request: %q", aipi.request.UserMessage)
	}
	if len(aipi.request.UserActionResultCiphertext) != 0 || aipi.request.UserActionID != "" {
		t.Fatal("encrypted action metadata was forwarded to the LLM provider")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(response.Content), &parsed); err != nil || parsed["ok"] != true {
		t.Fatalf("planner response should remain parseable JSON, response=%q err=%v", response.Content, err)
	}
}

func TestCallLLMRedactsSubmittedValuesFromWorkflowResult(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	t.Setenv("BROWSER_DATA_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString(key))
	actionID := uuid.New()
	plaintext := `[{"field_name":"OTP","value":"secret-code"}]`
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(strings.NewReader(strings.Repeat("n", len(nonce))), nonce); err != nil {
		t.Fatal(err)
	}
	ciphertext := append(nonce, aead.Seal(nil, nonce, []byte(plaintext), []byte(actionID.String()))...)
	aipi := &captureAIPI{responseContent: `{"tool_call":{"name":"input_text","arguments":{"element_index":2,"text":"secret-code"}}}`}
	activity := NewActivity(aipi)

	response, err := activity.CallLLM(context.Background(), types.AIPIRequest{
		UserActionID:               actionID.String(),
		UserActionResultCiphertext: ciphertext,
	})
	if err != nil {
		t.Fatalf("CallLLM: %v", err)
	}
	if strings.Contains(response.Content, "secret-code") {
		t.Fatalf("plaintext user answer escaped in planner response: %q", response.Content)
	}
	if !strings.Contains(response.Content, "__IRIS_SECURE_USER_ACTION_VALUE_0__") {
		t.Fatalf("planner response did not contain secure answer marker: %q", response.Content)
	}
}
