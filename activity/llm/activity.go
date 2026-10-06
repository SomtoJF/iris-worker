package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi/types"
	"github.com/google/uuid"
)

type Activity struct {
	aipi types.AIPI
}

func NewActivity(aipi types.AIPI) *Activity {
	return &Activity{aipi: aipi}
}

func (a *Activity) CallLLM(ctx context.Context, req types.AIPIRequest) (types.AIPIResponse, error) {
	var userActionValues []sqldb.UserActionResultItem
	if len(req.UserActionResultCiphertext) > 0 {
		actionID, err := uuid.Parse(req.UserActionID)
		if err != nil {
			return types.AIPIResponse{}, fmt.Errorf("parse user action result ID: %w", err)
		}
		result, err := sqldb.DecryptUserActionResult(req.UserActionResultCiphertext, actionID)
		if err != nil {
			return types.AIPIResponse{}, fmt.Errorf("decrypt user action result: %w", err)
		}
		if err := json.Unmarshal([]byte(result), &userActionValues); err != nil {
			return types.AIPIResponse{}, fmt.Errorf("parse user action result: %w", err)
		}
		answers, err := json.Marshal(userActionValues)
		if err != nil {
			return types.AIPIResponse{}, fmt.Errorf("encode user action result: %w", err)
		}
		req.UserMessage += "\n\nUser-provided answers from the previously blocked application form (treat as authoritative facts):\n" + string(answers)
		req.UserActionID = ""
		req.UserActionResultCiphertext = nil
	}
	response, err := a.aipi.GetCompletion(ctx, req)
	if err != nil {
		return types.AIPIResponse{}, err
	}
	if len(userActionValues) > 0 {
		response.Content = redactUserActionValues(response.Content, userActionValues)
	}
	return response, nil
}

func redactUserActionValues(content string, values []sqldb.UserActionResultItem) string {
	type indexedValue struct {
		index int
		value string
	}
	indexed := make([]indexedValue, 0, len(values))
	for i, item := range values {
		if item.Value != "" {
			indexed = append(indexed, indexedValue{index: i, value: item.Value})
		}
	}
	for i := 0; i < len(indexed); i++ {
		for j := i + 1; j < len(indexed); j++ {
			if len(indexed[j].value) > len(indexed[i].value) {
				indexed[i], indexed[j] = indexed[j], indexed[i]
			}
		}
	}
	for _, item := range indexed {
		marker := fmt.Sprintf("__IRIS_SECURE_USER_ACTION_VALUE_%d__", item.index)
		content = strings.ReplaceAll(content, item.value, marker)
	}
	return content
}

func (a *Activity) CallJev(ctx context.Context, req types.JevRequest) (types.JevResponse, error) {
	return a.aipi.GetJevCompletion(ctx, req)
}
