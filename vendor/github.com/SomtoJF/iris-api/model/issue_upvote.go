package model

import (
	"time"

	"github.com/google/uuid"
)

type IssueUpvote struct {
	IdIssueUpvote uint      `gorm:"primaryKey;autoIncrement;column:id_issue_upvote" json:"_"`
	IdExternal    uuid.UUID `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	IssueId       uint      `gorm:"column:id_issue;not null;uniqueIndex:idx_issue_upvote_issue_user"`
	Issue         Issue     `gorm:"foreignKey:IssueId;references:IdIssue"`
	UserId        uint      `gorm:"column:id_user;not null;uniqueIndex:idx_issue_upvote_issue_user"`
	User          User      `gorm:"foreignKey:UserId;references:IdUser"`
	CreatedAt     time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt     time.Time `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (IssueUpvote) TableName() string {
	return "issue_upvote"
}
