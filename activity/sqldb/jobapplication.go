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
	JobApplicationStatusFailed  JobApplicationStatus = "failed"
	JobApplicationStatusBlocked JobApplicationStatus = "blocked"
)

type JobApplication struct {
	IdJobApplication uint                 `gorm:"primaryKey;autoIncrement;column:id_job_application" json:"_"`
	IdExternal       uuid.UUID            `gorm:"type:text;not null;unique" json:"id"`
	Status           JobApplicationStatus `gorm:"type:varchar(50);not null"`
	Url              string               `gorm:"not null;unique"`
	JobTitle         string               `gorm:"column:job_title" json:"job_title"`
	CompanyName      string               `gorm:"column:company_name" json:"company_name"`
	JobDescription   string               `gorm:"column:job_description" json:"job_description"`
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
