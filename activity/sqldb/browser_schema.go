package sqldb

import (
	"fmt"

	"gorm.io/gorm"
)

// MigrateBrowserSchema registers the provider-neutral browser persistence schema.
func MigrateBrowserSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("browser schema migration: database is unavailable")
	}
	if err := db.AutoMigrate(&UserAction{}, &BrowserVault{}, &BrowserProfile{}, &BrowserAuthConnection{}, &BrowserSession{}, &BrowserReplayAttempt{}, &BrowserMutationChangelog{}); err != nil {
		return fmt.Errorf("migrate browser persistence schema: %w", err)
	}
	return nil
}
