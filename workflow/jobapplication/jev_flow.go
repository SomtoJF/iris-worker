package jobapplication

import (
	"fmt"

	"github.com/SomtoJF/iris-worker/activity/browser"
	"go.temporal.io/sdk/workflow"
)

// runJevFlow classifies fields with JEV and fills resume/structured ones deterministically.
// Returns nil step when JEV didn't help, so the caller falls back to the LLM flow.
func runJevFlow(s *agentLoopState, iteration int, shot browser.TakeScreenshotOutput) (*loopStep, error) {
	logger := workflow.GetLogger(s.sessionCtx)

	classified, err := classifyFieldsWithJev(
		s.sessionCtx, s.workflowID, s.input.IdUser, s.input.IdJobApplication,
		shot.TaggedNodes, shot.TaggedFileInputNodes, s.session.UserProfile,
	)
	if err != nil {
		logger.Warn("JEV field classification failed, falling back to LLM-only mode", "error", err)
		return nil, nil
	}

	fillResult, fillErr := fillDeterministicFields(
		s.sessionCtx, s.workflowID, s.input.IdUser, s.input.IdJobApplication,
		classified, s.session.UserProfile, s.session.ResumePath,
	)
	if fillErr != nil {
		logger.Warn("Deterministic fill encountered an error, continuing with LLM", "error", fillErr)
	}
	if fillResult == nil || (fillResult.ResumeFieldsFilled == 0 && fillResult.StructuredFieldsFilled == 0) {
		return nil, nil
	}

	logger.Info("Deterministic fields filled",
		"resume_filled", fillResult.ResumeFieldsFilled,
		"structured_filled", fillResult.StructuredFieldsFilled,
		"failed_structured", len(fillResult.FailedStructured),
	)

	newShot, err := takeScreenshot(s.sessionCtx, s.workflowID, fmt.Sprintf("screenshot_%d_postdeterministic.png", iteration))
	if err != nil {
		return nil, newJobAppError(err, "Failed to take screenshot after deterministic fill", "We couldn't continue the application because we failed to capture the page state")
	}

	openEnded := filterClassifiedFieldsForPlanner(classified)
	requiredFields := extractRequiredFieldsFromClassified(openEnded, newShot.TaggedNodes)
	logger.Info("Filtered required fields after deterministic fill", "count", len(requiredFields))

	return &loopStep{Screenshot: newShot, RequiredFields: requiredFields}, nil
}
