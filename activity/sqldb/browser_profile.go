package sqldb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BrowserStore struct{ db *gorm.DB }

// NewBrowserStore creates provider-neutral persistence operations for browser state.
func NewBrowserStore(db *gorm.DB) *BrowserStore { return &BrowserStore{db: db} }

func (s *BrowserStore) ready() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("browser store database is unavailable")
	}
	return nil
}

var ErrBrowserProfileLeaseNotOwned = errors.New("browser profile lease is not owned by caller")

// AcquireBrowserProfileLease atomically acquires or renews the profile's single-writer lease.
func (s *BrowserStore) AcquireBrowserProfileLease(ctx context.Context, profileID uint, owner string, expiresAt time.Time) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if profileID == 0 || owner == "" || !expiresAt.After(time.Now()) {
		return false, fmt.Errorf("profile id, owner, and future lease expiry are required")
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&BrowserProfile{}).Where("id_browser_profile = ? AND (lease_owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ? OR lease_owner = ?)", profileID, now, owner).
		Updates(map[string]any{"lease_owner": owner, "lease_expires_at": expiresAt.UTC(), "updated_at": now})
	if result.Error != nil {
		return false, fmt.Errorf("acquire browser profile lease: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (s *BrowserStore) ReleaseBrowserProfileLease(ctx context.Context, profileID uint, owner string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if profileID == 0 || owner == "" {
		return fmt.Errorf("profile id and lease owner are required")
	}
	result := s.db.WithContext(ctx).Model(&BrowserProfile{}).Where("id_browser_profile = ? AND lease_owner = ?", profileID, owner).
		Updates(map[string]any{"lease_owner": "", "lease_expires_at": nil, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("release browser profile lease: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrBrowserProfileLeaseNotOwned
	}
	return nil
}

// SaveBrowserProfile inserts or updates the user's single profile for this provider.
func (s *BrowserStore) SaveBrowserProfile(ctx context.Context, profile BrowserProfile) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	if profile.BrowserVaultID != nil {
		var vault BrowserVault
		if err := s.db.WithContext(ctx).Select("id_browser_vault", "user_id", "provider").First(&vault, *profile.BrowserVaultID).Error; err != nil {
			return fmt.Errorf("load browser profile vault: %w", err)
		}
		if vault.UserId != profile.UserId || vault.Provider != profile.Provider {
			return fmt.Errorf("browser profile vault must belong to the same user and provider")
		}
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id_user"}, {Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"provider_profile_id", "provider_profile_name", "id_browser_vault", "deleted_at", "updated_at",
		}),
	}).Omit(clause.Associations).Create(&profile).Error
}

// GetBrowserProfile returns the user's provider profile; absent records return found=false.
func (s *BrowserStore) GetBrowserProfile(ctx context.Context, userID uint, provider BrowserProvider) (BrowserProfile, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserProfile{}, false, err
	}
	if userID == 0 || !provider.Valid() {
		return BrowserProfile{}, false, fmt.Errorf("browser profile user id and valid provider are required")
	}
	var profile BrowserProfile
	err := s.db.WithContext(ctx).Where("id_user = ? AND provider = ?", userID, provider).First(&profile).Error
	if err == gorm.ErrRecordNotFound {
		return BrowserProfile{}, false, nil
	}
	if err != nil {
		return BrowserProfile{}, false, fmt.Errorf("get browser profile: %w", err)
	}
	return profile, true, nil
}

// SaveBrowserVault inserts or updates the user's single vault reference for this provider.
func (s *BrowserStore) SaveBrowserVault(ctx context.Context, vault BrowserVault) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := vault.Validate(); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id_user"}, {Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"provider_vault_id", "provider_vault_name", "deleted_at", "updated_at",
		}),
	}).Omit(clause.Associations).Create(&vault).Error
}

// GetBrowserVault returns the user's provider vault; absent records return found=false.
func (s *BrowserStore) GetBrowserVault(ctx context.Context, userID uint, provider BrowserProvider) (BrowserVault, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserVault{}, false, err
	}
	if userID == 0 || !provider.Valid() {
		return BrowserVault{}, false, fmt.Errorf("browser vault user id and valid provider are required")
	}
	var vault BrowserVault
	err := s.db.WithContext(ctx).Where("id_user = ? AND provider = ?", userID, provider).First(&vault).Error
	if err == gorm.ErrRecordNotFound {
		return BrowserVault{}, false, nil
	}
	if err != nil {
		return BrowserVault{}, false, fmt.Errorf("get browser vault: %w", err)
	}
	return vault, true, nil
}

// EnsureKernelProfileVault creates the user's persistent Kernel resource references if absent.
func (s *BrowserStore) EnsureKernelProfileVault(ctx context.Context, applicationBrowserID uuid.UUID) (BrowserProfile, BrowserVault, error) {
	if err := s.ready(); err != nil {
		return BrowserProfile{}, BrowserVault{}, err
	}
	if applicationBrowserID == uuid.Nil {
		return BrowserProfile{}, BrowserVault{}, fmt.Errorf("application browser id is required")
	}

	var profile BrowserProfile
	var vault BrowserVault
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var application JobApplication
		if err := tx.Model(&JobApplication{}).
			Select("job_application.id_user").
			Joins("JOIN browser_session ON browser_session.id_job_application = job_application.id_job_application").
			Where("browser_session.application_browser_id = ? AND browser_session.provider = ?", applicationBrowserID, BrowserProviderKernel).
			First(&application).Error; err != nil {
			return fmt.Errorf("load Kernel browser application user: %w", err)
		}
		if application.UserId == 0 {
			return fmt.Errorf("Kernel browser application user is required")
		}

		vault = BrowserVault{UserId: application.UserId, Provider: BrowserProviderKernel}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id_user"}, {Name: "provider"}},
			DoNothing: true,
		}).Omit(clause.Associations).Create(&vault).Error; err != nil {
			return fmt.Errorf("create Kernel browser vault reference: %w", err)
		}
		if err := tx.Where("id_user = ? AND provider = ?", application.UserId, BrowserProviderKernel).First(&vault).Error; err != nil {
			return fmt.Errorf("load Kernel browser vault reference: %w", err)
		}

		profile = BrowserProfile{UserId: application.UserId, Provider: BrowserProviderKernel}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id_user"}, {Name: "provider"}},
			DoNothing: true,
		}).Omit(clause.Associations).Create(&profile).Error; err != nil {
			return fmt.Errorf("create Kernel browser profile reference: %w", err)
		}
		if err := tx.Where("id_user = ? AND provider = ?", application.UserId, BrowserProviderKernel).First(&profile).Error; err != nil {
			return fmt.Errorf("load Kernel browser profile reference: %w", err)
		}
		profile.BrowserVaultID = &vault.IdBrowserVault
		if err := tx.Model(&BrowserProfile{}).Where("id_browser_profile = ?", profile.IdBrowserProfile).
			Update("id_browser_vault", vault.IdBrowserVault).Error; err != nil {
			return fmt.Errorf("link Kernel browser profile vault: %w", err)
		}
		return nil
	})
	if err != nil {
		return BrowserProfile{}, BrowserVault{}, err
	}
	if profile.ProviderProfileName == "" {
		profile.ProviderProfileName = fmt.Sprintf("iris-user-%d-profile", profile.UserId)
	}
	if vault.ProviderVaultName == "" {
		vault.ProviderVaultName = fmt.Sprintf("iris-user-%d-vault", vault.UserId)
	}
	return profile, vault, nil
}

// SaveKernelProfileVault persists Kernel resource IDs after provider creation/retrieval.
func (s *BrowserStore) SaveKernelProfileVault(ctx context.Context, applicationBrowserID uuid.UUID, profileID, vaultID uint, profileProviderID, profileName, vaultProviderID, vaultName string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil || profileID == 0 || vaultID == 0 || profileProviderID == "" || vaultProviderID == "" || profileName == "" || vaultName == "" {
		return fmt.Errorf("application, profile, vault, and provider resource identifiers are required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var application JobApplication
		if err := tx.Model(&JobApplication{}).
			Select("job_application.id_user").
			Joins("JOIN browser_session ON browser_session.id_job_application = job_application.id_job_application").
			Where("browser_session.application_browser_id = ? AND browser_session.provider = ?", applicationBrowserID, BrowserProviderKernel).
			First(&application).Error; err != nil {
			return fmt.Errorf("load Kernel browser application user: %w", err)
		}
		var vault BrowserVault
		if err := tx.Where("id_browser_vault = ? AND id_user = ? AND provider = ?", vaultID, application.UserId, BrowserProviderKernel).First(&vault).Error; err != nil {
			return fmt.Errorf("validate Kernel browser vault reference: %w", err)
		}
		if err := tx.Model(&BrowserVault{}).Where("id_browser_vault = ?", vaultID).Updates(map[string]any{
			"provider_vault_id": vaultProviderID, "provider_vault_name": vaultName, "deleted_at": nil,
		}).Error; err != nil {
			return fmt.Errorf("save Kernel browser vault reference: %w", err)
		}
		result := tx.Model(&BrowserProfile{}).Where("id_browser_profile = ? AND id_user = ? AND provider = ?", profileID, application.UserId, BrowserProviderKernel).
			Updates(map[string]any{
				"provider_profile_id": profileProviderID, "provider_profile_name": profileName,
				"id_browser_vault": vaultID, "deleted_at": nil,
			})
		if result.Error != nil {
			return fmt.Errorf("save Kernel browser profile reference: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("save Kernel browser profile reference: %w", gorm.ErrRecordNotFound)
		}
		return nil
	})
}

// SaveBrowserAuthConnection inserts or updates the profile's auth connection for a domain.
func (s *BrowserStore) SaveBrowserAuthConnection(ctx context.Context, connection BrowserAuthConnection) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := connection.Validate(); err != nil {
		return err
	}
	var profile BrowserProfile
	if err := s.db.WithContext(ctx).Select("id_browser_profile", "provider").First(&profile, connection.BrowserProfileID).Error; err != nil {
		return fmt.Errorf("load browser auth profile: %w", err)
	}
	if profile.Provider != connection.Provider {
		return fmt.Errorf("browser auth connection provider does not match its profile")
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id_browser_profile"}, {Name: "provider"}, {Name: "domain"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"provider_connection_id", "status", "can_reauth", "can_reauth_reason", "updated_at",
		}),
	}).Omit(clause.Associations).Create(&connection).Error
}

// GetBrowserAuthConnection returns a domain connection; absent records return found=false.
func (s *BrowserStore) GetBrowserAuthConnection(ctx context.Context, profileID uint, provider BrowserProvider, domain string) (BrowserAuthConnection, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserAuthConnection{}, false, err
	}
	if profileID == 0 || domain == "" || !provider.Valid() {
		return BrowserAuthConnection{}, false, fmt.Errorf("browser auth profile, domain, and valid provider are required")
	}
	var connection BrowserAuthConnection
	err := s.db.WithContext(ctx).
		Where("id_browser_profile = ? AND provider = ? AND domain = ?", profileID, provider, domain).
		First(&connection).Error
	if err == gorm.ErrRecordNotFound {
		return BrowserAuthConnection{}, false, nil
	}
	if err != nil {
		return BrowserAuthConnection{}, false, fmt.Errorf("get browser auth connection: %w", err)
	}
	return connection, true, nil
}

// ListBrowserAuthConnections returns the profile's connections in stable domain order.
func (s *BrowserStore) ListBrowserAuthConnections(ctx context.Context, profileID uint) ([]BrowserAuthConnection, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if profileID == 0 {
		return nil, fmt.Errorf("browser auth profile id is required")
	}
	var connections []BrowserAuthConnection
	if err := s.db.WithContext(ctx).Where("id_browser_profile = ?", profileID).Order("domain ASC, id_browser_auth_connection ASC").Find(&connections).Error; err != nil {
		return nil, fmt.Errorf("list browser auth connections: %w", err)
	}
	return connections, nil
}
