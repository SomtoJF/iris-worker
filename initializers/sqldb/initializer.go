package sqldb

import (
	"io"
	"log"
	"log/slog"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func ConnectToPostgres() (*gorm.DB, error) {
	var err error

	dsn := os.Getenv("DATABASE_URL")

	newLogger := logger.New(
		log.New(io.Discard, "", log.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second * 10,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: newLogger,
	})

	if err != nil {
		slog.Error("Failed to connect to database", "error", err)
		return nil, err
	}

	slog.Info("Successfully connected to the database")
	return db, nil
}
