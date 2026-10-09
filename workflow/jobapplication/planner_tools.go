package jobapplication

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/SomtoJF/iris-worker/aipi/types"
)

const (
	toolCompleteApplication = "complete_application"
	toolFailApplication     = "fail_application"
)

// plannerSharedProperties are required on every planner tool so each step reports
// its reasoning and the form Q&A it observed, then stripped before execution.
func plannerSharedProperties() map[string]interface{} {
	return map[string]interface{}{
		"reasoning": map[string]interface{}{
			"type":        "string",
			"description": "Brief explanation of the decision and next action",
		},
		"questions_answered": map[string]interface{}{
			"type":        "array",
			"description": "All application question/answer pairs currently visible as filled in tagged_nodes and required_elements. Only include actual application form questions, not buttons, navigation or cookies. Report what is already filled, not what you are about to fill.",
			"items": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question": map[string]interface{}{"type": "string", "description": "The form field label or question text"},
					"answer":   map[string]interface{}{"type": "string", "description": "The value filled in the field"},
				},
				"required": []string{"question", "answer"},
			},
		},
	}
}

func plannerToolDefinition(name, description string, schema map[string]interface{}) types.ToolDefinition {
	properties := map[string]interface{}{}
	required := []string{}
	if existing, ok := schema["properties"].(map[string]interface{}); ok {
		for key, value := range existing {
			properties[key] = value
		}
	}
	if existing, ok := schema["required"].([]string); ok {
		required = append(required, existing...)
	}
	for key, value := range plannerSharedProperties() {
		properties[key] = value
		required = append(required, key)
	}
	parameters := withNoAdditionalProperties(map[string]interface{}{
		"type":       "object",
		"properties": properties,
		"required":   required,
	})
	return types.ToolDefinition{Name: name, Description: description, Parameters: parameters, Strict: true}
}

// plannerTools returns the native tool definitions offered to the planner model.
func plannerTools() []types.ToolDefinition {
	names := make([]string, 0, len(toolRequestStructureMap))
	for name := range toolRequestStructureMap {
		if name == "submit_application" {
			continue // submission is decided and executed by the workflow, not the planner
		}
		names = append(names, name)
	}
	sort.Strings(names)

	tools := make([]types.ToolDefinition, 0, len(names)+2)
	for _, name := range names {
		tools = append(tools, plannerToolDefinition(name, toolItemMap[name].Description, toolRequestStructureMap[name]))
	}
	tools = append(tools,
		plannerToolDefinition(toolCompleteApplication,
			"Call only when the application is already confirmed submitted (e.g. a thank-you/confirmation page) or you landed on a payment, job board or external page. Submission itself is handled automatically.",
			map[string]interface{}{}),
		plannerToolDefinition(toolFailApplication,
			"Absolute last resort: call only when progress is impossible or disallowed (CAPTCHA, login wall, truthfulness contradiction, unrecoverable submission error). Never use when the user can supply the missing input via handle_user_action.",
			map[string]interface{}{
				"properties": map[string]interface{}{
					"failure_reason": map[string]interface{}{
						"type":        "string",
						"description": "ONE concise, user-friendly sentence. Must not mention model limitations, tools or internal shortcomings.",
					},
					"failure_status": map[string]interface{}{
						"type": "string",
						"enum": []string{
							string(PlannerFailureStatusCaptcha),
							string(PlannerFailureStatusTruthfulness),
							string(PlannerFailureStatusLoginRequired),
							string(PlannerFailureStatusSubmissionError),
							string(PlannerFailureStatusOther),
						},
					},
				},
				"required": []string{"failure_reason", "failure_status"},
			}),
	)
	return tools
}

// plannerResponseFromToolCalls converts the model's native tool call into a PlannerResponse.
func plannerResponseFromToolCalls(calls []types.ToolCall) (PlannerResponse, error) {
	if len(calls) == 0 {
		return PlannerResponse{}, fmt.Errorf("planner returned no tool call")
	}
	call := calls[0]

	arguments := map[string]interface{}{}
	if call.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
			return PlannerResponse{}, fmt.Errorf("parse arguments for tool %q: %w", call.Name, err)
		}
	}

	var response PlannerResponse
	if reasoning, ok := arguments["reasoning"].(string); ok {
		response.Reasoning = reasoning
	}
	if raw, ok := arguments["questions_answered"]; ok {
		encoded, err := json.Marshal(raw)
		if err != nil {
			return PlannerResponse{}, err
		}
		if err := json.Unmarshal(encoded, &response.QuestionsAnswered); err != nil {
			return PlannerResponse{}, fmt.Errorf("parse questions_answered: %w", err)
		}
	}
	delete(arguments, "reasoning")
	delete(arguments, "questions_answered")

	switch call.Name {
	case toolCompleteApplication:
		response.IsApplicationComplete = true
	case toolFailApplication:
		response.IsApplicationFailed = true
		if reason, ok := arguments["failure_reason"].(string); ok {
			response.FailureReason = &reason
		}
		if status, ok := arguments["failure_status"].(string); ok {
			failureStatus := PlannerFailureStatus(status)
			response.FailureStatus = &failureStatus
		}
	default:
		if _, ok := toolItemMap[call.Name]; !ok {
			return PlannerResponse{}, fmt.Errorf("planner called unknown tool %q", call.Name)
		}
		response.ToolCall = &ToolCall{Name: call.Name, Arguments: arguments}
	}
	return response, nil
}
