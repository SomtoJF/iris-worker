package browser

import (
	"context"
)

func (a *Activity) ClickSubmitAndCapture(ctx context.Context, input ClickSubmitInput) (ClickSubmitOutput, error) {
	attempt, err := a.client.ClickSubmit(ctx, applicationBrowserID(input.WorkflowID), input.ElementIndex)
	if err != nil {
		return ClickSubmitOutput{}, err
	}
	out := ClickSubmitOutput{
		BeforeURL:    attempt.BeforeURL,
		NewTabOpened: attempt.NewTabOpened,
		Requests:     make([]CapturedRequest, len(attempt.Requests)),
	}
	for i, request := range attempt.Requests {
		out.Requests[i] = CapturedRequest{
			URL: request.URL, Method: request.Method, ResourceType: request.ResourceType,
			StatusCode: request.StatusCode, ResponseBody: request.ResponseBody,
		}
	}
	return out, nil
}

func (a *Activity) VerifySubmissionState(ctx context.Context, input VerifySubmissionStateInput) (VerifySubmissionStateOutput, error) {
	state, err := a.client.VerifySubmission(ctx, applicationBrowserID(input.WorkflowID), input.BeforeURL)
	if err != nil {
		return VerifySubmissionStateOutput{}, err
	}
	return VerifySubmissionStateOutput{
		CurrentURL: state.CurrentURL, URLChanged: state.URLChanged, FormPresent: state.FormPresent,
		SuccessText: state.SuccessText, ValidationErrors: state.ValidationErrors, PageText: state.PageText,
	}, nil
}
