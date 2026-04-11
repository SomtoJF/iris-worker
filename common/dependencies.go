package common

import (
	"fmt"
	"os"

	"github.com/SomtoJF/iris-worker/aipi"
	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	"github.com/SomtoJF/iris-worker/initializers/s3"
	"github.com/SomtoJF/iris-worker/initializers/sqldb"
	"github.com/SomtoJF/iris-worker/initializers/temporal"
	s3pkg "github.com/SomtoJF/iris-worker/pkg/s3"
	"github.com/revrost/go-openrouter"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Dependencies interface {
	GetDB() *gorm.DB
	GetAIPIClient() *aipi.AIPIClient
	GetBrowserClient() browserfactory.BrowserClient
	GetS3Manager() *s3pkg.S3Manager
	GetTemporalClient() client.Client
	Cleanup()
}

type dependencies struct {
	db             *gorm.DB
	temporalClient client.Client
	aipiClient     *aipi.AIPIClient
	browserClient  browserfactory.BrowserClient
	fs             *fs.TemporaryFileSystem
	s3Manager      *s3pkg.S3Manager
}

func (d *dependencies) GetAIPIClient() *aipi.AIPIClient {
	return d.aipiClient
}

func (d *dependencies) GetBrowserClient() browserfactory.BrowserClient {
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

func (d *dependencies) Cleanup() {
	d.fs.Cleanup()
	d.temporalClient.Close()
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
	openrouterClient := openrouter.NewClient(apiKey)
	browserClient, err := browserfactory.NewBrowserFactory(fs)
	if err != nil {
		return nil, fmt.Errorf("browser: %w", err)
	}

	s3Client, err := s3.InitializeS3()
	if err != nil {
		return nil, fmt.Errorf("s3: %w", err)
	}

	bucket := os.Getenv("AWS_BUCKET")
	s3Manager := s3pkg.NewS3Manager(s3Client, bucket)

	temporalClient, err := temporal.ConnectToTemporal()
	if err != nil {
		return nil, fmt.Errorf("temporal: %w", err)
	}

	return &dependencies{
		db:             db,
		aipiClient:     aipi.NewAIPIClient(openrouterClient, db),
		browserClient:  browserClient,
		fs:             fs,
		s3Manager:      s3Manager,
		temporalClient: temporalClient,
	}, nil
}
