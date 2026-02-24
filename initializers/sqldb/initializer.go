package sqldb

import (
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func ConnectToSQLite() (*gorm.DB, error) {
	var err error
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dbPath := homeDir + "/iris/db/gorm.db"

	DB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return DB, nil
}
