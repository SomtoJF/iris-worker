package sqldb

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type LanguageProficiency struct {
	Language    string `json:"language"`
	Proficiency string `json:"proficiency"`
}

type LanguageProficiencies []LanguageProficiency

type JobApplicationProfile struct {
	IdJobApplicationProfile     uint                  `gorm:"primaryKey;autoIncrement;column:id_job_application_profile" json:"_"`
	IdExternal                  uuid.UUID             `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId                      uint                  `gorm:"column:id_user;not null;uniqueIndex"`
	FirstName                   string                `json:"first_name" gorm:"not null"`
	LastName                    string                `json:"last_name" gorm:"not null"`
	Email                       string                `json:"email" gorm:"not null"`
	Phone                       string                `json:"phone"`
	Address                     string                `json:"address"`
	City                        string                `json:"city"`
	State                       string                `json:"state"`
	Zip                         string                `json:"zip"`
	CountryOfResidence          string                `json:"country_of_residence"`
	IsVeteran                   bool                  `json:"is_veteran"`
	CountriesOfCitizenship      pq.StringArray        `json:"countries_of_citizenship" gorm:"type:text[]"`
	Gender                      string                `json:"gender"`
	DateOfBirth                 time.Time             `json:"date_of_birth"`
	SalaryMin                   *float64              `json:"salary_min" gorm:"default:NULL"`
	SalaryMax                   *float64              `json:"salary_max" gorm:"default:NULL"`
	SalaryCurrency              string                `json:"salary_currency"`
	Ethnicity                   string                `json:"ethnicity"`
	IsOpenToRelocating          *bool                 `json:"is_open_to_relocating" gorm:"default:NULL"`
	NoticePeriodDays            *int                  `json:"notice_period_days" gorm:"default:NULL"`
	PreferredWorkingArrangement pq.StringArray        `json:"preferred_working_arrangement" gorm:"type:text[]"`
	LanguageProficiencies       LanguageProficiencies `json:"language_proficiencies" gorm:"type:jsonb"`
	PortfolioLink               *string               `json:"portfolio_link"`
	CreatedAt                   time.Time             `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt                   time.Time             `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt                   *time.Time            `gorm:"index;default:NULL"`
}

func (JobApplicationProfile) TableName() string {
	return "job_application_profile"
}

func (lp *LanguageProficiencies) Scan(value any) error {
	if value == nil {
		*lp = nil
		return nil
	}

	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("LanguageProficiencies.Scan: unsupported type %T", value)
	}

	if len(b) == 0 {
		*lp = LanguageProficiencies{}
		return nil
	}

	var out LanguageProficiencies
	if err := json.Unmarshal(b, &out); err != nil {
		return fmt.Errorf("LanguageProficiencies.Scan: %w", err)
	}
	*lp = out
	return nil
}

func (lp LanguageProficiencies) Value() (driver.Value, error) {
	if lp == nil {
		return nil, nil
	}
	b, err := json.Marshal(lp)
	if err != nil {
		return nil, fmt.Errorf("LanguageProficiencies.Value: %w", err)
	}
	return b, nil
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

func (a *Activity) FetchJobApplicationProfile(ctx context.Context, idUser uint) (JobApplicationProfile, error) {
	var jobApplicationProfile JobApplicationProfile
	if err := a.db.Model(&JobApplicationProfile{}).Where("id_user = ?", idUser).First(&jobApplicationProfile).Error; err != nil {
		return JobApplicationProfile{}, err
	}
	return jobApplicationProfile, nil
}
