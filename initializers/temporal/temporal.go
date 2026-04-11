package temporal

import (
	"log"
	"os"

	"go.temporal.io/sdk/client"
)

func ConnectToTemporal() (client.Client, error) {
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
	}

	c, err := client.Dial(clientOptions)

	if err != nil {
		log.Fatalln("Unable to create Temporal client:", err)
	}

	return c, nil
}
