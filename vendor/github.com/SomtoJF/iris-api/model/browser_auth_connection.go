package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BrowserAuthConnectionStatus string

const (
	BrowserAuthConnectionConnected        BrowserAuthConnectionStatus = "connected"
	BrowserAuthConnectionNeedsAuth        BrowserAuthConnectionStatus = "needs_auth"
	BrowserAuthConnectionReauthenticating BrowserAuthConnectionStatus = "reauthenticating"
	BrowserAuthConnectionDisconnected     BrowserAuthConnectionStatus = "disconnected"
	BrowserAuthConnectionFailed           BrowserAuthConnectionStatus = "failed"
)

func (s BrowserAuthConnectionStatus) Valid() bool {
	switch s {
	case BrowserAuthConnectionConnected, BrowserAuthConnectionNeedsAuth,
		BrowserAuthConnectionReauthenticating, BrowserAuthConnectionDisconnected,
		BrowserAuthConnectionFailed:
		return true
	default:
		return false
	}
}

type BrowserAuthConnection struct {
	IdBrowserAuthConnection uint                        `gorm:"primaryKey;autoIncrement;column:id_browser_auth_connection"`
	IdExternal              uuid.UUID                   `gorm:"unique;type:uuid;default:gen_random_uuid()"`
	BrowserProfileID        uint                        `gorm:"column:id_browser_profile;not null;uniqueIndex:idx_browser_auth_profile_provider_domain;index"`
	BrowserProfile          BrowserProfile              `gorm:"foreignKey:BrowserProfileID;references:IdBrowserProfile;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Provider                BrowserProvider             `gorm:"type:varchar(16);not null;uniqueIndex:idx_browser_auth_profile_provider_domain;check:chk_browser_auth_provider,provider IN ('rod','kernel')"`
	ProviderConnectionID    string                      `gorm:"type:text;not null;default:'';index"`
	Domain                  string                      `gorm:"type:text;not null;uniqueIndex:idx_browser_auth_profile_provider_domain"`
	Status                  BrowserAuthConnectionStatus `gorm:"type:varchar(32);not null;index;check:chk_browser_auth_status,status IN ('connected','needs_auth','reauthenticating','disconnected','failed')"`
	CanReauth               bool                        `gorm:"not null;default:false"`
	CanReauthReason         string                      `gorm:"type:text;not null;default:''"`
	CreatedAt               time.Time                   `gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt               time.Time                   `gorm:"not null;default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (BrowserAuthConnection) TableName() string { return "browser_auth_connection" }

func (c BrowserAuthConnection) Validate() error {
	if c.BrowserProfileID == 0 || c.Domain == "" {
		return fmt.Errorf("browser auth connection profile and domain are required")
	}
	if !c.Provider.Valid() {
		return fmt.Errorf("browser auth connection provider is invalid")
	}
	if !c.Status.Valid() {
		return fmt.Errorf("browser auth connection status is invalid")
	}
	return nil
}

func (c *BrowserAuthConnection) BeforeCreate(*gorm.DB) error { return c.Validate() }
