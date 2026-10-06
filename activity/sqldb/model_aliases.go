package sqldb

import "github.com/SomtoJF/iris-api/model"

type BrowserProvider = model.BrowserProvider
type BrowserProfile = model.BrowserProfile
type BrowserVault = model.BrowserVault
type BrowserAuthConnectionStatus = model.BrowserAuthConnectionStatus
type BrowserAuthConnection = model.BrowserAuthConnection
type BrowserSessionStatus = model.BrowserSessionStatus
type BrowserSession = model.BrowserSession
type BrowserReplayAttempt = model.BrowserReplayAttempt
type BrowserMutationStatus = model.BrowserMutationStatus
type BrowserMutationChangelog = model.BrowserMutationChangelog
type BrowserMutationTarget = model.BrowserMutationTarget
type BrowserMutationContext = model.BrowserMutationContext
type BrowserMutationOutcome = model.BrowserMutationOutcome
type BrowserMutationResult = model.BrowserMutationResult
type UserActionType = model.UserActionType
type UserActionLayoutItem = model.UserActionLayoutItem
type UserActionResultItem = model.UserActionResultItem
type UserActionLayout = model.UserActionLayout
type UserActionResult = model.UserActionResult
type UserAction = model.UserAction

const (
	BrowserProviderRod                    = model.BrowserProviderRod
	BrowserProviderKernel                 = model.BrowserProviderKernel
	BrowserAuthConnectionConnected        = model.BrowserAuthConnectionConnected
	BrowserAuthConnectionNeedsAuth        = model.BrowserAuthConnectionNeedsAuth
	BrowserAuthConnectionReauthenticating = model.BrowserAuthConnectionReauthenticating
	BrowserAuthConnectionDisconnected     = model.BrowserAuthConnectionDisconnected
	BrowserAuthConnectionFailed           = model.BrowserAuthConnectionFailed
	BrowserSessionStarting                = model.BrowserSessionStarting
	BrowserSessionActive                  = model.BrowserSessionActive
	BrowserSessionClosed                  = model.BrowserSessionClosed
	BrowserSessionExpired                 = model.BrowserSessionExpired
	BrowserSessionFailed                  = model.BrowserSessionFailed
	BrowserMutationPending                = model.BrowserMutationPending
	BrowserMutationApplied                = model.BrowserMutationApplied
	BrowserMutationFailed                 = model.BrowserMutationFailed
	BrowserMutationReconcileRequired      = model.BrowserMutationReconcileRequired
	BrowserMutationOutcomeApplied         = model.BrowserMutationOutcomeApplied
	BrowserMutationOutcomeFailed          = model.BrowserMutationOutcomeFailed
	BrowserMutationOutcomeUncertain       = model.BrowserMutationOutcomeUncertain
	UserActionTypeAdditionalInfo          = model.UserActionTypeAdditionalInfo
	UserActionTypeOTP                     = model.UserActionTypeOTP
)
