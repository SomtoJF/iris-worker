package posthog

import (
	"os"

	posthoglib "github.com/posthog/posthog-go"
)

var PosthogClient posthoglib.Client

func NewPosthog() error {
	var err error
	PosthogClient, err = posthoglib.NewWithConfig(os.Getenv("POSTHOG_API_KEY"), posthoglib.Config{
		Endpoint: os.Getenv("POSTHOG_ENDPOINT"),
	})

	return err
}

func ClosePosthog() {
	PosthogClient.Close()
}
