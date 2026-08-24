package sqldb

import (
	"context"
	"errors"

	"github.com/SomtoJF/iris-api/model"
	"gorm.io/gorm"
)

type CoverLetter = model.CoverLetter
type CoverLetterStatus = model.CoverLetterStatus

const (
	CoverLetterStatusProcessing = model.CoverLetterStatusProcessing
	CoverLetterStatusReady      = model.CoverLetterStatusReady
	CoverLetterStatusFailed     = model.CoverLetterStatusFailed
)

type GetCoverLetterInput struct {
	IdCoverLetter uint `json:"id_cover_letter"`
}

func (a *Activity) GetCoverLetter(ctx context.Context, input GetCoverLetterInput) (CoverLetter, error) {
	var coverLetter CoverLetter
	if err := a.db.Where("id_cover_letter = ?", input.IdCoverLetter).First(&coverLetter).Error; err != nil {
		return CoverLetter{}, err
	}
	return coverLetter, nil
}

type UpsertCoverLetterInput struct {
	IdUser           uint              `json:"id_user"`
	IdJobApplication uint              `json:"id_job_application"`
	IdResume         uint              `json:"id_resume"`
	JobTitle         string            `json:"job_title"`
	CompanyName      string            `json:"company_name"`
	JobDescription   string            `json:"job_description"`
	Url              string            `json:"url"`
	Body             string            `json:"body"`
	Status           CoverLetterStatus `json:"status"`
}

func (a *Activity) UpsertCoverLetter(ctx context.Context, input UpsertCoverLetterInput) error {
	jobAppID := input.IdJobApplication
	body := input.Body
	status := input.Status
	if status == "" {
		status = CoverLetterStatusReady
	}

	var coverLetter CoverLetter
	err := a.db.Where("id_job_application = ?", input.IdJobApplication).First(&coverLetter).Error
	switch {
	case err == nil:
		return a.db.Model(&coverLetter).Updates(map[string]any{
			"id_resume":       input.IdResume,
			"job_title":       input.JobTitle,
			"company_name":    input.CompanyName,
			"job_description": input.JobDescription,
			"url":             input.Url,
			"body":            &body,
			"status":          status,
		}).Error
	case errors.Is(err, gorm.ErrRecordNotFound):
		coverLetter = CoverLetter{
			UserId:           input.IdUser,
			ResumeId:         input.IdResume,
			JobApplicationId: &jobAppID,
			JobTitle:         input.JobTitle,
			CompanyName:      input.CompanyName,
			JobDescription:   input.JobDescription,
			Url:              input.Url,
			Body:             &body,
			Status:           status,
		}
		return a.db.Create(&coverLetter).Error
	default:
		return err
	}
}
