package sqldb

import (
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

func ConnectToSQLite() error {
	var err error
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dbPath := homeDir + "/iris/db/gorm.db"

	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return err
	}
	return nil
}
