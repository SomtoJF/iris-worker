package sqldb

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BrowserReplayMutation struct {
	IdBrowserMutationChangelog uint
	Operation                  string
	Context                    BrowserMutationContext
	Status                     BrowserMutationStatus
	CreatedAt                  time.Time
}

func (s *BrowserStore) ListBrowserMutationsForReplay(ctx context.Context, applicationBrowserID uuid.UUID) ([]BrowserReplayMutation, BrowserSessionRef, error) {
	session, found, err := s.GetBrowserSession(ctx, applicationBrowserID)
	if err != nil {
		return nil, BrowserSessionRef{}, err
	}
	if !found || session.ReplayGeneration == 0 || session.Status != BrowserSessionActive || session.ProviderSessionID == "" {
		return nil, BrowserSessionRef{}, fmt.Errorf("browser replay session or active generation is missing")
	}
	if session.Provider == BrowserProviderKernel && (session.PendingCheckpointCreatedAt != nil || session.PendingCheckpointMutationID != nil) {
		return nil, BrowserSessionRef{}, fmt.Errorf("browser replay blocked by an uncommitted Kernel profile checkpoint")
	}
	if session.Provider == BrowserProviderKernel && (session.CheckpointMutationID == 0) != session.CheckpointCreatedAt.IsZero() {
		return nil, BrowserSessionRef{}, fmt.Errorf("browser replay blocked by an incomplete Kernel checkpoint cursor")
	}

	var dbSession BrowserSession
	if err := s.db.WithContext(ctx).
		Select("id_browser_session", "provider", "replay_generation").
		Where("application_browser_id = ?", applicationBrowserID).First(&dbSession).Error; err != nil {
		return nil, BrowserSessionRef{}, fmt.Errorf("load browser replay session: %w", err)
	}
	if err := s.recoverInterruptedBrowserReplays(ctx, dbSession, session); err != nil {
		return nil, BrowserSessionRef{}, err
	}

	query := s.db.WithContext(ctx).
		Select("id_browser_mutation_changelog", "id_browser_session", "operation", "context", "status", "created_at").
		Model(&BrowserMutationChangelog{}).
		Where("id_browser_session = ? AND replay_generation = ?", dbSession.IdBrowserSession, session.ReplayGeneration)
	if session.Provider == BrowserProviderKernel && session.CheckpointMutationID > 0 {
		query = query.Where("(created_at > ? OR (created_at = ? AND id_browser_mutation_changelog > ?))",
			session.CheckpointCreatedAt, session.CheckpointCreatedAt, session.CheckpointMutationID)
	}
	var rows []BrowserMutationChangelog
	if err := query.Order("created_at ASC, id_browser_mutation_changelog ASC").Find(&rows).Error; err != nil {
		return nil, BrowserSessionRef{}, fmt.Errorf("list browser mutations for replay: %w", err)
	}
	mutations := make([]BrowserReplayMutation, len(rows))
	for i, row := range rows {
		mutations[i] = BrowserReplayMutation{
			IdBrowserMutationChangelog: row.IdBrowserMutationChangelog,
			Operation:                  row.Operation,
			Context:                    row.Context,
			Status:                     row.Status,
			CreatedAt:                  row.CreatedAt,
		}
	}
	return mutations, session, nil
}

func (s *BrowserStore) recoverInterruptedBrowserReplays(ctx context.Context, session BrowserSession, sessionRef BrowserSessionRef) error {
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&BrowserMutationChangelog{}).
			Where("id_browser_session = ? AND replay_generation = ? AND status = ?",
				session.IdBrowserSession, session.ReplayGeneration, BrowserMutationPending)
		if session.Provider == BrowserProviderKernel && sessionRef.CheckpointMutationID > 0 {
			query = query.Where("(created_at > ? OR (created_at = ? AND id_browser_mutation_changelog > ?))",
				sessionRef.CheckpointCreatedAt, sessionRef.CheckpointCreatedAt, sessionRef.CheckpointMutationID)
		}
		var rows []BrowserMutationChangelog
		if err := query.Clauses(clause.Locking{Strength: "UPDATE"}).Find(&rows).Error; err != nil {
			return fmt.Errorf("load interrupted browser replays: %w", err)
		}
		for _, row := range rows {
			if row.Result == nil || row.Result.ReplaySessionID == "" || row.Result.ReplaySessionID == sessionRef.ProviderSessionID {
				continue
			}
			result := tx.Model(&BrowserMutationChangelog{}).
				Where("id_browser_mutation_changelog = ? AND status = ?", row.IdBrowserMutationChangelog, BrowserMutationPending).
				Updates(map[string]any{
					"status":       BrowserMutationApplied,
					"result":       nil,
					"completed_at": nil,
					"updated_at":   time.Now().UTC(),
				})
			if result.Error != nil {
				return fmt.Errorf("recover interrupted browser replay %d: %w", row.IdBrowserMutationChangelog, result.Error)
			}
			if result.RowsAffected == 0 {
				return fmt.Errorf("recover interrupted browser replay %d: %w", row.IdBrowserMutationChangelog, ErrBrowserMutationStateConflict)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recover interrupted browser replays: %w", err)
	}
	return nil
}

func (s *BrowserStore) GetBrowserMutationForReplay(ctx context.Context, applicationBrowserID uuid.UUID, mutationID uint) (BrowserMutationChangelog, []byte, error) {
	if err := s.ready(); err != nil {
		return BrowserMutationChangelog{}, nil, err
	}
	if applicationBrowserID == uuid.Nil || mutationID == 0 {
		return BrowserMutationChangelog{}, nil, fmt.Errorf("application browser ID and mutation ID are required")
	}
	var row BrowserMutationChangelog
	if err := s.db.WithContext(ctx).
		Joins("JOIN browser_session ON browser_session.id_browser_session = browser_mutation_changelog.id_browser_session").
		Where("browser_session.application_browser_id = ? AND browser_mutation_changelog.id_browser_mutation_changelog = ?",
			applicationBrowserID, mutationID).
		First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return BrowserMutationChangelog{}, nil, fmt.Errorf("browser mutation %d not found for session", mutationID)
		}
		return BrowserMutationChangelog{}, nil, fmt.Errorf("load browser mutation for replay: %w", err)
	}
	if row.Status != BrowserMutationApplied || !row.Context.ReplaySafe {
		return BrowserMutationChangelog{}, nil, fmt.Errorf("browser mutation %d is not applied and replay-safe", mutationID)
	}
	session, found, err := s.GetBrowserSession(ctx, applicationBrowserID)
	if err != nil {
		return BrowserMutationChangelog{}, nil, err
	}
	if !found || session.ReplayGeneration != row.ReplayGeneration || session.Provider != row.Provider {
		return BrowserMutationChangelog{}, nil, fmt.Errorf("browser mutation %d does not belong to the active replay generation", mutationID)
	}
	plaintext, err := decryptBrowserSecret(row.ArgumentsCiphertext, applicationBrowserID)
	if err != nil {
		return BrowserMutationChangelog{}, nil, fmt.Errorf("decrypt browser mutation arguments: %w", err)
	}
	return row, []byte(plaintext), nil
}

func (s *BrowserStore) MarkBrowserMutationReplayPending(ctx context.Context, mutationID uint, providerSessionID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if mutationID == 0 || providerSessionID == "" {
		return fmt.Errorf("browser mutation ID and provider session ID are required")
	}
	resultValue := &BrowserMutationResult{
		Outcome:         BrowserMutationOutcomeUncertain,
		ReplaySafe:      true,
		ReplaySessionID: providerSessionID,
	}
	result := s.db.WithContext(ctx).Model(&BrowserMutationChangelog{}).
		Where("id_browser_mutation_changelog = ? AND status = ?", mutationID, BrowserMutationApplied).
		Updates(map[string]any{"status": BrowserMutationPending, "result": resultValue, "completed_at": nil, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("mark browser mutation replay pending: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("mark browser mutation replay pending: %w", ErrBrowserMutationStateConflict)
	}
	return nil
}
