package sqldb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrBrowserMutationStateConflict = errors.New("browser mutation is not in a reconcilable state")

type CreateBrowserMutationInput struct {
	IdBrowserSession    uint
	Provider            BrowserProvider
	ReplayGeneration    uint64
	Operation           string
	ArgumentsCiphertext []byte
	Context             BrowserMutationContext
	IdempotencyKey      string
}

// RecordBrowserMutation resolves the current session generation and stores the
// encrypted arguments before the browser operation is attempted.
func (s *BrowserStore) RecordBrowserMutation(ctx context.Context, applicationBrowserID uuid.UUID, operation string, arguments []byte, mutationContext BrowserMutationContext, idempotencyKey string) (BrowserMutationChangelog, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserMutationChangelog{}, false, err
	}
	if applicationBrowserID == uuid.Nil {
		return BrowserMutationChangelog{}, false, fmt.Errorf("application browser id is required")
	}
	if len(arguments) == 0 || !json.Valid(arguments) {
		return BrowserMutationChangelog{}, false, fmt.Errorf("browser mutation arguments must be valid JSON")
	}
	var session BrowserSession
	if err := s.db.WithContext(ctx).Where("application_browser_id = ?", applicationBrowserID).First(&session).Error; err != nil {
		return BrowserMutationChangelog{}, false, fmt.Errorf("load browser mutation session: %w", err)
	}
	if session.Status != BrowserSessionActive || session.ProviderSessionID == "" {
		return BrowserMutationChangelog{}, false, fmt.Errorf("browser mutation requires an active browser session")
	}
	ciphertext, err := encryptBrowserSecret(string(arguments), applicationBrowserID)
	if err != nil {
		return BrowserMutationChangelog{}, false, fmt.Errorf("encrypt browser mutation arguments: %w", err)
	}
	return s.InsertOrGetBrowserMutation(ctx, CreateBrowserMutationInput{
		IdBrowserSession:    session.IdBrowserSession,
		Provider:            session.Provider,
		ReplayGeneration:    session.ReplayGeneration,
		Operation:           operation,
		ArgumentsCiphertext: ciphertext,
		Context:             mutationContext,
		IdempotencyKey:      idempotencyKey,
	})
}

// InsertOrGetBrowserMutation is idempotent within a session/replay generation/key tuple.
func (s *BrowserStore) InsertOrGetBrowserMutation(ctx context.Context, input CreateBrowserMutationInput) (BrowserMutationChangelog, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserMutationChangelog{}, false, err
	}
	row := BrowserMutationChangelog{BrowserSessionID: input.IdBrowserSession, Provider: input.Provider,
		ReplayGeneration: input.ReplayGeneration, Operation: input.Operation,
		ArgumentsCiphertext: append([]byte(nil), input.ArgumentsCiphertext...), Context: input.Context,
		IdempotencyKey: input.IdempotencyKey, Status: BrowserMutationPending}
	if err := row.Validate(); err != nil {
		return BrowserMutationChangelog{}, false, err
	}
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session BrowserSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id_browser_session", "provider", "replay_generation").First(&session, input.IdBrowserSession).Error; err != nil {
			return fmt.Errorf("load browser mutation session: %w", err)
		}
		if session.Provider != input.Provider || session.ReplayGeneration != input.ReplayGeneration {
			return fmt.Errorf("browser mutation provider or replay generation does not match the active session")
		}
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id_browser_session"}, {Name: "replay_generation"}, {Name: "idempotency_key"}}, DoNothing: true}).Omit(clause.Associations).Create(&row)
		if result.Error != nil {
			return fmt.Errorf("insert browser mutation: %w", result.Error)
		}
		created = result.RowsAffected > 0
		if !created {
			if err := tx.Where("id_browser_session = ? AND replay_generation = ? AND idempotency_key = ?", input.IdBrowserSession, input.ReplayGeneration, input.IdempotencyKey).First(&row).Error; err != nil {
				return fmt.Errorf("load idempotent browser mutation: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return BrowserMutationChangelog{}, false, err
	}
	return row, created, nil
}

type BrowserMutationCursor struct {
	CreatedAt  time.Time
	MutationID uint
}

func (s *BrowserStore) LatestBrowserMutationCursor(ctx context.Context, applicationBrowserID uuid.UUID, generation uint64) (BrowserMutationCursor, bool, error) {
	if err := s.ready(); err != nil {
		return BrowserMutationCursor{}, false, err
	}
	if applicationBrowserID == uuid.Nil || generation == 0 {
		return BrowserMutationCursor{}, false, fmt.Errorf("application browser ID and replay generation are required")
	}
	var session BrowserSession
	if err := s.db.WithContext(ctx).Select("id_browser_session", "replay_generation").
		Where("application_browser_id = ?", applicationBrowserID).First(&session).Error; err != nil {
		return BrowserMutationCursor{}, false, fmt.Errorf("load browser session for mutation cursor: %w", err)
	}
	if session.ReplayGeneration != generation {
		return BrowserMutationCursor{}, false, fmt.Errorf("browser mutation cursor generation changed")
	}
	var latest BrowserMutationChangelog
	err := s.db.WithContext(ctx).
		Select("id_browser_mutation_changelog", "created_at").
		Where("id_browser_session = ? AND replay_generation = ?", session.IdBrowserSession, generation).
		Order("created_at DESC, id_browser_mutation_changelog DESC").
		First(&latest).Error
	if err == gorm.ErrRecordNotFound {
		return BrowserMutationCursor{}, false, nil
	}
	if err != nil {
		return BrowserMutationCursor{}, false, fmt.Errorf("get latest browser mutation cursor: %w", err)
	}
	return BrowserMutationCursor{CreatedAt: latest.CreatedAt, MutationID: latest.IdBrowserMutationChangelog}, true, nil
}

// ListAppliedBrowserMutations returns only this generation's applied rows in canonical cursor order.
func (s *BrowserStore) ListAppliedBrowserMutations(ctx context.Context, sessionID uint, generation uint64, after *BrowserMutationCursor) ([]BrowserMutationChangelog, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if sessionID == 0 || generation == 0 {
		return nil, fmt.Errorf("browser session and replay generation are required")
	}
	query := s.db.WithContext(ctx).Where("id_browser_session = ? AND replay_generation = ? AND status = ?", sessionID, generation, BrowserMutationApplied)
	if after != nil {
		if after.CreatedAt.IsZero() || after.MutationID == 0 {
			return nil, fmt.Errorf("browser mutation cursor is incomplete")
		}
		query = query.Where("(created_at > ? OR (created_at = ? AND id_browser_mutation_changelog > ?))", after.CreatedAt, after.CreatedAt, after.MutationID)
	}
	var rows []BrowserMutationChangelog
	if err := query.Order("created_at ASC, id_browser_mutation_changelog ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list applied browser mutations: %w", err)
	}
	return rows, nil
}

func (s *BrowserStore) ListPendingBrowserMutations(ctx context.Context, sessionID uint, generation uint64) ([]BrowserMutationChangelog, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if sessionID == 0 || generation == 0 {
		return nil, fmt.Errorf("browser session and replay generation are required")
	}
	var rows []BrowserMutationChangelog
	if err := s.db.WithContext(ctx).Where("id_browser_session = ? AND replay_generation = ? AND status = ?", sessionID, generation, BrowserMutationPending).
		Order("created_at ASC, id_browser_mutation_changelog ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list pending browser mutations: %w", err)
	}
	return rows, nil
}

// ListBrowserMutationsRequiringReconciliation returns uncertain operations in canonical order.
func (s *BrowserStore) ListBrowserMutationsRequiringReconciliation(ctx context.Context, sessionID uint, generation uint64) ([]BrowserMutationChangelog, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if sessionID == 0 || generation == 0 {
		return nil, fmt.Errorf("browser session and replay generation are required")
	}
	var rows []BrowserMutationChangelog
	if err := s.db.WithContext(ctx).Where("id_browser_session = ? AND replay_generation = ? AND status IN ?", sessionID, generation, []BrowserMutationStatus{BrowserMutationPending, BrowserMutationReconcileRequired}).
		Order("created_at ASC, id_browser_mutation_changelog ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list browser mutations requiring reconciliation: %w", err)
	}
	return rows, nil
}

func (s *BrowserStore) MarkBrowserMutationApplied(ctx context.Context, mutationID uint, sanitizedResult *BrowserMutationResult) error {
	return s.setBrowserMutationStatus(ctx, mutationID, BrowserMutationApplied, sanitizedResult)
}

func (s *BrowserStore) MarkBrowserMutationFailed(ctx context.Context, mutationID uint, sanitizedResult *BrowserMutationResult) error {
	return s.setBrowserMutationStatus(ctx, mutationID, BrowserMutationFailed, sanitizedResult)
}

func (s *BrowserStore) MarkBrowserMutationReconcileRequired(ctx context.Context, mutationID uint, sanitizedResult *BrowserMutationResult) error {
	return s.setBrowserMutationStatus(ctx, mutationID, BrowserMutationReconcileRequired, sanitizedResult)
}

// ReconcileBrowserMutation finalizes a pending/uncertain operation after inspecting browser state.
func (s *BrowserStore) ReconcileBrowserMutation(ctx context.Context, mutationID uint, status BrowserMutationStatus, sanitizedResult *BrowserMutationResult) error {
	if status != BrowserMutationApplied && status != BrowserMutationFailed && status != BrowserMutationReconcileRequired {
		return fmt.Errorf("reconciliation status is invalid")
	}
	return s.setBrowserMutationStatus(ctx, mutationID, status, sanitizedResult)
}

func (s *BrowserStore) setBrowserMutationStatus(ctx context.Context, mutationID uint, status BrowserMutationStatus, result *BrowserMutationResult) error {
	if err := s.ready(); err != nil {
		return err
	}
	if mutationID == 0 || !status.Valid() || status == BrowserMutationPending {
		return fmt.Errorf("browser mutation id and terminal status are required")
	}
	if result != nil {
		if err := result.Validate(); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	update := s.db.WithContext(ctx).Model(&BrowserMutationChangelog{}).
		Where("id_browser_mutation_changelog = ? AND status IN ?", mutationID, []BrowserMutationStatus{BrowserMutationPending, BrowserMutationReconcileRequired}).
		Updates(map[string]any{"status": status, "result": result, "completed_at": now, "updated_at": now})
	if update.Error != nil {
		return fmt.Errorf("update browser mutation status: %w", update.Error)
	}
	if update.RowsAffected == 0 {
		return fmt.Errorf("update browser mutation status: %w", ErrBrowserMutationStateConflict)
	}
	return nil
}
