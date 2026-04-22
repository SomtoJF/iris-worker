package shared

// UserAction is the action the user needs to take when the workflow is blocked.
type UserAction string

const (
	UserActionAdditionalInfo = "USER_ACTION_ADDITIONAL_INFO"
	UserActionOTP            = "USER_ACTION_OTP"
)
