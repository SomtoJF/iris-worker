package model

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type BrowserReplayAttempt struct {
	IdBrowserReplayAttempt uint           `gorm:"primaryKey;autoIncrement;column:id_browser_replay_attempt"`
	BrowserSessionID       uint           `gorm:"column:id_browser_session;not null;uniqueIndex:idx_browser_attempt_session_workflow"`
	Session                BrowserSession `gorm:"foreignKey:BrowserSessionID;references:IdBrowserSession;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	WorkflowID             string         `gorm:"type:text;not null;uniqueIndex:idx_browser_attempt_session_workflow"`
	ReplayGeneration       uint64         `gorm:"not null"`
	CreatedAt              time.Time      `gorm:"not null;default:CURRENT_TIMESTAMP"`
}

func (BrowserReplayAttempt) TableName() string { return "browser_replay_attempt" }

func (a *BrowserReplayAttempt) BeforeCreate(*gorm.DB) error {
	if a.BrowserSessionID == 0 || a.WorkflowID == "" || a.ReplayGeneration == 0 {
		return fmt.Errorf("browser replay attempt requires a session, workflow ID, and positive generation")
	}
	return nil
}
