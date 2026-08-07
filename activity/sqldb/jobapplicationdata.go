package sqldb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ====== TYPES ======

type JobApplicationQuestion struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type JobApplicationQuestions []JobApplicationQuestion

func (q *JobApplicationQuestions) Scan(value interface{}) error {
	if value == nil {
		*q = JobApplicationQuestions{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*q = JobApplicationQuestions{}
		return nil
	}
	if len(b) == 0 || (len(b) == 4 && (string(b) == "NULL" || string(b) == "null")) {
		*q = JobApplicationQuestions{}
		return nil
	}
	if err := json.Unmarshal(b, (*[]JobApplicationQuestion)(q)); err != nil {
		*q = JobApplicationQuestions{}
		return nil
	}
	return nil
}

func (q JobApplicationQuestions) Value() (driver.Value, error) {
	if len(q) == 0 {
		return "[]", nil
	}
	return json.Marshal(q)
}

// ====== MODEL ======

type JobApplicationData struct {
	IdJobApplicationData uint                    `gorm:"primaryKey;autoIncrement;column:id_job_application_data" json:"_"`
	IdExternal           uuid.UUID               `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId               uint                    `gorm:"column:id_user;not null"`
	User                 User                    `gorm:"foreignKey:UserId;references:IdUser"`
	JobApplicationId     uint                    `gorm:"column:id_job_application;not null"`
	JobApplication       JobApplication          `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	CoverLetter          *string                 `gorm:"type:text"`
	Questions            JobApplicationQuestions `gorm:"type:jsonb;not null"`
	CreatedAt            time.Time               `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt            time.Time               `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (JobApplicationData) TableName() string {
	return "job_application_data"
}

// ====== ACTIVITY ======

type CreateJobApplicationDataInput struct {
	IdUser           uint                     `json:"id_user"`
	IdJobApplication uint                     `json:"id_job_application"`
	CoverLetter      *string                  `json:"cover_letter"`
	Questions        []JobApplicationQuestion `json:"questions"`
}

func (a *Activity) CreateJobApplicationData(ctx context.Context, input CreateJobApplicationDataInput) error {
	data := JobApplicationData{
		UserId:           input.IdUser,
		JobApplicationId: input.IdJobApplication,
		CoverLetter:      input.CoverLetter,
		Questions:        JobApplicationQuestions(input.Questions),
	}
	return a.db.Create(&data).Error
}
