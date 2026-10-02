package profile

import (
	"github.com/SomtoJF/iris-worker/activity/sqldb"
	"go.temporal.io/sdk/workflow"
)

type UserProfile struct {
	FirstName                   string                      `json:"first_name"`
	LastName                    string                      `json:"last_name"`
	Email                       string                      `json:"email"`
	Phone                       string                      `json:"phone"`
	Address                     string                      `json:"address"`
	City                        string                      `json:"city"`
	State                       string                      `json:"state"`
	Zip                         string                      `json:"zip"`
	CountryOfResidence          string                      `json:"country_of_residence"`
	IsVeteran                   bool                        `json:"is_veteran"`
	CountriesOfCitizenship      []string                    `json:"countries_of_citizenship"`
	Gender                      string                      `json:"gender"`
	DateOfBirth                 string                      `json:"date_of_birth"`
	Age                         int                         `json:"age"`
	SalaryMin                   *float64                    `json:"salary_min,omitempty"`
	SalaryMax                   *float64                    `json:"salary_max,omitempty"`
	SalaryCurrency              string                      `json:"salary_currency,omitempty"`
	Ethnicity                   string                      `json:"ethnicity,omitempty"`
	IsOpenToRelocating          *bool                       `json:"is_open_to_relocating,omitempty"`
	NoticePeriodDays            *int                        `json:"notice_period_days,omitempty"`
	LinkedInUrl                 *string                     `json:"linkedin_url,omitempty"`
	PreferredWorkingArrangement []string                    `json:"preferred_working_arrangement,omitempty"`
	LanguageProficiencies       []sqldb.LanguageProficiency `json:"language_proficiencies,omitempty"`
	PortfolioLink               *string                     `json:"portfolio_link,omitempty"`
}

func Fetch(ctx workflow.Context, idUser uint) (UserProfile, error) {
	var jobApplicationProfile sqldb.JobApplicationProfile
	if err := workflow.ExecuteActivity(ctx, "FetchJobApplicationProfile", idUser).Get(ctx, &jobApplicationProfile); err != nil {
		return UserProfile{}, err
	}

	now := workflow.Now(ctx)
	age := now.Year() - jobApplicationProfile.DateOfBirth.Year()
	if now.Month() < jobApplicationProfile.DateOfBirth.Month() ||
		(now.Month() == jobApplicationProfile.DateOfBirth.Month() && now.Day() < jobApplicationProfile.DateOfBirth.Day()) {
		age--
	}

	return UserProfile{
		FirstName:                   jobApplicationProfile.FirstName,
		LastName:                    jobApplicationProfile.LastName,
		Email:                       jobApplicationProfile.Email,
		Phone:                       jobApplicationProfile.Phone,
		Address:                     jobApplicationProfile.Address,
		City:                        jobApplicationProfile.City,
		State:                       jobApplicationProfile.State,
		Zip:                         jobApplicationProfile.Zip,
		CountryOfResidence:          jobApplicationProfile.CountryOfResidence,
		IsVeteran:                   jobApplicationProfile.IsVeteran,
		CountriesOfCitizenship:      jobApplicationProfile.CountriesOfCitizenship,
		Gender:                      jobApplicationProfile.Gender,
		DateOfBirth:                 jobApplicationProfile.DateOfBirth.Format("2006-01-02"),
		Age:                         age,
		SalaryMin:                   jobApplicationProfile.SalaryMin,
		SalaryMax:                   jobApplicationProfile.SalaryMax,
		SalaryCurrency:              jobApplicationProfile.SalaryCurrency,
		Ethnicity:                   jobApplicationProfile.Ethnicity,
		IsOpenToRelocating:          jobApplicationProfile.IsOpenToRelocating,
		NoticePeriodDays:            jobApplicationProfile.NoticePeriodDays,
		LinkedInUrl:                 jobApplicationProfile.LinkedInUrl,
		PreferredWorkingArrangement: jobApplicationProfile.PreferredWorkingArrangement,
		LanguageProficiencies:       jobApplicationProfile.LanguageProficiencies,
		PortfolioLink:               jobApplicationProfile.PortfolioLink,
	}, nil
}
