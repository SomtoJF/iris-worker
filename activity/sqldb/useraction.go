package sqldb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ====== TYPES ======

type UserActionLayoutItem struct {
	Type      *string   `json:"type"`
	FieldName string    `json:"field_name"`
	Component *string   `json:"component"`
	Options   *[]string `json:"options"`
}

type UserActionResultItem struct {
	FieldName string `json:"field_name"`
	Value     string `json:"value"`
}

type UserActionLayout []UserActionLayoutItem
type UserActionResult []UserActionResultItem

func (u *UserActionLayout) Scan(value interface{}) error {
	if value == nil {
		*u = UserActionLayout{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*u = UserActionLayout{}
		return nil
	}
	if len(b) == 0 || (len(b) == 4 && (string(b) == "NULL" || string(b) == "null")) {
		*u = UserActionLayout{}
		return nil
	}
	if err := json.Unmarshal(b, (*[]UserActionLayoutItem)(u)); err != nil {
		*u = UserActionLayout{}
		return nil
	}
	return nil
}

func (u UserActionLayout) Value() (driver.Value, error) {
	if len(u) == 0 {
		return "[]", nil
	}
	return json.Marshal(u)
}

// ====== MODEL ======

type UserAction struct {
	IdUserAction     uint             `gorm:"primaryKey;autoIncrement;column:id_user_action" json:"_"`
	IdExternal       uuid.UUID        `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId           uint             `gorm:"column:id_user;not null"`
	User             User             `gorm:"foreignKey:UserId;references:IdUser"`
	JobApplicationId uint             `gorm:"column:id_job_application;not null"`
	JobApplication   JobApplication   `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	UserActionType   string           `gorm:"type:text;not null"`
	ActionDetails    string           `gorm:"type:text;not null"`
	UserActionLayout UserActionLayout `gorm:"type:jsonb;not null"`
	WorkflowID       string           `gorm:"type:text"`
	IsPending        bool             `gorm:"default:true"`
	CreatedAt        time.Time        `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time        `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (UserAction) TableName() string {
	return "user_action"
}

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
		UserActionType:   input.UserActionType,
		ActionDetails:    input.ActionDetails,
		UserActionLayout: input.Layout,
		IsPending:        true,
	}
	if err := a.db.Create(&record).Error; err != nil {
		return UserAction{}, err
	}
	return record, nil
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
