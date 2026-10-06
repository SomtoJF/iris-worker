package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BrowserSessionStatus string

const (
	BrowserSessionStarting BrowserSessionStatus = "starting"
	BrowserSessionActive   BrowserSessionStatus = "active"
	BrowserSessionClosed   BrowserSessionStatus = "closed"
	BrowserSessionExpired  BrowserSessionStatus = "expired"
	BrowserSessionFailed   BrowserSessionStatus = "failed"
)

func (s BrowserSessionStatus) Valid() bool {
	switch s {
	case BrowserSessionStarting, BrowserSessionActive, BrowserSessionClosed, BrowserSessionExpired, BrowserSessionFailed:
		return true
	default:
		return false
	}
}

type BrowserSession struct {
	IdBrowserSession            uint                 `gorm:"primaryKey;autoIncrement;column:id_browser_session"`
	IdExternal                  uuid.UUID            `gorm:"unique;type:uuid;default:gen_random_uuid()"`
	JobApplicationID            uint                 `gorm:"column:id_job_application;not null;uniqueIndex:idx_browser_session_job_provider;index"`
	JobApplication              JobApplication       `gorm:"foreignKey:JobApplicationID;references:IdJobApplication;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	ApplicationBrowserID        uuid.UUID            `gorm:"column:application_browser_id;type:uuid;not null;uniqueIndex"`
	Provider                    BrowserProvider      `gorm:"type:varchar(16);not null;uniqueIndex:idx_browser_session_job_provider;check:chk_browser_session_provider,provider IN ('rod','kernel')"`
	BrowserProfileID            *uint                `gorm:"column:id_browser_profile;index"`
	BrowserProfile              *BrowserProfile      `gorm:"foreignKey:BrowserProfileID;references:IdBrowserProfile;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`
	BrowserVaultID              *uint                `gorm:"column:id_browser_vault;index"`
	BrowserVault                *BrowserVault        `gorm:"foreignKey:BrowserVaultID;references:IdBrowserVault;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`
	ProviderSessionID           string               `gorm:"type:text;not null;default:'';index"`
	Status                      BrowserSessionStatus `gorm:"type:varchar(16);not null;default:'starting';index;check:chk_browser_session_status,status IN ('starting','active','closed','expired','failed')"`
	ReplayGeneration            uint64               `gorm:"not null;default:1"`
	ReplayPending               bool                 `gorm:"not null;default:false"`
	LiveViewURLCiphertext       []byte               `gorm:"column:browser_live_view_url_ciphertext;type:bytea"`
	CheckpointCreatedAt         *time.Time           `gorm:"column:checkpoint_created_at"`
	CheckpointMutationID        *uint                `gorm:"column:checkpoint_mutation_id"`
	PendingCheckpointCreatedAt  *time.Time           `gorm:"column:pending_checkpoint_created_at"`
	PendingCheckpointMutationID *uint                `gorm:"column:pending_checkpoint_mutation_id"`
	Owner                       string               `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt              *time.Time           `gorm:"index"`
	StartedAt                   *time.Time           `gorm:"index"`
	ClosedAt                    *time.Time           `gorm:"index"`
	ExpiresAt                   *time.Time           `gorm:"index"`
	CreatedAt                   time.Time            `gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt                   time.Time            `gorm:"not null;default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (BrowserSession) TableName() string { return "browser_session" }

func (s BrowserSession) Validate() error {
	if s.JobApplicationID == 0 || s.ApplicationBrowserID == uuid.Nil {
		return fmt.Errorf("browser session application and logical browser ids are required")
	}
	if !s.Provider.Valid() {
		return fmt.Errorf("browser session provider is invalid")
	}
	if !s.Status.Valid() {
		return fmt.Errorf("browser session status is invalid")
	}
	if s.ReplayGeneration == 0 {
		return fmt.Errorf("browser session replay generation must be positive")
	}
	if (s.CheckpointCreatedAt == nil) != (s.CheckpointMutationID == nil) || (s.PendingCheckpointCreatedAt == nil) != (s.PendingCheckpointMutationID == nil) {
		return fmt.Errorf("browser session checkpoint cursor is incomplete")
	}
	return nil
}

func (s *BrowserSession) BeforeCreate(*gorm.DB) error { return s.Validate() }
