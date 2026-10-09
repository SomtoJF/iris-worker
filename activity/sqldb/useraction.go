package sqldb

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ====== TYPES ======

// ====== ACTIVITIES ======

type CreateUserActionInput struct {
	WorkflowID       string           `json:"workflow_id"`
	UserId           uint             `json:"id_user"`
	JobApplicationId uint             `json:"id_job_application"`
	UserActionType   string           `json:"user_action_type"`
	ActionDetails    string           `json:"action_details"`
	Layout           UserActionLayout `json:"layout"`
}

func (a *Activity) CreateUserAction(ctx context.Context, input CreateUserActionInput) (UserAction, error) {
	record := UserAction{
		WorkflowID:       input.WorkflowID,
		UserId:           input.UserId,
		JobApplicationId: input.JobApplicationId,
		UserActionType:   UserActionType(input.UserActionType),
		ActionDetails:    input.ActionDetails,
		UserActionLayout: input.Layout,
		IsPending:        true,
	}
	if err := a.db.Create(&record).Error; err != nil {
		return UserAction{}, err
	}
	return record, nil
}

type CreateDurableUserActionInput struct {
	CreateUserActionInput
	ApplicationBrowserID string `json:"application_browser_id"`
}

func (a *Activity) CreateDurableUserAction(ctx context.Context, input CreateDurableUserActionInput) (UserAction, error) {
	browserID, err := uuid.Parse(input.ApplicationBrowserID)
	if err != nil || browserID == uuid.Nil {
		return UserAction{}, fmt.Errorf("create user action requires a valid application browser ID")
	}
	var session BrowserSession
	if err := a.db.WithContext(ctx).Where("application_browser_id = ?", browserID).First(&session).Error; err != nil {
		return UserAction{}, fmt.Errorf("load browser session for user action: %w", err)
	}
	if session.Provider == BrowserProviderKernel {
		store := NewBrowserStore(a.db)
		cursor, found, err := store.LatestBrowserMutationCursor(ctx, browserID, session.ReplayGeneration)
		if err != nil {
			return UserAction{}, fmt.Errorf("load user-action mutation cursor: %w", err)
		}
		if found {
			if err := store.SetPendingBrowserCheckpoint(ctx, browserID, cursor.CreatedAt, cursor.MutationID); err != nil {
				return UserAction{}, fmt.Errorf("set pending user-action checkpoint: %w", err)
			}
		}
	}
	record := UserAction{
		WorkflowID:           input.WorkflowID,
		ApplicationBrowserID: browserID,
		ReplayGeneration:     session.ReplayGeneration,
		CheckpointCreatedAt:  session.CheckpointCreatedAt,
		CheckpointMutationID: session.CheckpointMutationID,
		UserId:               input.UserId,
		JobApplicationId:     input.JobApplicationId,
		UserActionType:       UserActionType(input.UserActionType),
		ActionDetails:        input.ActionDetails,
		UserActionLayout:     input.Layout,
		IsPending:            true,
		DurablePause:         true,
	}
	if err := a.db.WithContext(ctx).Create(&record).Error; err != nil {
		return UserAction{}, err
	}
	return record, nil
}

type CommitUserActionCheckpointInput struct {
	IdUserAction uint `json:"id_user_action"`
}

func (a *Activity) CommitUserActionCheckpoint(ctx context.Context, input CommitUserActionCheckpointInput) error {
	if input.IdUserAction == 0 {
		return fmt.Errorf("user action ID is required")
	}
	return a.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var action UserAction
		if err := tx.Where("id_user_action = ? AND durable_pause = ?", input.IdUserAction, true).First(&action).Error; err != nil {
			return fmt.Errorf("load durable user action checkpoint: %w", err)
		}
		if action.ApplicationBrowserID == uuid.Nil {
			return fmt.Errorf("durable user action has no application browser ID")
		}
		var session BrowserSession
		if err := tx.Where("application_browser_id = ?", action.ApplicationBrowserID).First(&session).Error; err != nil {
			return fmt.Errorf("load closed browser session for user action: %w", err)
		}
		if session.Status != BrowserSessionClosed {
			return fmt.Errorf("browser session must be closed before committing user-action checkpoint")
		}
		if session.PendingCheckpointCreatedAt != nil || session.PendingCheckpointMutationID != nil {
			return fmt.Errorf("browser session has an uncommitted user-action checkpoint")
		}
		return tx.Model(&UserAction{}).Where("id_user_action = ?", input.IdUserAction).Updates(map[string]any{
			"checkpoint_created_at":  session.CheckpointCreatedAt,
			"checkpoint_mutation_id": session.CheckpointMutationID,
		}).Error
	})
}

type SubmittedUserActionInput struct {
	IdUserAction uint `json:"id_user_action"`
}

type SubmittedUserAction struct {
	IdExternal uuid.UUID `json:"id_external"`
	Ciphertext []byte    `json:"ciphertext"`
}

func (a *Activity) GetSubmittedUserAction(ctx context.Context, input SubmittedUserActionInput) (SubmittedUserAction, error) {
	var action UserAction
	if err := a.db.WithContext(ctx).Where("id_user_action = ? AND submitted_at IS NOT NULL", input.IdUserAction).First(&action).Error; err != nil {
		return SubmittedUserAction{}, err
	}
	if len(action.ResultCiphertext) == 0 {
		return SubmittedUserAction{}, fmt.Errorf("submitted user action has no protected result")
	}
	if action.ApplicationBrowserID == uuid.Nil || action.ReplayGeneration == 0 {
		return SubmittedUserAction{}, fmt.Errorf("submitted user action has no browser replay checkpoint")
	}
	var session BrowserSession
	if err := a.db.WithContext(ctx).Where("application_browser_id = ?", action.ApplicationBrowserID).First(&session).Error; err != nil {
		return SubmittedUserAction{}, fmt.Errorf("load browser session for user action resume: %w", err)
	}
	if session.ReplayGeneration != action.ReplayGeneration {
		return SubmittedUserAction{}, fmt.Errorf("user action replay generation no longer matches browser session")
	}
	return SubmittedUserAction{IdExternal: action.IdExternal, Ciphertext: append([]byte(nil), action.ResultCiphertext...)}, nil
}

func DecryptUserActionResult(ciphertext []byte, actionID uuid.UUID) (string, error) {
	return decryptBrowserSecret(ciphertext, actionID)
}

func EncryptUserActionResult(plaintext string, actionID uuid.UUID) ([]byte, error) {
	return encryptBrowserSecret(plaintext, actionID)
}

type UpdateUserActionInput struct {
	IdUserAction uint                   `json:"id_user_action"`
	Data         map[string]interface{} `json:"data"`
}

func (a *Activity) UpdateUserAction(ctx context.Context, input UpdateUserActionInput) error {
	return a.db.Model(&UserAction{}).Where("id_user_action = ?", input.IdUserAction).Updates(input.Data).Error
}

type DeletePendingUserActionsInput struct {
	JobApplicationId uint `json:"id_job_application"`
}

func (a *Activity) DeletePendingUserActions(ctx context.Context, input DeletePendingUserActionsInput) error {
	return a.db.Where("id_job_application = ? AND is_pending = ?", input.JobApplicationId, true).Delete(&UserAction{}).Error
}
