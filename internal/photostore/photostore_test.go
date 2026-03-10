package photostore

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStoreSave_WritesFileAndReturnsPublicURL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewFileStore(dir, "https://api.test/photos/")

	file := bytes.NewReader([]byte("photo-bytes"))

	gotURL, err := store.Save(context.Background(), nopSeekFile{Reader: file}, ".jpg")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	const wantPrefix = "https://api.test/photos/"
	if !strings.HasPrefix(gotURL, wantPrefix) {
		t.Fatalf("url = %q, want prefix %q", gotURL, wantPrefix)
	}

	filename := filepath.Base(gotURL)
	gotBytes, err := os.ReadFile(filepath.Join(dir, filename))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(gotBytes) != "photo-bytes" {
		t.Fatalf("stored bytes = %q, want %q", string(gotBytes), "photo-bytes")
	}
}

type nopSeekFile struct {
	*bytes.Reader
}

func (f nopSeekFile) Close() error { return nil }
