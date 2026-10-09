package jobapplication

import (
	"encoding/json"

	"github.com/SomtoJF/iris-worker/activity/sqldb"
	jobapplicationprofile "github.com/SomtoJF/iris-worker/workflow/jobapplication/profile"
	"go.temporal.io/sdk/workflow"
)

type applicationSession struct {
	UserResume       sqldb.Resume
	UserProfile      jobapplicationprofile.UserProfile
	UserProfileJSON  string
	ResumePath       string
	UserActionResult sqldb.SubmittedUserAction
}

// loadApplicationInputs fetches resume, profile and downloads the resume file.
func loadApplicationInputs(cancelCtx workflow.Context, input jobApplicationRuntimeInput) (*applicationSession, error) {
	userResume, err := fetchUserResume(cancelCtx, input.IdResume)
	if err != nil {
		return nil, newJobAppError(err, "Failed to fetch user resume", "An error occurred while fetching your resume")
	}

	userProfile, err := jobapplicationprofile.Fetch(cancelCtx, input.IdUser)
	if err != nil {
		return nil, newJobAppError(err, "Failed to fetch user profile", "An error occurred while fetching your profile")
	}

	resumePath, err := loadResumeIntoMemory(cancelCtx, userResume.FileName, userResume.FileKey)
	if err != nil {
		return nil, newJobAppError(err, "Failed to download and load resume into memory", "An error occurred while loading your resume into memory")
	}

	return &applicationSession{UserResume: userResume, UserProfile: userProfile, ResumePath: resumePath}, nil
}

// openApplicationPage opens the posting and replays prior browser mutations when needed.
func openApplicationPage(ctx, sessionCtx workflow.Context, workflowID string, input jobApplicationRuntimeInput) (string, error) {
	replayVersion := workflow.GetVersion(ctx, "browser-mutation-replay-catchup", workflow.DefaultVersion, 1)
	replayRequired := false
	provider := ""
	var openErr error
	if replayVersion == 1 {
		provider, replayRequired, openErr = openWebpageWithReplayStatus(sessionCtx, workflowID, input.Url)
	} else {
		provider, openErr = openWebpage(sessionCtx, workflowID, input.Url)
	}
	if openErr != nil {
		return "", newJobAppError(openErr, "Failed to open webpage", "We couldn't open the job posting page")
	}
	if replayVersion == 1 && replayRequired {
		if err := replayBrowserMutations(sessionCtx, workflowID, input.IdUser, input.IdJobApplication, input.Url); err != nil {
			return "", newJobAppError(err, "Failed to replay browser mutations", "We couldn't safely restore the application page")
		}
	}
	return provider, nil
}

// finishSessionSetup serializes the profile and loads a resumed user action, if any.
func finishSessionSetup(ctx workflow.Context, session *applicationSession, input jobApplicationRuntimeInput) error {
	profileBytes, err := json.Marshal(session.UserProfile)
	if err != nil {
		return newJobAppError(err, "Failed to marshal user profile", "We couldn't marshal the user profile")
	}
	session.UserProfileJSON = string(profileBytes)

	if input.ResumeUserActionID != 0 && input.DurableResumeVersion {
		if err := workflow.ExecuteActivity(ctx, "GetSubmittedUserAction", sqldb.SubmittedUserActionInput{
			IdUserAction: input.ResumeUserActionID,
		}).Get(ctx, &session.UserActionResult); err != nil {
			return newJobAppError(err, "Failed to load submitted user action", "We couldn't resume the application with your answers")
		}
	}
	return nil
}
