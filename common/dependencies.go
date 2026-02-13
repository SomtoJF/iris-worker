package common

import (
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
	fs := fs.NewTemporaryFilesystem()
	return &dependencies{
		aipiClient:    aipi.NewAIPIClient(openrouter.NewClient(os.Getenv("OPENROUTER_API_KEY"))),
		browserClient: browserfactory.NewBrowserFactory(fs),
		fs:            fs,
	}, nil
}
