package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BrowserProvider string

const (
	BrowserProviderRod    BrowserProvider = "rod"
	BrowserProviderKernel BrowserProvider = "kernel"
)

func (p BrowserProvider) Valid() bool {
	return p == BrowserProviderRod || p == BrowserProviderKernel
}

type BrowserProfile struct {
	IdBrowserProfile    uint            `gorm:"primaryKey;autoIncrement;column:id_browser_profile"`
	IdExternal          uuid.UUID       `gorm:"unique;type:uuid;default:gen_random_uuid()"`
	UserId              uint            `gorm:"column:id_user;not null;uniqueIndex:idx_browser_profile_user_provider"`
	User                User            `gorm:"foreignKey:UserId;references:IdUser;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Provider            BrowserProvider `gorm:"type:varchar(16);not null;uniqueIndex:idx_browser_profile_user_provider;check:chk_browser_profile_provider,provider IN ('rod','kernel')"`
	ProviderProfileID   string          `gorm:"type:text;not null;default:'';index"`
	ProviderProfileName string          `gorm:"type:text;not null;default:''"`
	BrowserVaultID      *uint           `gorm:"column:id_browser_vault;index"`
	BrowserVault        *BrowserVault   `gorm:"foreignKey:BrowserVaultID;references:IdBrowserVault;constraint:OnUpdate:CASCADE,OnDelete:SET NULL"`
	CreatedAt           time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt           time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt           *time.Time      `gorm:"index;default:NULL"`
	LeaseOwner          string          `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt      *time.Time      `gorm:"index"`
}

func (BrowserProfile) TableName() string { return "browser_profile" }

func (p BrowserProfile) Validate() error {
	if p.UserId == 0 {
		return fmt.Errorf("browser profile user id is required")
	}
	if !p.Provider.Valid() {
		return fmt.Errorf("browser profile provider is invalid")
	}
	return nil
}

func (p *BrowserProfile) BeforeCreate(*gorm.DB) error { return p.Validate() }
