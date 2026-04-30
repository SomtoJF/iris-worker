package s3

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	s3pkg "github.com/SomtoJF/iris-worker/pkg/s3"
)

type Activity struct {
	s3 *s3pkg.S3Manager
}

func NewActivity(s3 *s3pkg.S3Manager) *Activity {
	return &Activity{s3: s3}
}

type DownloadFileInput struct {
	Key      string
	DestPath string
	Filename string
}

type DownloadFileOutput struct {
	Path string
}

func (a *Activity) DownloadFile(ctx context.Context, input DownloadFileInput) (DownloadFileOutput, error) {
	destPath := input.DestPath
	if destPath == "" {
		dir, err := os.MkdirTemp("", "iris_download_*")
		if err != nil {
			return DownloadFileOutput{}, err
		}

		filename := input.Filename
		if filename == "" {
			return DownloadFileOutput{}, fmt.Errorf("filename is required when dest_path is empty")
		}
		destPath = filepath.Join(dir, filename)
	}

	err := a.s3.DownloadFileToPath(ctx, input.Key, destPath)
	if err != nil {
		return DownloadFileOutput{}, err
	}
	return DownloadFileOutput{Path: destPath}, nil
}
