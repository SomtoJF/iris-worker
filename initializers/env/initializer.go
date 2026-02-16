package env

import (
	"os"

	"github.com/joho/godotenv"
)

func LoadEnvVariables() error {
	if os.Getenv("RENDER") != "true" {
		err := godotenv.Load()
		if err != nil {
			return err
		}
	}
	return nil
}
