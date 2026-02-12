package common

import (
	"os"

	"github.com/SomtoJF/iris-worker/aipi"
	"github.com/revrost/go-openrouter"
)

type Dependencies interface {
	GetAIPIClient() *aipi.AIPIClient
	Cleanup()
}

type dependencies struct {
	aipiClient *aipi.AIPIClient
}

func (d *dependencies) GetAIPIClient() *aipi.AIPIClient {
	return d.aipiClient
}

func (d *dependencies) Cleanup() {

}

func MakeDependencies() (Dependencies, error) {
	return &dependencies{
		aipiClient: aipi.NewAIPIClient(openrouter.NewClient(os.Getenv("OPENROUTER_API_KEY"))),
	}, nil
}
