package fs

import (
	"os"
	"path/filepath"
)

type TemporaryFileSystem struct {
	basePath string
}

func NewTemporaryFilesystem() *TemporaryFileSystem {
	dirName, err := os.MkdirTemp("", "go-rod*")
	if err != nil {
		panic(err)
	}
	return &TemporaryFileSystem{
		basePath: dirName,
	}
}

func (t *TemporaryFileSystem) GetBasePath() string {
	return t.basePath
}

func (t *TemporaryFileSystem) CreateFile(fileName string) string {
	filePath := filepath.Join(t.basePath, fileName)
	os.Create(filePath)
	return filePath
}

func (t *TemporaryFileSystem) ConcatenatePath(fileName string) string {
	return filepath.Join(t.basePath, fileName)
}

func (t *TemporaryFileSystem) Cleanup() {
	os.RemoveAll(t.basePath)
}
