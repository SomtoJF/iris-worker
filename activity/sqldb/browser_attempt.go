package sqldb

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *BrowserStore) BeginBrowserReplayGeneration(ctx context.Context, applicationBrowserID uuid.UUID, workflowID string) (uint64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if applicationBrowserID == uuid.Nil || workflowID == "" {
		return 0, fmt.Errorf("application browser ID and workflow ID are required")
	}

	var generation uint64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session BrowserSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("application_browser_id = ?", applicationBrowserID).First(&session).Error; err != nil {
			return fmt.Errorf("load browser session for retry generation: %w", err)
		}

		var prior BrowserReplayAttempt
		err := tx.Where("id_browser_session = ? AND workflow_id = ?", session.IdBrowserSession, workflowID).First(&prior).Error
		if err == nil {
			generation = prior.ReplayGeneration
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return fmt.Errorf("find browser replay attempt: %w", err)
		}

		generation = session.ReplayGeneration + 1
		attempt := BrowserReplayAttempt{
			BrowserSessionID: session.IdBrowserSession,
			WorkflowID:       workflowID,
			ReplayGeneration: generation,
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return fmt.Errorf("save browser replay attempt: %w", err)
		}
		if err := tx.Model(&BrowserSession{}).Where("id_browser_session = ?", session.IdBrowserSession).
			Updates(map[string]any{
				"replay_generation":              generation,
				"checkpoint_created_at":          nil,
				"checkpoint_mutation_id":         nil,
				"pending_checkpoint_created_at":  nil,
				"pending_checkpoint_mutation_id": nil,
			}).Error; err != nil {
			return fmt.Errorf("advance browser replay generation: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return generation, nil
}
