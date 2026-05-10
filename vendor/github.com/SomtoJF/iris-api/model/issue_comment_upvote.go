package model

import (
	"time"

	"github.com/google/uuid"
)

type IssueCommentUpvote struct {
	IdIssueCommentUpvote uint         `gorm:"primaryKey;autoIncrement;column:id_issue_comment_upvote" json:"_"`
	IdExternal           uuid.UUID    `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	IssueCommentId       uint         `gorm:"column:id_issue_comment;not null;uniqueIndex:idx_issue_comment_upvote_issue_comment_user"`
	IssueComment         IssueComment `gorm:"foreignKey:IssueCommentId;references:IdIssueComment"`
	UserId               uint         `gorm:"column:id_user;not null;uniqueIndex:idx_issue_comment_upvote_issue_comment_user"`
	User                 User         `gorm:"foreignKey:UserId;references:IdUser"`
	CreatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (IssueCommentUpvote) TableName() string {
	return "issue_comment_upvote"
}
