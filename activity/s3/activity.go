package s3

import (
	"context"

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
}

type DownloadFileOutput struct {
	Path string
}

func (a *Activity) DownloadFile(ctx context.Context, input DownloadFileInput) (DownloadFileOutput, error) {
	err := a.s3.DownloadFileToPath(ctx, input.Key, input.DestPath)
	if err != nil {
		return DownloadFileOutput{}, err
	}
	return DownloadFileOutput{Path: input.DestPath}, nil
}
