package sqldb

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type JobApplicationProfile struct {
	IdJobApplicationProfile uint       `gorm:"primaryKey;autoIncrement;column:id_job_application_profile" json:"_"`
	IdExternal              uuid.UUID  `gorm:"type:text;not null;unique" json:"id"`
	UserId                  uint       `gorm:"column:id_user;not null;uniqueIndex"`
	FirstName               string     `json:"first_name" gorm:"not null"`
	LastName                string     `json:"last_name" gorm:"not null"`
	Email                   string     `json:"email" gorm:"not null"`
	Phone                   string     `json:"phone"`
	Address                 string     `json:"address"`
	City                    string     `json:"city"`
	State                   string     `json:"state"`
	Zip                     string     `json:"zip"`
	CountryOfResidence      string     `json:"country_of_residence"`
	IsVeteran               bool       `json:"is_veteran"`
	CountriesOfCitizenship  []string   `json:"countries_of_citizenship" gorm:"type:text;serializer:json"`
	Gender                  string     `json:"gender"`
	DateOfBirth             time.Time  `json:"date_of_birth"`
	CreatedAt               time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt               time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt               *time.Time `gorm:"index;default:NULL"`
}

func (JobApplicationProfile) TableName() string {
	return "job_application_profile"
}

type UpdateJobApplicationProfileInput struct {
	IdJobApplicationProfile uint                   `json:"id_job_application_profile"`
	Data                    map[string]interface{} `json:"data"`
}

func (a *Activity) UpdateJobApplicationProfile(ctx context.Context, input UpdateJobApplicationProfileInput) error {
	if err := a.db.Model(&JobApplicationProfile{}).Where("id_job_application_profile = ?", input.IdJobApplicationProfile).Updates(input.Data).Error; err != nil {
		return err
	}
	return nil
}
