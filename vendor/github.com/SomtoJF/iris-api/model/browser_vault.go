package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type BrowserVault struct {
	IdBrowserVault    uint            `gorm:"primaryKey;autoIncrement;column:id_browser_vault"`
	IdExternal        uuid.UUID       `gorm:"unique;type:uuid;default:gen_random_uuid()"`
	UserId            uint            `gorm:"column:id_user;not null;uniqueIndex:idx_browser_vault_user_provider"`
	User              User            `gorm:"foreignKey:UserId;references:IdUser;constraint:OnUpdate:CASCADE,OnDelete:CASCADE"`
	Provider          BrowserProvider `gorm:"type:varchar(16);not null;uniqueIndex:idx_browser_vault_user_provider;check:chk_browser_vault_provider,provider IN ('rod','kernel')"`
	ProviderVaultID   string          `gorm:"type:text;not null;default:'';index"`
	ProviderVaultName string          `gorm:"type:text;not null;default:''"`
	CreatedAt         time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt         time.Time       `gorm:"not null;default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt         *time.Time      `gorm:"index;default:NULL"`
}

func (BrowserVault) TableName() string { return "browser_vault" }

func (v BrowserVault) Validate() error {
	if v.UserId == 0 {
		return fmt.Errorf("browser vault user id is required")
	}
	if !v.Provider.Valid() {
		return fmt.Errorf("browser vault provider is invalid")
	}
	return nil
}

func (v *BrowserVault) BeforeCreate(*gorm.DB) error { return v.Validate() }
