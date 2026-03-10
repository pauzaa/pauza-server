package photostore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

type Store interface {
	Save(ctx context.Context, file multipart.File, extension string) (string, error)
}

// FileStore writes uploaded photos into a deployment-provided writable
// directory on the server's local filesystem and returns a URL rooted at a
// deployment-provided public base URL. The Go API does not serve those files
// itself; a reverse proxy such as Nginx is expected to publish the storage
// directory at the configured public path.
type FileStore struct {
	dir           string
	publicBaseURL string
}

func NewFileStore(dir, publicBaseURL string) *FileStore {
	return &FileStore{
		dir:           dir,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
	}
}

func (s *FileStore) Save(_ context.Context, file multipart.File, extension string) (string, error) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", fmt.Errorf("creating photo storage dir: %w", err)
	}

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generating photo name: %w", err)
	}
	name := hex.EncodeToString(raw[:]) + extension
	path := filepath.Join(s.dir, name)

	dst, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("creating photo file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		return "", fmt.Errorf("writing photo file: %w", err)
	}

	return s.publicBaseURL + "/" + name, nil
}
