package model

import (
	"time"

	"github.com/google/uuid"
)

type JobApplicationStatus string

const (
	// currently processing the job application
	JobApplicationStatusProcessing JobApplicationStatus = "processing"
	// request accepted no processing started yet
	JobApplicationStatusPending JobApplicationStatus = "pending"
	// started manually using extension but not completed
	JobApplicationStatusStarted JobApplicationStatus = "started"
	// queued by worker
	JobApplicationStatusQueued JobApplicationStatus = "queued"
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

type ResponseStatus string

const (
	ResponseStatusNone          ResponseStatus = "none"
	ResponseStatusRejected      ResponseStatus = "rejected"
	ResponseStatusInterviewing  ResponseStatus = "interviewing"
	ResponseStatusGhosted       ResponseStatus = "ghosted"
	ResponseStatusOffer         ResponseStatus = "offer"
	ResponseStatusOfferAccepted ResponseStatus = "offer_accepted"
)

type JobApplication struct {
	IdJobApplication      uint                 `gorm:"primaryKey;autoIncrement;column:id_job_application" json:"_"`
	IdExternal            uuid.UUID            `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId                uint                 `gorm:"column:id_user;not null;index"`
	User                  User                 `gorm:"foreignKey:UserId;references:IdUser"`
	ResumeId              uint                 `gorm:"column:id_resume;not null"`
	Resume                Resume               `gorm:"foreignKey:ResumeId;references:IdResume"`
	JobApplicationData    *JobApplicationData  `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	CoverLetter           *CoverLetter         `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	Status                JobApplicationStatus `gorm:"type:varchar(50);not null;index"`
	ResponseStatus        ResponseStatus       `gorm:"type:varchar(50);not null;index;default:none"`
	WorkflowID            *string              `gorm:"type:text;default:NULL"`
	AppliedUsingExtension bool                 `gorm:"not null;default:false"`

	FailureReason      *string    `gorm:"type:text;default:NULL"`
	CancellationReason *string    `gorm:"type:text;default:NULL"`
	HaltReason         *string    `gorm:"type:text;default:NULL"`
	JobTitle           string     `gorm:"type:varchar(255);not null"`
	CompanyName        string     `gorm:"type:varchar(255);not null"`
	JobDescription     string     `gorm:"type:text;not null"`
	Url                string     `gorm:"not null"`
	AppliedAt          *time.Time `gorm:"default:NULL"`
	CreatedAt          time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt          time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt          *time.Time `gorm:"index;default:NULL"`
}

func (JobApplication) TableName() string {
	return "job_application"
}
