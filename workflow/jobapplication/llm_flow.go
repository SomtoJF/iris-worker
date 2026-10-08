package jobapplication

import (
	"fmt"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"go.temporal.io/sdk/workflow"
)

type agentLoopState struct {
	ctx             workflow.Context
	cancelCtx       workflow.Context
	sessionCtx      workflow.Context
	screenCtx       workflow.Context
	workflowID      string
	browserProvider string
	input           jobApplicationRuntimeInput
	jobDetails      *JobDetails
	session         *applicationSession
	result          *executeJobApplicationResult
	toolHistory     []ToolCallResult
	qaMap           map[string]string
	complete        bool
	paused          bool
}

// loopStep is what the planner sees for one iteration.
type loopStep struct {
	Screenshot     browser.TakeScreenshotOutput
	RequiredFields []browser.SerializableTaggedNode
}

// runLLMFlow hands all required fields to the planner.
func runLLMFlow(shot browser.TakeScreenshotOutput) *loopStep {
	return &loopStep{Screenshot: shot, RequiredFields: extractRequiredFields(shot.TaggedNodes)}
}

func (s *agentLoopState) buildPlannerRequest(step *loopStep) PlannerRequest {
	return PlannerRequest{
		IdUser:                     s.input.IdUser,
		IdJobApplication:           s.input.IdJobApplication,
		JobPostingUrl:              s.input.Url,
		ScreenshotPath:             step.Screenshot.Path,
		TaggedNodes:                step.Screenshot.TaggedNodes,
		TaggedFileInputElements:    step.Screenshot.TaggedFileInputNodes,
		ToolCallHistory:            s.toolHistory,
		UserResume:                 s.session.UserResume.Content,
		JobDescription:             s.jobDetails.JobDescription,
		UserResumePath:             s.session.ResumePath,
		UserProfileJSON:            s.session.UserProfileJSON,
		RequiredFields:             step.RequiredFields,
		CurrentDate:                workflow.Now(s.cancelCtx).Format("2006-01-02"),
		UserActionID:               s.session.UserActionResult.IdExternal.String(),
		UserActionResultCiphertext: s.session.UserActionResult.Ciphertext,
	}
}

func plannerFailureError(resp PlannerResponse) error {
	failureReason := "We couldn't complete your application. Please try again."
	if resp.FailureReason != nil && *resp.FailureReason != "" {
		failureReason = *resp.FailureReason
	}
	if resp.FailureStatus != nil && *resp.FailureStatus == PlannerFailureStatusTruthfulness {
		return newHaltedJobAppError(fmt.Errorf("%s", failureReason), "Job application halted by truthfulness clause", failureReason)
	}
	return newJobAppError(fmt.Errorf("%s", failureReason), "Job application failed by planner", failureReason)
}

func (s *agentLoopState) recordQuestionsAnswered(resp PlannerResponse) {
	for _, qa := range resp.QuestionsAnswered {
		if qa.Question != "" && qa.Answer != "" {
			s.qaMap[qa.Question] = qa.Answer
		}
	}
}

func (s *agentLoopState) runToolCall(step *loopStep, toolCall ToolCall) error {
	toolResult := executeToolCall(
		s.sessionCtx,
		s.workflowID,
		s.input.IdUser,
		s.input.IdJobApplication,
		toolCall,
		step.Screenshot.TaggedNodes,
		step.Screenshot.TaggedFileInputNodes,
		s.session.UserActionResult.IdExternal.String(),
		s.session.UserActionResult.Ciphertext,
	)
	s.toolHistory = append(s.toolHistory, toolResult)

	if paused, ok := toolResult.Result["user_action_paused"].(bool); ok && paused {
		s.paused = true
		s.result.UserActionPaused = true
		return nil
	}
	if toolResult.Error != nil && isCancelled(s.cancelCtx) {
		return toolResult.Error
	}
	if toolCall.Name == "write_cover_letter" {
		if cl, ok := toolResult.Result["cover_letter"].(string); ok {
			s.result.CoverLetter = &cl
		}
	}
	return nil
}

// planAndAct asks the planner for the next action and executes it.
func planAndAct(s *agentLoopState, step *loopStep) error {
	resp, err := planNextAction(s.cancelCtx, s.buildPlannerRequest(step))
	if err != nil {
		return newJobAppError(err, "Failed to plan next action", "We couldn't continue the application because we failed to plan the next step")
	}
	if resp.IsApplicationFailed {
		return plannerFailureError(resp)
	}

	s.recordQuestionsAnswered(resp)

	s.complete = resp.IsApplicationComplete
	if s.complete || resp.ToolCall == nil {
		return nil
	}
	return s.runToolCall(step, *resp.ToolCall)
}
