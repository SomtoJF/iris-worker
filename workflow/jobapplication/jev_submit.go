package jobapplication

import (
	"fmt"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	maxSubmitFailures = 2

	submitRequiredFilledThreshold = 0.85
	submitNoErrorsThreshold       = 0.8
	submitFinalStepThreshold      = 0.85
)

// findSubmitNode returns the final-submit control on the page, if any.
func findSubmitNode(nodes []browser.SerializableTaggedNode) *browser.SerializableTaggedNode {
	for i := range nodes {
		if nodes[i].Submit {
			return &nodes[i]
		}
	}
	return nil
}

func serializeSubmitState(shot browser.TakeScreenshotOutput) string {
	var sb strings.Builder
	sb.WriteString("PAGE URL: " + safeReplayURL(shot.CurrentURL) + "\n")
	sb.WriteString(fmt.Sprintf("HAS VISIBLE ALERTS: %t\n\nFORM FIELDS:\n", shot.HasVisibleAlerts))
	for _, node := range shot.TaggedNodes {
		value, checked := "", ""
		if node.Value != nil {
			value = *node.Value
		}
		if node.Checked != nil {
			checked = *node.Checked
		}
		required := node.Required != nil && *node.Required
		sb.WriteString(fmt.Sprintf("- index=%d role=%s label=%q description=%q required=%t value=%q checked=%q final_submit=%t\n",
			node.Index, node.Role, node.Label, node.Description, required, value, checked, node.Submit))
	}
	for _, node := range shot.TaggedFileInputNodes {
		label, value := "", ""
		if node.Label != nil {
			label = *node.Label
		}
		if node.Value != nil {
			value = *node.Value
		}
		sb.WriteString(fmt.Sprintf("- file_input index=%d label=%q value=%q\n", node.Index, label, value))
	}
	return sb.String()
}

// decideSubmit asks the decisions model whether the form is ready for its final submission.
// It never returns an error: any failure means "don't submit", leaving the planner to continue.
func decideSubmit(s *agentLoopState, shot browser.TakeScreenshotOutput) (submitIndex int, ready bool) {
	node := findSubmitNode(shot.TaggedNodes)
	if node == nil {
		return 0, false
	}
	logger := workflow.GetLogger(s.sessionCtx)

	image, err := getBase64Screenshot(s.sessionCtx, shot.Path)
	if err != nil {
		logger.Warn("Submit decision: failed to load screenshot", "error", err)
		return 0, false
	}

	ctx := workflow.WithActivityOptions(s.sessionCtx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	applicationID := s.input.IdJobApplication
	var response types.JevResponse
	err = workflow.ExecuteActivity(ctx, "CallDecisions", types.JevRequest{
		ScreenshotDataURL: image,
		State:             map[string]string{"form_state": serializeSubmitState(shot)},
		Questions: map[string]types.JevQuestion{
			"all_required_filled": {
				Type:         "noul",
				Instructions: "Is every required field on the page satisfied (text and select fields have a non-empty value; checkboxes, radios and switches are checked)?",
				Criteria: map[string]string{
					"true":  "Every required field has a value or is checked.",
					"false": "At least one required field is empty or unchecked.",
				},
			},
			"no_validation_errors": {
				Type:         "noul",
				Instructions: "Judging from the screenshot, are there no visible validation errors or blocking alerts on the form?",
				Criteria: map[string]string{
					"true":  "No validation error or blocking alert is visible.",
					"false": "A validation error or blocking alert is visible.",
				},
			},
			"is_final_step": {
				Type:         "noul",
				Instructions: "Is the control marked final_submit=true the last step that submits the whole application, rather than a Next or Continue button leading to more form steps?",
				Criteria: map[string]string{
					"true":  "The marked control submits the finished application.",
					"false": "More form steps remain after this control.",
				},
			},
		},
		IdUser:           s.input.IdUser,
		IdJobApplication: &applicationID,
	}).Get(ctx, &response)
	if err != nil {
		logger.Warn("Submit decision failed, leaving to planner", "error", err)
		return 0, false
	}

	for _, check := range []struct {
		name      string
		threshold float64
	}{
		{"all_required_filled", submitRequiredFilledThreshold},
		{"no_validation_errors", submitNoErrorsThreshold},
		{"is_final_step", submitFinalStepThreshold},
	} {
		passed, err := replayJevDecisionIsYes(response.Answers, check.name, check.threshold)
		if err != nil {
			logger.Warn("Submit decision invalid", "error", err)
			return 0, false
		}
		if !passed {
			return 0, false
		}
	}
	return node.Index, true
}

// submitIfReady submits the application when the decisions model says it is ready.
// Returns true when it handled the iteration (the planner must be skipped).
func submitIfReady(s *agentLoopState, shot browser.TakeScreenshotOutput) (bool, error) {
	if workflow.GetVersion(s.ctx, "jev-decides-submit", workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		return false, nil
	}
	index, ready := decideSubmit(s, shot)
	if !ready {
		return false, nil
	}

	step := &loopStep{Screenshot: shot}
	err := s.runToolCall(step, ToolCall{
		Name:      "submit_application",
		Arguments: map[string]interface{}{"element_index": index},
	})
	if err != nil {
		return true, err
	}
	last := s.toolHistory[len(s.toolHistory)-1]
	if submitted, _ := last.Result["submitted"].(bool); submitted && last.Error == nil {
		s.complete = true
		return true, nil
	}

	s.submitFailures++
	if s.submitFailures >= maxSubmitFailures {
		reason := "We couldn't submit your application. Please try again."
		return true, newJobAppError(fmt.Errorf("%s", reason), "Application submission failed", reason)
	}
	return true, nil
}
