package model

import (
	"time"

	"github.com/google/uuid"
)

type JobApplicationStatus string

const (
	// currently processing the job application
	JobApplicationStatusProcessing JobApplicationStatus = "processing"
	// successfully applied to the job
	JobApplicationStatusApplied JobApplicationStatus = "applied"
	// failed to apply to the job
	JobApplicationStatusFailed JobApplicationStatus = "failed"
	// user needs to perform an action to continue the job application
	JobApplicationStatusBlocked JobApplicationStatus = "blocked"
	// cancelled by the user
	JobApplicationStatusCancelled JobApplicationStatus = "cancelled"
	// job application was halted by the system for ethical reasons
	JobApplicationStatusHalted JobApplicationStatus = "halted"
)

type JobApplication struct {
	IdJobApplication   uint                 `gorm:"primaryKey;autoIncrement;column:id_job_application" json:"_"`
	IdExternal         uuid.UUID            `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId             uint                 `gorm:"column:id_user;not null;index"`
	User               User                 `gorm:"foreignKey:UserId;references:IdUser"`
	ResumeId           uint                 `gorm:"column:id_resume;not null"`
	Resume             Resume               `gorm:"foreignKey:ResumeId;references:IdResume"`
	JobApplicationData *JobApplicationData  `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	Status             JobApplicationStatus `gorm:"type:varchar(50);not null;index"`
	WorkflowID         *string              `gorm:"type:text;default:NULL"`

	FailureReason      *string    `gorm:"type:text;default:NULL"`
	CancellationReason *string    `gorm:"type:text;default:NULL"`
	HaltReason         *string    `gorm:"type:text;default:NULL"`
	JobTitle           string     `gorm:"type:varchar(255);not null"`
	CompanyName        string     `gorm:"type:varchar(255);not null"`
	JobDescription     string     `gorm:"type:text;not null"`
	Url                string     `gorm:"not null"`
	CoverLetterOnly    bool       `gorm:"default:false"`
	AppliedAt          *time.Time `gorm:"default:NULL"`
	CreatedAt          time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt          time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt          *time.Time `gorm:"index;default:NULL"`
}

func (JobApplication) TableName() string {
	return "job_application"
}
