package sqldb

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ApplicationBrowserIDResult struct {
	Found bool
	ID    uuid.UUID
}

// GetApplicationBrowserID looks up the stable logical ID for an application/provider pair.
func (s *BrowserStore) GetApplicationBrowserID(ctx context.Context, jobApplicationID uint, provider BrowserProvider) (ApplicationBrowserIDResult, error) {
	if err := s.ready(); err != nil {
		return ApplicationBrowserIDResult{}, err
	}
	if jobApplicationID == 0 || !provider.Valid() {
		return ApplicationBrowserIDResult{}, fmt.Errorf("application id and valid browser provider are required")
	}
	var session BrowserSession
	err := s.db.WithContext(ctx).Where("id_job_application = ? AND provider = ?", jobApplicationID, provider).First(&session).Error
	if err == gorm.ErrRecordNotFound {
		return ApplicationBrowserIDResult{}, nil
	}
	if err != nil {
		return ApplicationBrowserIDResult{}, fmt.Errorf("get application browser id: %w", err)
	}
	return ApplicationBrowserIDResult{Found: true, ID: session.ApplicationBrowserID}, nil
}

// GetOrCreateApplicationBrowserID atomically inserts the session if absent and returns the persisted winner.
func (s *BrowserStore) GetOrCreateApplicationBrowserID(ctx context.Context, jobApplicationID uint, provider BrowserProvider, proposedID uuid.UUID) (uuid.UUID, error) {
	if err := s.ready(); err != nil {
		return uuid.Nil, err
	}
	if jobApplicationID == 0 || !provider.Valid() || proposedID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("application id, valid browser provider, and non-empty proposed browser id are required")
	}
	row := BrowserSession{JobApplicationID: jobApplicationID, ApplicationBrowserID: proposedID, Provider: provider, Status: BrowserSessionStarting, ReplayGeneration: 1}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id_job_application"}, {Name: "provider"}}, DoNothing: true}).Omit(clause.Associations).Create(&row).Error; err != nil {
			return fmt.Errorf("insert application browser id: %w", err)
		}
		var persisted BrowserSession
		if err := tx.Where("id_job_application = ? AND provider = ?", jobApplicationID, provider).First(&persisted).Error; err != nil {
			return fmt.Errorf("load application browser id after insert: %w", err)
		}
		row.ApplicationBrowserID = persisted.ApplicationBrowserID
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return row.ApplicationBrowserID, nil
}

// BrowserSessionRef carries secret-bearing URLs only as opaque ciphertext.
type BrowserSessionRef struct {
	Provider                    BrowserProvider
	ProviderSessionID           string
	LiveViewURLCiphertext       []byte
	Status                      BrowserSessionStatus
	ReplayGeneration            uint64
	ReplayPending               bool
	CheckpointCreatedAt         time.Time
	CheckpointMutationID        uint64
	PendingCheckpointCreatedAt  *time.Time
	PendingCheckpointMutationID *uint64
	ExpiresAt                   time.Time
	Owner                       string
	LeaseExpiresAt              time.Time
	IdBrowserProfile            *uint
	IdBrowserVault              *uint
}

func (s *BrowserStore) GetBrowserSession(ctx context.Context, applicationBrowserID uuid.UUID) (BrowserSessionRef, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserSessionRef{}, false, err
	}
	if applicationBrowserID == uuid.Nil {
		return BrowserSessionRef{}, false, fmt.Errorf("application browser id is required")
	}
	var session BrowserSession
	err := s.db.WithContext(ctx).Where("application_browser_id = ?", applicationBrowserID).First(&session).Error
	if err == gorm.ErrRecordNotFound {
		return BrowserSessionRef{}, false, nil
	}
	if err != nil {
		return BrowserSessionRef{}, false, fmt.Errorf("get browser session: %w", err)
	}
	ref := BrowserSessionRef{Provider: session.Provider, ProviderSessionID: session.ProviderSessionID,
		LiveViewURLCiphertext: append([]byte(nil), session.LiveViewURLCiphertext...), Status: session.Status,
		ReplayGeneration: session.ReplayGeneration, ReplayPending: session.ReplayPending,
		IdBrowserProfile: session.BrowserProfileID, IdBrowserVault: session.BrowserVaultID, Owner: session.Owner}
	if session.CheckpointCreatedAt != nil {
		ref.CheckpointCreatedAt = *session.CheckpointCreatedAt
	}
	if session.CheckpointMutationID != nil {
		ref.CheckpointMutationID = uint64(*session.CheckpointMutationID)
	}
	if session.PendingCheckpointCreatedAt != nil {
		value := *session.PendingCheckpointCreatedAt
		ref.PendingCheckpointCreatedAt = &value
	}
	if session.PendingCheckpointMutationID != nil {
		value := uint64(*session.PendingCheckpointMutationID)
		ref.PendingCheckpointMutationID = &value
	}
	if session.ExpiresAt != nil {
		ref.ExpiresAt = *session.ExpiresAt
	}
	if session.LeaseExpiresAt != nil {
		ref.LeaseExpiresAt = *session.LeaseExpiresAt
	}
	return ref, true, nil
}

func (s *BrowserStore) SaveBrowserSession(ctx context.Context, applicationBrowserID uuid.UUID, ref BrowserSessionRef) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil || !ref.Provider.Valid() || !ref.Status.Valid() || ref.ReplayGeneration == 0 {
		return fmt.Errorf("application browser id, valid provider/status, and positive replay generation are required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current BrowserSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_browser_id = ?", applicationBrowserID).First(&current).Error; err != nil {
			return fmt.Errorf("load browser session before save: %w", err)
		}
		if current.Provider != ref.Provider || current.ReplayGeneration != ref.ReplayGeneration {
			return fmt.Errorf("browser session provider or generation changed; reload before saving")
		}
		if ref.IdBrowserProfile != nil {
			var profile BrowserProfile
			if err := tx.Select("id_browser_profile", "provider").First(&profile, *ref.IdBrowserProfile).Error; err != nil {
				return fmt.Errorf("load browser session profile: %w", err)
			}
			if profile.Provider != ref.Provider {
				return fmt.Errorf("browser session profile provider does not match session")
			}
		}
		if ref.IdBrowserVault != nil {
			var vault BrowserVault
			if err := tx.Select("id_browser_vault", "provider").First(&vault, *ref.IdBrowserVault).Error; err != nil {
				return fmt.Errorf("load browser session vault: %w", err)
			}
			if vault.Provider != ref.Provider {
				return fmt.Errorf("browser session vault provider does not match session")
			}
		}
		updates := map[string]any{"provider_session_id": ref.ProviderSessionID,
			"browser_live_view_url_ciphertext": ref.LiveViewURLCiphertext, "status": ref.Status,
			"id_browser_profile": ref.IdBrowserProfile, "id_browser_vault": ref.IdBrowserVault,
			"expires_at": nullableTime(ref.ExpiresAt), "updated_at": time.Now().UTC()}
		result := tx.Model(&BrowserSession{}).Where("id_browser_session = ?", current.IdBrowserSession).Updates(updates)
		if result.Error != nil {
			return fmt.Errorf("save browser session: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("save browser session: %w", gorm.ErrRecordNotFound)
		}
		return nil
	})
}

func (s *BrowserStore) CompleteBrowserReplay(ctx context.Context, applicationBrowserID uuid.UUID, providerSessionID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil || providerSessionID == "" {
		return fmt.Errorf("application browser ID and provider session ID are required")
	}
	result := s.db.WithContext(ctx).Model(&BrowserSession{}).
		Where("application_browser_id = ? AND provider_session_id = ? AND status = ? AND replay_pending = ?",
			applicationBrowserID, providerSessionID, BrowserSessionActive, true).
		Update("replay_pending", false)
	if result.Error != nil {
		return fmt.Errorf("complete browser replay: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return nil
	}
	var session BrowserSession
	if err := s.db.WithContext(ctx).Select("provider_session_id", "status", "replay_pending").
		Where("application_browser_id = ?", applicationBrowserID).First(&session).Error; err != nil {
		return fmt.Errorf("verify completed browser replay: %w", err)
	}
	if session.ProviderSessionID != providerSessionID || session.Status != BrowserSessionActive || session.ReplayPending {
		return fmt.Errorf("complete browser replay: session changed before completion")
	}
	return nil
}

func nullableTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (s *BrowserStore) ClearBrowserSessionActive(ctx context.Context, applicationBrowserID uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil {
		return fmt.Errorf("application browser id is required")
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&BrowserSession{}).Where("application_browser_id = ?", applicationBrowserID).Updates(map[string]any{
		"provider_session_id": "", "browser_live_view_url_ciphertext": nil, "status": BrowserSessionClosed, "closed_at": now, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("clear active browser session: %w", result.Error)
	}
	return nil
}

// AcquireBrowserSessionLease atomically acquires or renews the session's single-writer lease.
func (s *BrowserStore) AcquireBrowserSessionLease(ctx context.Context, applicationBrowserID uuid.UUID, owner string, expiresAt time.Time) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if applicationBrowserID == uuid.Nil || owner == "" || !expiresAt.After(time.Now()) {
		return false, fmt.Errorf("application browser id, owner, and future lease expiry are required")
	}
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&BrowserSession{}).Where("application_browser_id = ? AND (owner = '' OR lease_expires_at IS NULL OR lease_expires_at <= ? OR owner = ?)", applicationBrowserID, now, owner).
		Updates(map[string]any{"owner": owner, "lease_expires_at": expiresAt.UTC(), "updated_at": now})
	if result.Error != nil {
		return false, fmt.Errorf("acquire browser session lease: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func (s *BrowserStore) ReleaseBrowserSessionLease(ctx context.Context, applicationBrowserID uuid.UUID, owner string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil || owner == "" {
		return fmt.Errorf("application browser id and lease owner are required")
	}
	result := s.db.WithContext(ctx).Model(&BrowserSession{}).Where("application_browser_id = ? AND owner = ?", applicationBrowserID, owner).
		Updates(map[string]any{"owner": "", "lease_expires_at": nil, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("release browser session lease: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("release browser session lease: %w", gorm.ErrRecordNotFound)
	}
	return nil
}

func (s *BrowserStore) SetPendingBrowserCheckpoint(ctx context.Context, applicationBrowserID uuid.UUID, createdAt time.Time, mutationID uint) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil || createdAt.IsZero() || mutationID == 0 {
		return fmt.Errorf("application browser id and complete pending checkpoint cursor are required")
	}
	result := s.db.WithContext(ctx).Model(&BrowserSession{}).Where("application_browser_id = ?", applicationBrowserID).Updates(map[string]any{
		"pending_checkpoint_created_at": createdAt, "pending_checkpoint_mutation_id": mutationID})
	if result.Error != nil {
		return fmt.Errorf("set pending browser checkpoint: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("set pending browser checkpoint: %w", gorm.ErrRecordNotFound)
	}
	return nil
}

func (s *BrowserStore) PromoteBrowserCheckpoint(ctx context.Context, applicationBrowserID uuid.UUID) error {
	if err := s.ready(); err != nil {
		return err
	}
	if applicationBrowserID == uuid.Nil {
		return fmt.Errorf("application browser id is required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session BrowserSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_browser_id = ?", applicationBrowserID).First(&session).Error; err != nil {
			return fmt.Errorf("load browser checkpoint: %w", err)
		}
		if session.PendingCheckpointCreatedAt == nil && session.PendingCheckpointMutationID == nil {
			return nil
		}
		if session.PendingCheckpointCreatedAt == nil || session.PendingCheckpointMutationID == nil {
			return fmt.Errorf("promote browser checkpoint: pending cursor is incomplete")
		}
		if err := tx.Model(&BrowserSession{}).Where("id_browser_session = ?", session.IdBrowserSession).Updates(map[string]any{
			"checkpoint_created_at": *session.PendingCheckpointCreatedAt, "checkpoint_mutation_id": *session.PendingCheckpointMutationID,
			"pending_checkpoint_created_at": nil, "pending_checkpoint_mutation_id": nil}).Error; err != nil {
			return fmt.Errorf("promote browser checkpoint: %w", err)
		}
		return nil
	})
}
