package model

import (
	"time"

	"github.com/google/uuid"
)

type JobApplicationStatus string

const (
	JobApplicationStatusPending JobApplicationStatus = "processing"
	JobApplicationStatusApplied JobApplicationStatus = "applied"
	JobApplicationStatusFailed    JobApplicationStatus = "failed"
	JobApplicationStatusBlocked   JobApplicationStatus = "blocked"
	JobApplicationStatusCancelled JobApplicationStatus = "cancelled"
)

type JobApplication struct {
	IdJobApplication   uint                 `gorm:"primaryKey;autoIncrement;column:id_job_application" json:"_"`
	IdExternal         uuid.UUID            `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId             uint                 `gorm:"column:id_user;not null"`
	User               User                 `gorm:"foreignKey:UserId;references:IdUser"`
	JobApplicationData *JobApplicationData  `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	Status             JobApplicationStatus `gorm:"type:varchar(50);not null"`
	WorkflowID         *string              `gorm:"type:text;default:NULL"`

	FailureReason      *string    `gorm:"type:text;default:NULL"`
	CancellationReason *string    `gorm:"type:text;default:NULL"`
	JobTitle       string     `gorm:"type:varchar(255);not null"`
	CompanyName    string     `gorm:"type:varchar(255);not null"`
	JobDescription string     `gorm:"type:text;not null"`
	Url            string     `gorm:"not null"`
	CreatedAt      time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt      time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt      *time.Time `gorm:"index;default:NULL"`
}

func (JobApplication) TableName() string {
	return "job_application"
}
