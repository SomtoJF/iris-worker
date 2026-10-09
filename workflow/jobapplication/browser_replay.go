package jobapplication

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"sort"
	"strings"
	"time"

	browseractivity "github.com/SomtoJF/iris-worker/activity/browser"
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func replayBrowserMutations(ctx workflow.Context, workflowID string, userID, applicationID uint, startingURL string) error {
	var replay browseractivity.ListBrowserMutationsForReplayOutput
	if err := workflow.ExecuteActivity(ctx, "ListBrowserMutationsForReplay", browseractivity.ListBrowserMutationsForReplayInput{
		WorkflowID: workflowID,
	}).Get(ctx, &replay); err != nil {
		return fmt.Errorf("list browser mutations for replay: %w", err)
	}
	expectedFields := make(map[string]struct{})
	expectedURL := startingURL
	for _, mutation := range replay.Mutations {
		if mutation.Status != sqldb.BrowserMutationApplied || !mutation.Context.ReplaySafe {
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("browser mutation %d (%s) is not applied and replay-safe", mutation.IdBrowserMutationChangelog, mutation.Operation),
				"BrowserReplayBlocked",
				nil,
			)
		}
		if mutation.Context.Target != nil {
			if mutation.Operation == "input_text" || mutation.Operation == "input_multiple" {
				expectedFields[targetDescription(mutation.Context.Target)] = struct{}{}
			}
		}
		if mutation.Operation == "navigate" && mutation.Context.URL != "" {
			expectedURL = mutation.Context.URL
		}

		screenshot, err := takeReplayScreenshot(ctx, workflowID, mutation.IdBrowserMutationChangelog)
		if err != nil {
			return err
		}
		if mutation.Context.Target != nil {
			if err := verifyReplayTarget(ctx, screenshot, mutation.Context, userID, applicationID); err != nil {
				return fmt.Errorf("resolve replay target for mutation %d: %w", mutation.IdBrowserMutationChangelog, err)
			}
		}
		if err := workflow.ExecuteActivity(ctx, "ReplayBrowserMutation", browseractivity.ReplayBrowserMutationInput{
			WorkflowID: workflowID,
			MutationID: mutation.IdBrowserMutationChangelog,
		}).Get(ctx, nil); err != nil {
			return diagnoseReplayFailure(ctx, workflowID, mutation, userID, applicationID, err)
		}
		if replay.Provider != string(sqldb.BrowserProviderKernel) {
			if _, err := maybeSolveCaptcha(ctx, workflowID, userID, applicationID); err != nil {
				return fmt.Errorf("captcha check after replay mutation %d: %w", mutation.IdBrowserMutationChangelog, err)
			}
		}
		if err := waitReplayPacingInterval(ctx); err != nil {
			return err
		}
	}

	if err := verifyReplayFinalState(ctx, workflowID, userID, applicationID, expectedURL, expectedFields); err != nil {
		return err
	}
	return completeBrowserReplay(ctx, workflowID)
}

func takeReplayScreenshot(ctx workflow.Context, workflowID string, mutationID uint) (browseractivity.TakeScreenshotOutput, error) {
	var screenshot browseractivity.TakeScreenshotOutput
	err := workflow.ExecuteActivity(ctx, "TakeScreenshot", browseractivity.TakeScreenshotInput{
		WorkflowID: workflowID,
		FileName:   fmt.Sprintf("browser_replay_%d.png", mutationID),
	}).Get(ctx, &screenshot)
	if err != nil {
		return browseractivity.TakeScreenshotOutput{}, fmt.Errorf("capture/tag page before replay mutation %d: %w", mutationID, err)
	}
	return screenshot, nil
}

func verifyReplayTarget(ctx workflow.Context, screenshot browseractivity.TakeScreenshotOutput, mutationContext sqldb.BrowserMutationContext, userID, applicationID uint) error {
	target := mutationContext.Target
	matchingIndices := matchingReplayTargetIndices(screenshot.TaggedNodes, screenshot.TaggedFileInputNodes, target)
	indexDrift := len(matchingIndices) != 1
	if target.Role == "file" {
		indexDrift = indexDrift || mutationContext.FileInputIndex == nil || matchingIndices[0] != *mutationContext.FileInputIndex
	} else {
		indexDrift = indexDrift || mutationContext.ElementIndex == nil || matchingIndices[0] != *mutationContext.ElementIndex
	}
	if !indexDrift {
		return nil
	}
	state, err := safeReplayState(screenshot)
	if err != nil {
		return err
	}
	targetJSON, err := json.Marshal(target)
	if err != nil {
		return fmt.Errorf("serialize replay target: %w", err)
	}
	var response types.JevResponse
	if err := callReplayJev(ctx, userID, applicationID, types.JevRequest{
		State: map[string]string{
			"current_page":    state,
			"recorded_target": string(targetJSON),
		},
		Questions: map[string]types.JevQuestion{
			"target_present": {
				Type:         "noul",
				Instructions: "Is the recorded semantic target present as one visible, interactive element on the current page?",
				Criteria: map[string]string{
					"true":  "A current element matches the recorded role and accessible name or label.",
					"false": "No such current element is visible and interactive.",
				},
			},
		},
	}).Get(ctx, &response); err != nil {
		return fmt.Errorf("JEV replay target check: %w", err)
	}
	present, err := replayJevDecisionIsYes(response.Answers, "target_present", 0.7)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("JEV did not confirm the replay target is present with sufficient confidence")
	}
	if len(matchingIndices) != 1 {
		return fmt.Errorf("semantic target matched %d current elements; exactly one is required", len(matchingIndices))
	}
	return nil
}

func matchingReplayTargetIndices(nodes []browseractivity.SerializableTaggedNode, files []browseractivity.SerializableTaggedFileInputNode, target *sqldb.BrowserMutationTarget) []int {
	if target.Role == "file" {
		matches := make([]int, 0, 1)
		for _, file := range files {
			if target.Name != "" && !strings.EqualFold(strings.TrimSpace(target.Name), strings.TrimSpace(file.Name)) {
				continue
			}
			label := ""
			if file.Label != nil {
				label = *file.Label
			}
			if target.Label != "" && !strings.EqualFold(strings.TrimSpace(target.Label), strings.TrimSpace(label)) {
				continue
			}
			matches = append(matches, file.Index)
		}
		return matches
	}
	matches := make([]int, 0, 1)
	for _, node := range nodes {
		if target.Role != "" && !strings.EqualFold(strings.TrimSpace(target.Role), strings.TrimSpace(node.Role)) {
			continue
		}
		if target.Name != "" && !strings.EqualFold(strings.TrimSpace(target.Name), strings.TrimSpace(node.Name)) {
			continue
		}
		if target.Label != "" && !strings.EqualFold(strings.TrimSpace(target.Label), strings.TrimSpace(node.Label)) {
			continue
		}
		if target.Selector != "" && target.Selector != node.Selector {
			continue
		}
		if target.Submit != node.Submit {
			continue
		}
		matches = append(matches, node.Index)
	}
	return matches
}

func verifyReplayFinalState(ctx workflow.Context, workflowID string, userID, applicationID uint, expectedURL string, expectedFields map[string]struct{}) error {
	screenshot, err := takeReplayScreenshot(ctx, workflowID, 0)
	if err != nil {
		return fmt.Errorf("capture final replay state: %w", err)
	}
	state, err := safeReplayState(screenshot)
	if err != nil {
		return err
	}
	fields := make([]string, 0, len(expectedFields))
	for field := range expectedFields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("serialize expected replay fields: %w", err)
	}
	var response types.JevResponse
	if err := callReplayJev(ctx, userID, applicationID, types.JevRequest{
		State: map[string]string{
			"page_elements":        state,
			"expected_form_fields": string(fieldsJSON),
			"expected_page_url":    safeReplayURL(expectedURL),
			"current_url":          safeReplayURL(screenshot.CurrentURL),
			"has_visible_alerts":   fmt.Sprintf("%t", screenshot.HasVisibleAlerts),
		},
		Questions: map[string]types.JevQuestion{
			"all_expected_fields_present": {
				Type:         "noul",
				Instructions: "Are all expected form fields present and interactive on the page?",
				Criteria: map[string]string{
					"true":  "Every expected form field is present and interactive.",
					"false": "At least one expected form field is missing or not interactive.",
				},
			},
			"no_error_messages": {
				Type:         "noul",
				Instructions: "Are there no visible page-level error messages or validation errors? A true has_visible_alerts state means this check must be false.",
				Criteria: map[string]string{
					"true":  "No visible alert region or page-level validation error is present.",
					"false": "A visible alert region or page-level validation error is present.",
				},
			},
			"correct_page_loaded": {
				Type:         "noul",
				Instructions: "Is the expected application page loaded, rather than an error, redirect, or login page?",
				Criteria: map[string]string{
					"true":  "The expected application page is loaded and ready for the applicant.",
					"false": "An error, redirect, login page, or other unexpected page is loaded.",
				},
			},
		},
	}).Get(ctx, &response); err != nil {
		return fmt.Errorf("JEV final replay verification: %w", err)
	}
	for _, check := range []struct {
		name      string
		threshold float64
	}{
		{name: "all_expected_fields_present", threshold: 0.8},
		{name: "no_error_messages", threshold: 0.7},
		{name: "correct_page_loaded", threshold: 0.85},
	} {
		passed, err := replayJevDecisionIsYes(response.Answers, check.name, check.threshold)
		if err != nil {
			return err
		}
		if !passed {
			return fmt.Errorf("replay final-state verification failed: %s", check.name)
		}
	}
	return nil
}

func diagnoseReplayFailure(ctx workflow.Context, workflowID string, mutation browseractivity.ReplayMutationRef, userID, applicationID uint, replayErr error) error {
	screenshot, screenshotErr := takeReplayScreenshot(ctx, workflowID, mutation.IdBrowserMutationChangelog)
	if screenshotErr != nil {
		return fmt.Errorf("replay mutation %d failed: %w; diagnostic screenshot failed: %v", mutation.IdBrowserMutationChangelog, replayErr, screenshotErr)
	}
	state, stateErr := safeReplayState(screenshot)
	if stateErr != nil {
		return fmt.Errorf("replay mutation %d failed: %w; diagnostic state failed: %v", mutation.IdBrowserMutationChangelog, replayErr, stateErr)
	}
	var response types.JevResponse
	jevErr := callReplayJev(ctx, userID, applicationID, types.JevRequest{
		State: map[string]string{
			"failed_operation": mutation.Operation,
			"page_elements":    state,
		},
		Questions: map[string]types.JevQuestion{
			"page_usable": {
				Type:         "noul",
				Instructions: "Is the page still in a usable state despite the replay failure?",
				Criteria: map[string]string{
					"true":  "The page remains usable and the applicant can continue interacting with it.",
					"false": "An error or blocking state prevents the applicant from continuing.",
				},
			},
		},
	}).Get(ctx, &response)
	if jevErr != nil {
		return fmt.Errorf("replay mutation %d failed: %w; JEV diagnostic failed: %v", mutation.IdBrowserMutationChangelog, replayErr, jevErr)
	}
	usable, decisionErr := replayJevDecisionIsYes(response.Answers, "page_usable", 0.6)
	if decisionErr != nil {
		return fmt.Errorf("replay mutation %d failed: %w; JEV diagnostic invalid: %v", mutation.IdBrowserMutationChangelog, replayErr, decisionErr)
	}
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("replay mutation %d failed (JEV reports page usable=%t); replay stopped", mutation.IdBrowserMutationChangelog, usable),
		"BrowserReplayFailed",
		replayErr,
	)
}

func safeReplayState(screenshot browseractivity.TakeScreenshotOutput) (string, error) {
	type node struct {
		Index       int    `json:"index"`
		Description string `json:"description"`
		Name        string `json:"name,omitempty"`
		Label       string `json:"label,omitempty"`
		Role        string `json:"role"`
		Submit      bool   `json:"submit,omitempty"`
		Required    *bool  `json:"required,omitempty"`
	}
	type fileInput struct {
		Index int    `json:"index"`
		Name  string `json:"name,omitempty"`
		Label string `json:"label,omitempty"`
	}
	safeNodes := make([]node, len(screenshot.TaggedNodes))
	for i, item := range screenshot.TaggedNodes {
		safeNodes[i] = node{
			Index: item.Index, Description: item.Description, Name: item.Name,
			Label: item.Label, Role: item.Role, Submit: item.Submit, Required: item.Required,
		}
	}
	safeFiles := make([]fileInput, len(screenshot.TaggedFileInputNodes))
	for i, item := range screenshot.TaggedFileInputNodes {
		label := ""
		if item.Label != nil {
			label = *item.Label
		}
		safeFiles[i] = fileInput{Index: item.Index, Name: item.Name, Label: label}
	}
	encoded, err := json.Marshal(map[string]interface{}{
		"tagged_nodes":       safeNodes,
		"tagged_file_inputs": safeFiles,
		"has_visible_alerts": screenshot.HasVisibleAlerts,
	})
	if err != nil {
		return "", fmt.Errorf("serialize sanitized accessibility state: %w", err)
	}
	return string(encoded), nil
}

func callReplayJev(ctx workflow.Context, userID, applicationID uint, request types.JevRequest) workflow.Future {
	options := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	request.IdUser = userID
	request.IdJobApplication = &applicationID
	return workflow.ExecuteActivity(workflow.WithActivityOptions(ctx, options), "CallJev", request)
}

func replayJevDecisionIsYes(answers map[string]types.JevAnswer, name string, threshold float64) (bool, error) {
	answer, ok := answers[name]
	if !ok || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
		return false, fmt.Errorf("missing or invalid JEV Noul answer %q", name)
	}
	return *answer.Noul >= threshold, nil
}

func targetDescription(target *sqldb.BrowserMutationTarget) string {
	if target == nil {
		return ""
	}
	if target.Label != "" {
		return target.Label
	}
	return target.Name
}

func safeReplayURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}

func waitReplayPacingInterval(ctx workflow.Context) error {
	var seconds int
	if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
		return randomReplayPacingSeconds()
	}).Get(&seconds); err != nil {
		return fmt.Errorf("select replay pacing interval: %w", err)
	}
	if seconds < 1 || seconds > 5 {
		return fmt.Errorf("replay pacing interval %d is out of range", seconds)
	}
	if err := workflow.Sleep(ctx, time.Duration(seconds)*time.Second); err != nil {
		return fmt.Errorf("wait between replay mutations: %w", err)
	}
	return nil
}

func randomReplayPacingSeconds() int {
	return rand.Intn(5) + 1
}

func completeBrowserReplay(ctx workflow.Context, workflowID string) error {
	return workflow.ExecuteActivity(ctx, "CompleteBrowserReplay", browseractivity.CompleteBrowserReplayInput{
		WorkflowID: workflowID,
	}).Get(ctx, nil)
}
