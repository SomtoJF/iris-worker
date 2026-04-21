package sqldb

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Issue struct {
	IdIssue          uint            `gorm:"primaryKey;autoIncrement;column:id_issue" json:"_"`
	IdExternal       uuid.UUID       `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	Title            string          `gorm:"not null"`
	Type             string          `gorm:"type:text;not null"`
	UserId           uint            `gorm:"column:id_user;not null"`
	JobApplicationId *uint           `gorm:"column:id_job_application;default:NULL"`
	ContentJSON      json.RawMessage `gorm:"type:jsonb;not null" json:"content_json"`
	ContentText      string          `gorm:"not null"`
	Summary          string          `gorm:"not null"`
	CreatedAt        time.Time       `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time       `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt        *time.Time      `gorm:"index;default:NULL"`
}

func (Issue) TableName() string {
	return "issue"
}

func (a *Activity) GetIssueByExternalID(ctx context.Context, issueExternalID string) (Issue, error) {
	id, err := uuid.Parse(issueExternalID)
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	if err := a.db.Model(&Issue{}).Where("id_external = ?", id).First(&issue).Error; err != nil {
		return Issue{}, err
	}
	return issue, nil
}

type UpdateIssueInput struct {
	IssueExternalID string                 `json:"issue_external_id"`
	Data            map[string]interface{} `json:"data"`
}

func (a *Activity) UpdateIssue(ctx context.Context, input UpdateIssueInput) error {
	id, err := uuid.Parse(input.IssueExternalID)
	if err != nil {
		return err
	}
	return a.db.Model(&Issue{}).Where("id_external = ?", id).Updates(input.Data).Error
}

