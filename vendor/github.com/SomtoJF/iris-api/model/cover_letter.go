package model

import (
	"time"

	"github.com/google/uuid"
)

type CoverLetterStatus string

const (
	CoverLetterStatusProcessing CoverLetterStatus = "processing"
	CoverLetterStatusReady      CoverLetterStatus = "ready"
	CoverLetterStatusFailed     CoverLetterStatus = "failed"
)

type CoverLetter struct {
	IdCoverLetter    uint              `gorm:"primaryKey;autoIncrement;column:id_cover_letter" json:"_"`
	IdExternal       uuid.UUID         `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId           uint              `gorm:"column:id_user;not null;index"`
	User             User              `gorm:"foreignKey:UserId;references:IdUser"`
	ResumeId         uint              `gorm:"column:id_resume;not null"`
	Resume           Resume            `gorm:"foreignKey:ResumeId;references:IdResume"`
	JobApplicationId *uint             `gorm:"column:id_job_application;uniqueIndex"`
	JobApplication   *JobApplication   `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	Status           CoverLetterStatus `gorm:"type:varchar(50);not null;index"`
	Body             *string           `gorm:"type:text"`
	JobTitle         string            `gorm:"type:varchar(255);not null"`
	CompanyName      string            `gorm:"type:varchar(255);not null"`
	JobDescription   string            `gorm:"type:text;not null"`
	Url              string            `gorm:"not null"`
	WorkflowID       *string           `gorm:"column:workflow_id;type:text;default:NULL"`
	FailureReason    *string           `gorm:"type:text;default:NULL"`
	CreatedAt        time.Time         `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time         `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt        *time.Time        `gorm:"index;default:NULL"`
}

func (CoverLetter) TableName() string {
	return "cover_letter"
}
