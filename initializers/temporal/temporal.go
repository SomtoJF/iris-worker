package temporal

import (
	stdlog "log"
	"os"

	"go.temporal.io/sdk/client"
	temporallog "go.temporal.io/sdk/log"
)

func ConnectToTemporal(logger temporallog.Logger) (client.Client, error) {
	temporalHost := os.Getenv("TEMPORAL_HOST")
	if temporalHost == "" {
		temporalHost = "localhost:7233"
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
		stdlog.Fatalln("Unable to create Temporal client:", err)
	}

	return c, nil
}
