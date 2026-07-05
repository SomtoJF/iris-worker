package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type JobApplicationQuestions struct {
	Question   string `gorm:"type:text;not null" json:"question"`
	Answer     string `gorm:"type:text;not null" json:"answer"`
	IsOptional bool   `gorm:"default:false" json:"is_optional"`
}

// JobApplicationQuestionsList is stored as jsonb in Postgres.
// It implements sql.Scanner and driver.Valuer so GORM can persist it.
type JobApplicationQuestionsList []JobApplicationQuestions

func (q *JobApplicationQuestionsList) Scan(value any) error {
	if value == nil {
		*q = JobApplicationQuestionsList{}
		return nil
	}

	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("JobApplicationQuestionsList.Scan: unsupported type %T", value)
	}

	if len(b) == 0 {
		*q = JobApplicationQuestionsList{}
		return nil
	}

	var out []JobApplicationQuestions
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("JobApplicationQuestionsList.Scan: %w", err)
	}
	*q = JobApplicationQuestionsList(out)
	return nil
}

func (q JobApplicationQuestionsList) Value() (driver.Value, error) {
	if q == nil {
		return []byte("[]"), nil
	}
	b, err := json.Marshal([]JobApplicationQuestions(q))
	if err != nil {
		return nil, fmt.Errorf("JobApplicationQuestionsList.Value: %w", err)
	}
	return b, nil
}

type JobApplicationData struct {
	IdJobApplicationData uint                        `gorm:"primaryKey;autoIncrement;column:id_job_application_data" json:"_"`
	IdExternal           uuid.UUID                   `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId               uint                        `gorm:"column:id_user;not null"`
	User                 User                        `gorm:"foreignKey:UserId;references:IdUser"`
	JobApplicationId     uint                        `gorm:"column:id_job_application;not null;uniqueIndex"`
	JobApplication       JobApplication              `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	CoverLetter          *string                     `gorm:"type:text"`
	Questions            JobApplicationQuestionsList `gorm:"type:jsonb;not null"`
	CreatedAt            time.Time                   `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt            time.Time                   `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (JobApplicationData) TableName() string {
	return "job_application_data"
}
