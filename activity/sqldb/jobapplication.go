package sqldb

import (
	"context"

	"github.com/SomtoJF/iris-api/model"
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

type JobApplication = model.JobApplication
type JobApplicationStatus = model.JobApplicationStatus

const (
	JobApplicationStatusPending   = model.JobApplicationStatusPending
	JobApplicationStatusApplied   = model.JobApplicationStatusApplied
	JobApplicationStatusFailed    = model.JobApplicationStatusFailed
	JobApplicationStatusBlocked   = model.JobApplicationStatusBlocked
	JobApplicationStatusCancelled = model.JobApplicationStatusCancelled
	JobApplicationStatusHalted    = model.JobApplicationStatusHalted
)

func (a *Activity) UpdateJobApplication(ctx context.Context, input UpdateJobApplicationInput) error {
	if err := a.db.Model(&JobApplication{}).Where("id_job_application = ?", input.IdJobApplication).Updates(input.Data).Error; err != nil {
		return err
	}
	return nil
}

type GetJobApplicationInput struct {
	IdJobApplication          uint `json:"id_job_application"`
	IncludeJobApplicationData bool `json:"include_job_application_data"`
}

func (a *Activity) GetJobApplication(ctx context.Context, input GetJobApplicationInput) (JobApplication, error) {
	var jobApplication JobApplication
	db := a.db.Model(&JobApplication{})
	if input.IncludeJobApplicationData {
		db = db.Preload("JobApplicationData")
	}
	if err := db.Where("id_job_application = ?", input.IdJobApplication).First(&jobApplication).Error; err != nil {
		return JobApplication{}, err
	}
	return jobApplication, nil
}
