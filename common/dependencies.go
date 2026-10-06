package common

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	sqldbActivity "github.com/SomtoJF/iris-worker/activity/sqldb"
	"github.com/SomtoJF/iris-worker/aipi"
	"github.com/SomtoJF/iris-worker/browser"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	posthogInit "github.com/SomtoJF/iris-worker/initializers/posthog"
	"github.com/SomtoJF/iris-worker/initializers/s3"
	"github.com/SomtoJF/iris-worker/initializers/sqldb"
	"github.com/SomtoJF/iris-worker/initializers/temporal"
	s3pkg "github.com/SomtoJF/iris-worker/pkg/s3"
	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Dependencies interface {
	GetDB() *gorm.DB
	GetAIPIClient() *aipi.AIPIClient
	GetBrowserClient() browser.BrowserClient
	GetS3Manager() *s3pkg.S3Manager
	GetTemporalClient() client.Client
	GetPosthogClient() posthog.Client
	Cleanup()
}

type dependencies struct {
	db             *gorm.DB
	temporalClient client.Client
	aipiClient     *aipi.AIPIClient
	browserClient  browser.BrowserClient
	fs             *fs.TemporaryFileSystem
	s3Manager      *s3pkg.S3Manager
	posthogClient  posthog.Client
}

func (d *dependencies) GetAIPIClient() *aipi.AIPIClient {
	return d.aipiClient
}

func (d *dependencies) GetBrowserClient() browser.BrowserClient {
	return d.browserClient
}

func (d *dependencies) GetS3Manager() *s3pkg.S3Manager {
	return d.s3Manager
}

func (d *dependencies) GetDB() *gorm.DB {
	return d.db
}

func (d *dependencies) GetTemporalClient() client.Client {
	return d.temporalClient
}

func (d *dependencies) GetPosthogClient() posthog.Client {
	return d.posthogClient
}

func (d *dependencies) Cleanup() {
	d.fs.Cleanup()
	d.temporalClient.Close()
	posthogInit.ClosePosthog()
}

func MakeDependencies() (Dependencies, error) {

	db, err := sqldb.ConnectToPostgres()
	if err != nil {
		return nil, fmt.Errorf("sqldb: %w", err)
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	fs := fs.NewTemporaryFilesystem()
	sessionStore, err := sqldbActivity.NewBrowserSessionStore(db)
	if err != nil {
		fs.Cleanup()
		return nil, fmt.Errorf("browser session store: %w", err)
	}

	workerID := uuid.NewString()
	browserClient, err := browser.NewBrowserClient(browser.ClientTypeKernel, browser.Config{
		SessionStore: sessionStore,
		TempFS:       fs,
		WorkerID:     workerID,
		KernelAPIKey: os.Getenv("KERNEL_API_KEY"),
	})
	if err != nil {
		fs.Cleanup()
		return nil, fmt.Errorf("browser: %w", err)
	}

	s3Client, err := s3.InitializeS3()
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}

	bucket := os.Getenv("AWS_BUCKET")
	s3Manager := s3pkg.NewS3Manager(s3Client, bucket)

	err = posthogInit.NewPosthog()
	if err != nil {
		return nil, fmt.Errorf("posthog: %w", err)
	}

	posthogClient := posthogInit.PosthogClient

	baseHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(posthog.NewSlogCaptureHandler(baseHandler, posthogClient,
		posthog.WithDistinctIDFn(func(_ context.Context, r slog.Record) string {
			var workflowID string
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "WorkflowID" {
					workflowID = a.Value.String()
					return false
				}
				return true
			})
			return workflowID
		}),
	))

	temporalLogger := NewTemporalSlogLogger(logger)
	temporalClient, err := temporal.ConnectToTemporal(temporalLogger)
	if err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}

	return &dependencies{
		db:             db,
		aipiClient:     aipi.NewAIPIClient(apiKey, db),
		browserClient:  browserClient,
		fs:             fs,
		s3Manager:      s3Manager,
		temporalClient: temporalClient,
		posthogClient:  posthogClient,
	}, nil
}
