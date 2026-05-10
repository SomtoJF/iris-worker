package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type IssueComment struct {
	IdIssueComment uint                 `gorm:"primaryKey;autoIncrement;column:id_issue_comment" json:"_"`
	IdExternal     uuid.UUID            `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	IssueId        uint                 `gorm:"column:id_issue;not null"`
	Issue          Issue                `gorm:"foreignKey:IssueId;references:IdIssue"`
	UserId         uint                 `gorm:"column:id_user;not null"`
	User           User                 `gorm:"foreignKey:UserId;references:IdUser"`
	CommentJSON    json.RawMessage      `gorm:"type:jsonb;not null" json:"comment_json"`
	CommentText    string               `gorm:"not null"`
	Upvotes        []IssueCommentUpvote `gorm:"foreignKey:IssueCommentId;references:IdIssueComment"`
	CreatedAt      time.Time            `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt      time.Time            `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt      *time.Time           `gorm:"index;default:NULL"`
}

func (IssueComment) TableName() string {
	return "issue_comments"
}
