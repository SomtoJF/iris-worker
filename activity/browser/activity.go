package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	browserclient "github.com/SomtoJF/iris-worker/browser"
	browsertype "github.com/SomtoJF/iris-worker/browser/types"
	"github.com/google/uuid"
	sdkactivity "go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

type Activity struct {
	client browserclient.BrowserClient
	store  *sqldb.BrowserStore
}

func NewActivities(client browserclient.BrowserClient, stores ...*sqldb.BrowserStore) *Activity {
	activity := &Activity{client: client}
	if len(stores) > 0 {
		activity.store = stores[0]
	}
	return activity
}

func applicationBrowserID(workflowID string) browsertype.ApplicationBrowserID {
	return browsertype.ApplicationBrowserID(workflowID)
}

func (a *Activity) OpenWebpage(ctx context.Context, input OpenWebpageInput) (OpenWebpageOutput, error) {
	id := applicationBrowserID(input.WorkflowID)
	if err := a.client.StartBrowser(ctx, id, browsertype.BrowserOptions{
		StartingURL: input.Url,
	}); err != nil {
		return OpenWebpageOutput{}, err
	}
	provider := a.client.GetBrowserProvider()
	if a.store == nil {
		return OpenWebpageOutput{Provider: provider}, nil
	}
	browserID, err := uuid.Parse(input.WorkflowID)
	if err != nil {
		return OpenWebpageOutput{}, fmt.Errorf("parse application browser ID after startup: %w", err)
	}
	after, afterFound, err := a.store.GetBrowserSession(ctx, browserID)
	if err != nil {
		return OpenWebpageOutput{}, fmt.Errorf("read browser session after startup: %w", err)
	}
	if afterFound {
		provider = string(after.Provider)
	}
	return OpenWebpageOutput{ReplayRequired: afterFound && after.ReplayPending, Provider: provider}, nil
}

func (a *Activity) TakeScreenshot(ctx context.Context, input TakeScreenshotInput) (TakeScreenshotOutput, error) {
	screenshot, err := a.client.ScreenshotForLLM(ctx, applicationBrowserID(input.WorkflowID), input.FileName)
	if err != nil {
		return TakeScreenshotOutput{}, err
	}

	out := TakeScreenshotOutput{
		Path:                 screenshot.Path,
		CurrentURL:           screenshot.CurrentURL,
		HasVisibleAlerts:     screenshot.HasVisibleAlerts,
		TaggedNodes:          make([]SerializableTaggedNode, len(screenshot.TaggedNodes)),
		TaggedFileInputNodes: make([]SerializableTaggedFileInputNode, len(screenshot.TaggedFileInputNodes)),
	}
	for i, node := range screenshot.TaggedNodes {
		out.TaggedNodes[i] = SerializableTaggedNode{
			Index: node.Index, Description: node.Description, Name: node.Name, Label: node.Label, Selector: node.Selector, Submit: node.Submit,
			X: node.X, Y: node.Y, Width: node.Width, Height: node.Height,
			Role: node.Role, Value: node.Value, Required: node.Required, Checked: node.Checked,
		}
	}
	for i, node := range screenshot.TaggedFileInputNodes {
		out.TaggedFileInputNodes[i] = SerializableTaggedFileInputNode{
			Index: node.Index, HTML: node.HTML, Name: node.Name, Label: node.Label, Value: node.Value,
		}
	}
	return out, nil
}

func (a *Activity) ListBrowserMutationsForReplay(ctx context.Context, input ListBrowserMutationsForReplayInput) (ListBrowserMutationsForReplayOutput, error) {
	if a.store == nil {
		return ListBrowserMutationsForReplayOutput{}, fmt.Errorf("browser mutation store is required for replay")
	}
	browserID, err := uuid.Parse(input.WorkflowID)
	if err != nil {
		return ListBrowserMutationsForReplayOutput{}, fmt.Errorf("parse application browser ID for replay: %w", err)
	}
	mutations, session, err := a.store.ListBrowserMutationsForReplay(ctx, browserID)
	if err != nil {
		return ListBrowserMutationsForReplayOutput{}, err
	}
	out := ListBrowserMutationsForReplayOutput{
		Provider:         string(session.Provider),
		ReplayGeneration: session.ReplayGeneration,
		Mutations:        make([]ReplayMutationRef, len(mutations)),
	}
	for i, mutation := range mutations {
		out.Mutations[i] = ReplayMutationRef{
			IdBrowserMutationChangelog: mutation.IdBrowserMutationChangelog,
			Operation:                  mutation.Operation,
			Context:                    mutation.Context,
			Status:                     mutation.Status,
			CreatedAt:                  mutation.CreatedAt,
		}
	}
	return out, nil
}

func (a *Activity) CompleteBrowserReplay(ctx context.Context, input CompleteBrowserReplayInput) error {
	if a.store == nil {
		return fmt.Errorf("browser mutation store is required to complete replay")
	}
	browserID, err := uuid.Parse(input.WorkflowID)
	if err != nil {
		return fmt.Errorf("parse application browser ID to complete replay: %w", err)
	}
	session, found, err := a.store.GetBrowserSession(ctx, browserID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("browser session not found to complete replay")
	}
	if err := a.store.CompleteBrowserReplay(ctx, browserID, session.ProviderSessionID); err != nil {
		return err
	}
	return nil
}

func (a *Activity) ReplayBrowserMutation(ctx context.Context, input ReplayBrowserMutationInput) error {
	if a.store == nil {
		return temporal.NewNonRetryableApplicationError("browser mutation store is required for replay", "BrowserReplayUnavailable", nil)
	}
	browserID, err := uuid.Parse(input.WorkflowID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("invalid application browser ID for replay", "BrowserReplayInvalidInput", err)
	}
	mutation, rawArgs, err := a.store.GetBrowserMutationForReplay(ctx, browserID, input.MutationID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError(err.Error(), "BrowserReplayUnsafe", err)
	}
	session, found, err := a.store.GetBrowserSession(ctx, browserID)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("load browser session for replay", "BrowserReplayUnavailable", err)
	}
	if !found || session.ProviderSessionID == "" {
		return temporal.NewNonRetryableApplicationError("active browser provider session is missing for replay", "BrowserReplayUnavailable", nil)
	}
	if mutation.Result != nil && mutation.Result.ReplaySessionID == session.ProviderSessionID {
		return nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return temporal.NewNonRetryableApplicationError("decode replay mutation arguments", "BrowserReplayInvalidMutation", err)
	}

	index, fileIndex, err := a.resolveReplayTarget(ctx, input.WorkflowID, mutation.Operation, mutation.Context.Target)
	if err != nil {
		return temporal.NewNonRetryableApplicationError(err.Error(), "BrowserReplayTargetUnresolved", err)
	}
	if err := a.store.MarkBrowserMutationReplayPending(ctx, mutation.IdBrowserMutationChangelog, session.ProviderSessionID); err != nil {
		return temporal.NewNonRetryableApplicationError("claim replay mutation before execution", "BrowserReplayStateConflict", err)
	}

	if err := a.executeReplayMutation(ctx, input.WorkflowID, mutation.Operation, args, index, fileIndex); err != nil {
		statusErr := a.store.MarkBrowserMutationReconcileRequired(ctx, mutation.IdBrowserMutationChangelog, &sqldb.BrowserMutationResult{
			Outcome:         sqldb.BrowserMutationOutcomeUncertain,
			ReplaySessionID: session.ProviderSessionID,
		})
		return temporal.NewNonRetryableApplicationError("replay browser mutation failed and requires reconciliation", "BrowserReplayExecutionUncertain", errors.Join(err, statusErr))
	}
	if err := a.store.MarkBrowserMutationApplied(ctx, mutation.IdBrowserMutationChangelog, &sqldb.BrowserMutationResult{
		Outcome:         sqldb.BrowserMutationOutcomeApplied,
		ReplaySafe:      true,
		ReplaySessionID: session.ProviderSessionID,
	}); err != nil {
		return temporal.NewNonRetryableApplicationError("persist replay mutation completion", "BrowserReplayStateConflict", err)
	}
	return nil
}

func (a *Activity) resolveReplayTarget(ctx context.Context, workflowID, operation string, target *sqldb.BrowserMutationTarget) (int, int, error) {
	if operation != "click" && operation != "input_text" && operation != "input_multiple" && operation != "upload_file" {
		return 0, 0, nil
	}
	if target == nil {
		return 0, 0, fmt.Errorf("mutation has no semantic target")
	}
	screenshot, err := a.client.ScreenshotForLLM(ctx, applicationBrowserID(workflowID), "browser_replay_target.png")
	if err != nil {
		return 0, 0, fmt.Errorf("capture replay target screenshot: %w", err)
	}
	if operation == "upload_file" {
		index, err := resolveFileInputTarget(screenshot.TaggedFileInputNodes, target)
		return 0, index, err
	}
	index, err := resolveTaggedTarget(screenshot.TaggedNodes, target)
	return index, 0, err
}

func resolveTaggedTarget(nodes []browsertype.TaggedNode, target *sqldb.BrowserMutationTarget) (int, error) {
	if target.Role == "" || target.Name == "" && target.Label == "" && target.Selector == "" {
		return 0, fmt.Errorf("mutation semantic target is incomplete")
	}
	var matches []int
	for _, node := range nodes {
		if !matchesTarget(node.Role, node.Name, node.Label, node.Selector, node.Submit, target) {
			continue
		}
		matches = append(matches, node.Index)
	}
	if len(matches) != 1 {
		return 0, fmt.Errorf("semantic target matched %d tagged elements; exactly one is required", len(matches))
	}
	return matches[0], nil
}

func resolveFileInputTarget(nodes []browsertype.TaggedFileInput, target *sqldb.BrowserMutationTarget) (int, error) {
	if target.Name == "" && target.Label == "" {
		return 0, fmt.Errorf("file input semantic target is incomplete")
	}
	var matches []int
	for _, node := range nodes {
		if target.Name != "" && !strings.EqualFold(strings.TrimSpace(node.Name), strings.TrimSpace(target.Name)) {
			continue
		}
		label := ""
		if node.Label != nil {
			label = *node.Label
		}
		if target.Label != "" && !strings.EqualFold(strings.TrimSpace(label), strings.TrimSpace(target.Label)) {
			continue
		}
		matches = append(matches, node.Index)
	}
	if len(matches) != 1 {
		return 0, fmt.Errorf("file input semantic target matched %d elements; exactly one is required", len(matches))
	}
	return matches[0], nil
}

func matchesTarget(role, name, label, selector string, submit bool, target *sqldb.BrowserMutationTarget) bool {
	if target.Role != "" && !strings.EqualFold(strings.TrimSpace(role), strings.TrimSpace(target.Role)) {
		return false
	}
	if target.Name != "" && !strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(target.Name)) {
		return false
	}
	if target.Label != "" && !strings.EqualFold(strings.TrimSpace(label), strings.TrimSpace(target.Label)) {
		return false
	}
	if target.Submit != submit {
		return false
	}
	return target.Selector == "" || selector == target.Selector
}

func (a *Activity) executeReplayMutation(ctx context.Context, workflowID, operation string, args map[string]json.RawMessage, elementIndex, fileInputIndex int) error {
	id := applicationBrowserID(workflowID)
	switch operation {
	case "click":
		return a.client.Click(ctx, id, elementIndex)
	case "input_text", "input_multiple":
		var input struct {
			Text    string `json:"text"`
			Replace bool   `json:"replace"`
		}
		if err := json.Unmarshal(args["text"], &input.Text); err != nil {
			return fmt.Errorf("decode replay text: %w", err)
		}
		if raw := args["replace"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &input.Replace); err != nil {
				return fmt.Errorf("decode replay replace flag: %w", err)
			}
		}
		return a.client.Type(ctx, id, browsertype.FieldInput{ElementIndex: elementIndex, Text: input.Text, Replace: input.Replace})
	case "scroll":
		var input struct {
			Direction string  `json:"direction"`
			Ratio     float64 `json:"ratio"`
		}
		if err := json.Unmarshal(args["direction"], &input.Direction); err != nil {
			return fmt.Errorf("decode replay scroll direction: %w", err)
		}
		if err := json.Unmarshal(args["ratio"], &input.Ratio); err != nil {
			return fmt.Errorf("decode replay scroll ratio: %w", err)
		}
		return a.client.Scroll(ctx, id, input.Direction, input.Ratio)
	case "navigate":
		var url string
		if err := json.Unmarshal(args["url"], &url); err != nil {
			return fmt.Errorf("decode replay navigation URL: %w", err)
		}
		return a.client.Navigate(ctx, id, url)
	case "upload_file":
		var path string
		if err := json.Unmarshal(args["file_path"], &path); err != nil {
			return fmt.Errorf("decode replay file path: %w", err)
		}
		return a.client.UploadFile(ctx, id, fileInputIndex, path)
	default:
		return fmt.Errorf("unsupported browser replay operation %q", operation)
	}
}

func (a *Activity) GetBase64Screenshot(ctx context.Context, input GetBase64ScreenshotInput) (string, error) {
	screenshot, err := os.ReadFile(input.Path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(screenshot)), nil
}

func (a *Activity) Click(ctx context.Context, input ClickInput) error {
	return a.mutate(ctx, input.WorkflowID, "click", map[string]interface{}{
		"element_index": input.ElementIndex,
		"target":        input.Target,
	}, sqldb.BrowserMutationContext{
		ElementIndex: &input.ElementIndex,
		Target:       input.Target,
		ReplaySafe:   isReplaySafeTarget("click", input.Target),
	}, "", func() error {
		return a.client.Click(ctx, applicationBrowserID(input.WorkflowID), input.ElementIndex)
	})
}

func (a *Activity) Type(ctx context.Context, input TypeInput) error {
	return a.mutate(ctx, input.WorkflowID, "input_text", map[string]interface{}{
		"element_index": input.ElementIndex,
		"text":          input.Text,
		"replace":       input.Replace,
		"target":        input.Target,
	}, sqldb.BrowserMutationContext{
		ElementIndex: &input.ElementIndex,
		Target:       input.Target,
		ReplaySafe:   isReplaySafeTarget("input_text", input.Target),
	}, "", func() error {
		return a.client.Type(ctx, applicationBrowserID(input.WorkflowID), browsertype.FieldInput{
			ElementIndex: input.ElementIndex,
			Text:         input.Text,
			Replace:      input.Replace,
		})
	})
}

func (a *Activity) TypeMultiple(ctx context.Context, input TypeMultipleInput) error {
	if len(input.Fields) == 0 {
		return nil
	}

	id := applicationBrowserID(input.WorkflowID)
	activityID := ""
	if a.store != nil {
		activityID = sdkactivity.GetInfo(ctx).ActivityID
		if activityID == "" {
			return fmt.Errorf("record browser mutation: Temporal activity ID is unavailable")
		}
	}
	var errorMessages []string
	plannerMistake := false
	for i, field := range input.Fields {
		err := a.mutate(ctx, input.WorkflowID, "input_multiple", map[string]interface{}{
			"element_index": field.ElementIndex,
			"text":          field.Text,
			"replace":       true,
			"target":        field.Target,
		}, sqldb.BrowserMutationContext{
			ElementIndex: &field.ElementIndex,
			Target:       field.Target,
			ReplaySafe:   isReplaySafeTarget("input_text", field.Target),
		}, fmt.Sprintf("%s/%d", activityID, i), func() error {
			return a.client.Type(ctx, id, browsertype.FieldInput{
				ElementIndex: field.ElementIndex,
				Text:         field.Text,
				Replace:      true,
			})
		})
		if err != nil {
			errorMessages = append(errorMessages,
				fmt.Sprintf("field %d (index %d): %s", i, field.ElementIndex, err.Error()))
			if isPlannerElementMistake(err) {
				plannerMistake = true
			}
			continue
		}

		if i < len(input.Fields)-1 {
			time.Sleep(150 * time.Millisecond)
		}
	}

	if len(errorMessages) > 0 {
		msg := fmt.Sprintf("failed to type %d/%d fields: %v",
			len(errorMessages), len(input.Fields), errorMessages)
		if plannerMistake {
			return temporal.NewNonRetryableApplicationError(msg, "ElementNotEditable", nil)
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func (a *Activity) TypeUserActionSecure(ctx context.Context, input SecureTypeInput) error {
	actionID, err := uuid.Parse(input.ActionID)
	if err != nil {
		return fmt.Errorf("parse secure user action ID: %w", err)
	}
	plaintext, err := sqldb.DecryptUserActionResult(input.Ciphertext, actionID)
	if err != nil {
		return fmt.Errorf("decrypt secure browser input: %w", err)
	}
	if input.Operation == "user_action_value" {
		var values []sqldb.UserActionResultItem
		if err := json.Unmarshal([]byte(plaintext), &values); err != nil {
			return fmt.Errorf("decode secure user action values: %w", err)
		}
		if input.ValueIndex < 0 || input.ValueIndex >= len(values) {
			return fmt.Errorf("secure user action value index %d is out of range", input.ValueIndex)
		}
		return a.Type(ctx, TypeInput{
			WorkflowID:   input.WorkflowID,
			ElementIndex: input.ElementIndex,
			Text:         values[input.ValueIndex].Value,
			Replace:      true,
			Target:       input.Target,
		})
	}
	switch input.Operation {
	case "input_text":
		var decoded TypeInput
		if err := json.Unmarshal([]byte(plaintext), &decoded); err != nil {
			return fmt.Errorf("decode secure text input: %w", err)
		}
		return a.Type(ctx, decoded)
	case "input_multiple":
		var decoded TypeMultipleInput
		if err := json.Unmarshal([]byte(plaintext), &decoded); err != nil {
			return fmt.Errorf("decode secure multiple input: %w", err)
		}
		return a.TypeMultiple(ctx, decoded)
	default:
		return fmt.Errorf("unsupported secure browser input operation %q", input.Operation)
	}
}

func isPlannerElementMistake(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "is not editable") || strings.Contains(msg, "not found among")
}

func (a *Activity) Scroll(ctx context.Context, input ScrollInput) error {
	if input.Ratio < 0.1 || input.Ratio > 1.0 {
		return fmt.Errorf("scroll ratio must be between 0.1 and 1.0, got %f", input.Ratio)
	}
	if input.Direction != "up" && input.Direction != "down" {
		return fmt.Errorf("scroll direction must be 'up' or 'down', got %s", input.Direction)
	}
	return a.mutate(ctx, input.WorkflowID, "scroll", map[string]interface{}{
		"direction": input.Direction,
		"ratio":     input.Ratio,
	}, sqldb.BrowserMutationContext{ReplaySafe: true}, "", func() error {
		return a.client.Scroll(ctx, applicationBrowserID(input.WorkflowID), input.Direction, input.Ratio)
	})
}

func (a *Activity) Navigate(ctx context.Context, input NavigateInput) error {
	return a.mutate(ctx, input.WorkflowID, "navigate", map[string]interface{}{
		"url": input.Url,
	}, sqldb.BrowserMutationContext{
		URL:        input.Url,
		ReplaySafe: input.Url != "",
	}, "", func() error {
		return a.client.Navigate(ctx, applicationBrowserID(input.WorkflowID), input.Url)
	})
}

func (a *Activity) ClosePage(ctx context.Context, input ClosePageInput) error {
	return a.client.CloseBrowser(ctx, applicationBrowserID(input.WorkflowID))
}

type UploadFileInput struct {
	WorkflowID     string                       `json:"workflow_id"`
	FilePath       string                       `json:"file_path"`
	FileInputIndex int                          `json:"file_input_index"`
	Target         *sqldb.BrowserMutationTarget `json:"target,omitempty"`
}

func (a *Activity) UploadFile(ctx context.Context, input UploadFileInput) error {
	return a.mutate(ctx, input.WorkflowID, "upload_file", map[string]interface{}{
		"file_input_index": input.FileInputIndex,
		"file_path":        input.FilePath,
	}, sqldb.BrowserMutationContext{
		FileInputIndex: &input.FileInputIndex,
		Target:         input.Target,
		ReplaySafe:     isReplaySafeFileTarget(input.Target),
	}, "", func() error {
		return a.client.UploadFile(ctx, applicationBrowserID(input.WorkflowID), input.FileInputIndex, input.FilePath)
	})
}

func (a *Activity) mutate(ctx context.Context, workflowID, operation string, arguments interface{}, mutationContext sqldb.BrowserMutationContext, idempotencySuffix string, execute func() error) error {
	if a.store == nil || isExcludedFromReplay(operation, mutationContext.Target) {
		return execute()
	}
	browserID, err := uuid.Parse(workflowID)
	if err != nil {
		return fmt.Errorf("record %s browser mutation: parse application browser ID: %w", operation, err)
	}
	activityID := sdkactivity.GetInfo(ctx).ActivityID
	if activityID == "" {
		return fmt.Errorf("record %s browser mutation: Temporal activity ID is unavailable", operation)
	}
	if idempotencySuffix == "" {
		idempotencySuffix = activityID
	}
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("record %s browser mutation arguments: %w", operation, err)
	}
	mutation, created, err := a.store.RecordBrowserMutation(ctx, browserID, operation, argumentsJSON, mutationContext, browserID.String()+"/"+idempotencySuffix)
	if err != nil {
		return fmt.Errorf("record %s browser mutation: %w", operation, err)
	}
	if !created {
		if mutation.Status == sqldb.BrowserMutationApplied {
			return nil
		}
		if mutation.Status == sqldb.BrowserMutationFailed {
			if err := a.store.PrepareBrowserMutationRetry(ctx, mutation.IdBrowserMutationChangelog); err != nil {
				return fmt.Errorf("prepare %s browser mutation retry: %w", operation, err)
			}
		} else if mutation.Status == sqldb.BrowserMutationPending || mutation.Status == sqldb.BrowserMutationReconcileRequired {
			applied, reconcileErr := a.reconcileBrowserMutation(ctx, workflowID, operation, mutation, argumentsJSON)
			if reconcileErr == nil && applied {
				if err := a.store.MarkBrowserMutationApplied(ctx, mutation.IdBrowserMutationChangelog, &sqldb.BrowserMutationResult{
					Outcome:    sqldb.BrowserMutationOutcomeApplied,
					ReplaySafe: mutation.Context.ReplaySafe,
				}); err != nil {
					return fmt.Errorf("mark reconciled %s browser mutation applied: %w", operation, err)
				}
				return nil
			}
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("%s browser mutation outcome remains uncertain and requires reconciliation", operation),
				"BrowserMutationReconciliationRequired",
				reconcileErr,
			)
		} else {
			return temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("%s browser mutation has an unsupported status and requires reconciliation", operation),
				"BrowserMutationReconciliationRequired",
				nil,
			)
		}
	}
	if err := execute(); err != nil {
		var notExecuted browsertype.MutationNotExecutedError
		statusErr := error(nil)
		if errors.As(err, &notExecuted) {
			statusErr = a.store.MarkBrowserMutationFailed(ctx, mutation.IdBrowserMutationChangelog, &sqldb.BrowserMutationResult{
				Outcome: sqldb.BrowserMutationOutcomeFailed,
			})
		} else {
			statusErr = a.store.MarkBrowserMutationReconcileRequired(ctx, mutation.IdBrowserMutationChangelog, &sqldb.BrowserMutationResult{
				Outcome: sqldb.BrowserMutationOutcomeUncertain,
			})
		}
		return errors.Join(fmt.Errorf("%s browser operation: %w", operation, err), statusErr)
	}
	result := &sqldb.BrowserMutationResult{
		Outcome:    sqldb.BrowserMutationOutcomeApplied,
		ReplaySafe: mutation.Context.ReplaySafe,
	}
	if err := a.store.MarkBrowserMutationApplied(ctx, mutation.IdBrowserMutationChangelog, result); err != nil {
		return fmt.Errorf("mark %s browser mutation applied: %w", operation, err)
	}
	return nil
}

func (a *Activity) reconcileBrowserMutation(ctx context.Context, workflowID, operation string, mutation sqldb.BrowserMutationChangelog, arguments []byte) (bool, error) {
	screenshot, err := a.client.ScreenshotForLLM(ctx, applicationBrowserID(workflowID), "browser_mutation_reconciliation.png")
	if err != nil {
		return false, fmt.Errorf("inspect browser state: %w", err)
	}

	var args map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &args); err != nil {
		return false, fmt.Errorf("decode mutation arguments for reconciliation: %w", err)
	}

	switch operation {
	case "navigate":
		var targetURL string
		if err := json.Unmarshal(args["url"], &targetURL); err != nil {
			return false, fmt.Errorf("decode navigation URL for reconciliation: %w", err)
		}
		return targetURL != "" && screenshot.CurrentURL == targetURL, nil
	case "input_text", "input_multiple":
		if mutation.Context.Target == nil || strings.EqualFold(mutation.Context.Target.Role, "password") {
			return false, nil
		}
		var text string
		if err := json.Unmarshal(args["text"], &text); err != nil {
			return false, fmt.Errorf("decode input text for reconciliation: %w", err)
		}
		index, err := resolveTaggedTarget(screenshot.TaggedNodes, mutation.Context.Target)
		if err != nil {
			return false, nil
		}
		for _, node := range screenshot.TaggedNodes {
			if node.Index == index {
				return node.Value != nil && *node.Value == text, nil
			}
		}
	}
	return false, nil
}

func isReplaySafeTarget(operation string, target *sqldb.BrowserMutationTarget) bool {
	if operation != "click" && operation != "input_text" {
		return false
	}
	if target == nil || target.Role == "" || target.Name == "" && target.Label == "" {
		return false
	}
	if strings.EqualFold(target.Role, "password") || target.Submit {
		return false
	}
	return !looksLikeSubmitTarget(target)
}

func isReplaySafeFileTarget(target *sqldb.BrowserMutationTarget) bool {
	return target != nil && strings.EqualFold(target.Role, "file") && (target.Name != "" || target.Label != "")
}

func isExcludedFromReplay(operation string, target *sqldb.BrowserMutationTarget) bool {
	return operation == "click" && looksLikeSubmitTarget(target)
}

func looksLikeSubmitTarget(target *sqldb.BrowserMutationTarget) bool {
	if target == nil {
		return false
	}
	if target.Submit {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(target.Name + " " + target.Label))
	return text == "submit" || strings.Contains(text, "submit application") || text == "apply" ||
		text == "apply now" || strings.Contains(text, "send application")
}
