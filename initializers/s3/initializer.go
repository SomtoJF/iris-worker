package s3

import (
	"context"
	"errors"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func InitializeS3() (*s3.Client, error) {
	accessKey := os.Getenv("AWS_ACCESS_KEY")
	secretKey := os.Getenv("AWS_SECRET_KEY")
	region := os.Getenv("AWS_REGION")
	if accessKey == "" || secretKey == "" || region == "" {
		return nil, errors.New("AWS_ACCESS_KEY, AWS_SECRET_KEY, and AWS_REGION must be set")
	}

	ctx := context.Background()
	sdkConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			accessKey,
			secretKey,
			"",
		)),
	)
	if err != nil {
		return nil, err
	}

	return s3.NewFromConfig(sdkConfig), nil
}
