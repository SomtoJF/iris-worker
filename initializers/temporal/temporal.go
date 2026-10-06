package temporal

import (
	"fmt"
	"os"

	"go.temporal.io/sdk/client"
	temporallog "go.temporal.io/sdk/log"
)

func ConnectToTemporal(logger temporallog.Logger) (client.Client, error) {
	temporalHost := os.Getenv("TEMPORAL_HOST")
	if temporalHost == "" {
		temporalHost = "127.0.0.1:7233"
	}

	nameSpace := os.Getenv("TEMPORAL_NAMESPACE")
	if nameSpace == "" {
		nameSpace = "default"
	}

	clientOptions := client.Options{
		HostPort:  temporalHost,
		Namespace: nameSpace,
		Logger:    logger,
	}

	c, err := client.Dial(clientOptions)

	if err != nil {
		return nil, fmt.Errorf("unable to create Temporal client: %w", err)
	}

	return c, nil
}
