package shared

// UserAction is the action the user needs to take when the workflow is blocked.
type UserAction string

const (
	UserActionCaptcha        = "USER_ACTION_CAPTCHA"
	UserActionAuthentication = "USER_ACTION_AUTHENTICATION"
	UserActionOTP            = "USER_ACTION_OTP"
)
