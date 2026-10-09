package browser

import (
	"context"
)

func (a *Activity) DetectCaptcha(ctx context.Context, input DetectCaptchaInput) (DetectCaptchaOutput, error) {
	captcha, err := a.client.DetectCaptcha(ctx, applicationBrowserID(input.WorkflowID))
	if err != nil {
		return DetectCaptchaOutput{}, err
	}
	return DetectCaptchaOutput{
		Type: captcha.Type, SiteKey: captcha.SiteKey, PageURL: captcha.PageURL,
		Action: captcha.Action, Invisible: captcha.Invisible, Extra: captcha.Extra,
	}, nil
}

func (a *Activity) InjectCaptchaToken(ctx context.Context, input InjectCaptchaTokenInput) (InjectCaptchaTokenOutput, error) {
	result, err := a.client.InjectCaptchaToken(ctx, applicationBrowserID(input.WorkflowID), input.Type, input.Token)
	if err != nil {
		return InjectCaptchaTokenOutput{}, err
	}
	return InjectCaptchaTokenOutput{CallbackFired: result.CallbackFired}, nil
}

func (a *Activity) ClickCaptchaButton(ctx context.Context, input ClickCaptchaButtonInput) (ClickCaptchaButtonOutput, error) {
	clicked, err := a.client.ClickCaptchaButton(ctx, applicationBrowserID(input.WorkflowID), input.Selector)
	if err != nil {
		return ClickCaptchaButtonOutput{}, err
	}
	return ClickCaptchaButtonOutput{Clicked: clicked}, nil
}
