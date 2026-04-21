package summarizeissue

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type SummarizeIssueWorkflowInput struct {
	IssueExternalID string `json:"issue_external_id"`
}

type summarizeIssueLLMResponse struct {
	Summary string `json:"summary"`
}

func SummarizeIssueWorkflow(ctx workflow.Context, input SummarizeIssueWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("SummarizeIssueWorkflow started", "issueExternalId", input.IssueExternalID)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var issue sqldb.Issue
	if err := workflow.ExecuteActivity(ctx, "GetIssueByExternalID", input.IssueExternalID).Get(ctx, &issue); err != nil {
		return fmt.Errorf("GetIssueByExternalID: %w", err)
	}

	systemPrompt := "You write concise product issue summaries."
	userPrompt := fmt.Sprintf(
		"Summarize this issue in exactly two lines. Be concise and clear.\n\nTitle: %s\nType: %s\n\nBody (Markdown):\n%s\n",
		issue.Title,
		issue.Type,
		issue.ContentText,
	)

	llmReq := types.AIPIRequest{
		SystemMessage:  systemPrompt,
		UserMessage:    userPrompt,
		Model:          "google/gemma-4-31b-it",
		ResponseSchema: summarizeIssueResponseSchema(),
		Temperature:    floatPtr(0.2),
		MaxTokens:      intPtr(120),
		IdUser:         issue.UserId,
		// IdJobApplication intentionally omitted.
	}

	var llmResp types.AIPIResponse
	if err := workflow.ExecuteActivity(ctx, "CallLLM", llmReq).Get(ctx, &llmResp); err != nil {
		return fmt.Errorf("CallLLM: %w", err)
	}

	var parsed summarizeIssueLLMResponse
	if err := json.Unmarshal([]byte(llmResp.Content), &parsed); err != nil {
		return fmt.Errorf("unmarshal summarize issue response: %w", err)
	}

	summary := normalizeTwoLines(parsed.Summary)
	if summary == "" {
		return fmt.Errorf("empty summary from LLM")
	}

	if err := workflow.ExecuteActivity(ctx, "UpdateIssue", sqldb.UpdateIssueInput{
		IssueExternalID: input.IssueExternalID,
		Data: map[string]interface{}{
			"summary": summary,
		},
	}).Get(ctx, nil); err != nil {
		return fmt.Errorf("UpdateIssue: %w", err)
	}

	logger.Info("SummarizeIssueWorkflow completed", "issueExternalId", input.IssueExternalID)
	return nil
}

func summarizeIssueResponseSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"summary": map[string]interface{}{
				"type":        "string",
				"description": "A concise summary of the issue in exactly two lines separated by a newline.",
			},
		},
		"required": []string{"summary"},
	}
}

func normalizeTwoLines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Collapse excessive blank lines.
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, 2)
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ln)
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return ""
	}
	if len(out) == 1 {
		return out[0]
	}
	return out[0] + "\n" + out[1]
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
