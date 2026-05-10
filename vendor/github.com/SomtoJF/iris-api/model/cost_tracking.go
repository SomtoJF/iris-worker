package model

import (
	"time"

	"github.com/google/uuid"
)

type CostTrackingType string

const (
	CostTrackingTypeAIPI        CostTrackingType = "llm_call"
	CostTrackingTypeWebSearch   CostTrackingType = "web_search"
	CostTrackingTypeWebScraping CostTrackingType = "web_scraping"
	CostTrackingTypeOther       CostTrackingType = "other"
)

type CostTracking struct {
	ID               uint             `gorm:"primaryKey;autoIncrement;column:id" json:"_"`
	IdExternal       uuid.UUID        `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId           uint             `gorm:"column:id_user;not null" json:"id_user"`
	User             User             `gorm:"foreignKey:UserId;references:IdUser"`
	JobApplicationId *uint            `gorm:"column:id_job_application;default:NULL" json:"id_job_application"`
	JobApplication   *JobApplication  `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	Type             CostTrackingType `gorm:"type:text" json:"type"`
	Model            *string          `json:"model"`
	InputTokens      *int             `json:"input_tokens"`
	OutputTokens     *int             `json:"output_tokens"`
	InputCost        *float64         `json:"input_cost"`
	OutputCost       float64          `json:"output_cost"`
	TotalCost        float64          `json:"total_cost"`
	CreatedAt        time.Time        `gorm:"default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt        time.Time        `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime" json:"updated_at"`
}

func (CostTracking) TableName() string {
	return "cost_tracking"
}
