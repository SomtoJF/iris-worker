package sqldb

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
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
	IdExternal       uuid.UUID        `gorm:"type:text;not null;unique" json:"id"`
	IdUser           uint             `gorm:"not null" json:"id_user"`
	User             User             `gorm:"foreignKey:IdUser;references:IdUser"`
	IdJobApplication *uint            `json:"id_job_application"`
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

func (c *CostTracking) BeforeCreate(tx *gorm.DB) error {
	if c.IdExternal == uuid.Nil {
		c.IdExternal = uuid.New()
	}
	return nil
}
