package common

import (
	"fmt"
	"os"

	"github.com/SomtoJF/iris-worker/aipi"
	"github.com/SomtoJF/iris-worker/browserfactory"
	"github.com/SomtoJF/iris-worker/initializers/fs"
	"github.com/revrost/go-openrouter"
)

type Dependencies interface {
	GetAIPIClient() *aipi.AIPIClient
	GetBrowserClient() browserfactory.BrowserClient
	Cleanup()
}

type dependencies struct {
	aipiClient    *aipi.AIPIClient
	browserClient browserfactory.BrowserClient
	fs            *fs.TemporaryFileSystem
}

func (d *dependencies) GetAIPIClient() *aipi.AIPIClient {
	return d.aipiClient
}

func (d *dependencies) GetBrowserClient() browserfactory.BrowserClient {
	return d.browserClient
}

func (d *dependencies) Cleanup() {
	d.fs.Cleanup()
}

func MakeDependencies() (Dependencies, error) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY environment variable is not set")
	}

	fs := fs.NewTemporaryFilesystem()
	openrouterClient := openrouter.NewClient(apiKey)
	return &dependencies{
		aipiClient:    aipi.NewAIPIClient(openrouterClient),
		browserClient: browserfactory.NewBrowserFactory(fs),
		fs:            fs,
	}, nil
}
