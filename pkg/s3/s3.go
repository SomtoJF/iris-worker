package s3

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type S3Manager struct {
	client *s3.Client
	bucket string
}

func NewS3Manager(client *s3.Client, bucket string) *S3Manager {
	return &S3Manager{
		client: client,
		bucket: bucket,
	}
}

func (m *S3Manager) GenerateResumeKey(userId uint, filename string, uuid string) string {
	return fmt.Sprintf("%d/resumes/%s-%s", userId, filename, uuid)
}

func (m *S3Manager) UploadFile(ctx context.Context, key string, file io.Reader, contentType string) error {
	_, err := m.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(m.bucket),
		Key:         aws.String(key),
		Body:        file,
		ContentType: aws.String(contentType),
	})
	return err
}

func (m *S3Manager) GeneratePresignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(m.client)
	request, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiration))

	if err != nil {
		return "", err
	}

	return request.URL, nil
}

func (m *S3Manager) DeleteFile(ctx context.Context, key string) error {
	_, err := m.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(key),
	})
	return err
}

func (m *S3Manager) DownloadFileToPath(ctx context.Context, key string, destPath string) error {
	// Download file from S3
	result, err := m.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to download from S3: %w", err)
	}
	defer result.Body.Close()

	// Read entire file into memory
	data, err := io.ReadAll(result.Body)
	if err != nil {
		return fmt.Errorf("failed to read S3 object body: %w", err)
	}

	// Create parent directory if it doesn't exist
	parentDir := filepath.Dir(destPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	// Create file at destination path
	file, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file at %s: %w", destPath, err)
	}
	defer file.Close()

	// Write data to file
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	return nil
}
