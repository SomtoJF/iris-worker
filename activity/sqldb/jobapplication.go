package sqldb

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Activity struct {
	db *gorm.DB
}

func NewActivities(db *gorm.DB) *Activity {
	return &Activity{db: db}
}

type UpdateJobApplicationInput struct {
	IdJobApplication uint                   `json:"id_job_application"`
	Data             map[string]interface{} `json:"data"`
}

// ====== MODELS ======

type JobApplicationStatus string

const (
	JobApplicationStatusPending JobApplicationStatus = "processing"
	JobApplicationStatusApplied JobApplicationStatus = "applied"
	JobApplicationStatusFailed    JobApplicationStatus = "failed"
	JobApplicationStatusBlocked   JobApplicationStatus = "blocked"
	JobApplicationStatusCancelled JobApplicationStatus = "cancelled"
)

type JobApplication struct {
	IdJobApplication uint                 `gorm:"primaryKey;autoIncrement;column:id_job_application" json:"_"`
	IdExternal       uuid.UUID            `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId           uint                 `gorm:"column:id_user;not null"`
	User             User                 `gorm:"foreignKey:UserId;references:IdUser"`
	Status           JobApplicationStatus `gorm:"type:varchar(50);not null"`
	JobTitle         string               `gorm:"type:varchar(255);not null"`
	CompanyName      string               `gorm:"type:varchar(255);not null"`
	JobDescription   string               `gorm:"type:text;not null"`
	Url              string               `gorm:"not null"`
	CreatedAt        time.Time            `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time            `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt        *time.Time           `gorm:"index;default:NULL"`
}

func (JobApplication) TableName() string {
	return "job_application"
}

func (a *Activity) UpdateJobApplication(ctx context.Context, input UpdateJobApplicationInput) error {
	if err := a.db.Model(&JobApplication{}).Where("id_job_application = ?", input.IdJobApplication).Updates(input.Data).Error; err != nil {
		return err
	}
	return nil
}

func (a *Activity) GetJobApplication(ctx context.Context, idJobApplication uint) (JobApplication, error) {
	var jobApplication JobApplication
	if err := a.db.Model(&JobApplication{}).Where("id_job_application = ?", idJobApplication).First(&jobApplication).Error; err != nil {
		return JobApplication{}, err
	}
	return jobApplication, nil
}
