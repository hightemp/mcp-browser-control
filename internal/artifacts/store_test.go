package artifacts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestStorePutReadAndReload(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store, err := New(
		directory,
		time.Hour,
		WithClock(func() time.Time { return now }),
		WithMaxBytes(1024),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	metadata, err := store.Put(
		context.Background(),
		"application/json; charset=utf-8",
		[]byte(`{"token":"[redacted]"}`),
		RedactionMetadata{Rules: []string{"authorization", " authorization ", "cookie"}},
	)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if err := validateID(metadata.ID); err != nil {
		t.Fatalf("generated ID = %q: %v", metadata.ID, err)
	}
	if metadata.URI != URI(metadata.ID) || metadata.Size != 22 ||
		metadata.MIMEType != "application/json; charset=utf-8" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if !metadata.Redaction.Applied ||
		!reflect.DeepEqual(metadata.Redaction.Rules, []string{"authorization", "cookie"}) {
		t.Fatalf("redaction = %#v", metadata.Redaction)
	}

	gotMetadata, data, err := store.Read(context.Background(), metadata.ID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !reflect.DeepEqual(gotMetadata, metadata) || string(data) != `{"token":"[redacted]"}` {
		t.Fatalf("Read() = (%#v, %q)", gotMetadata, data)
	}
	assertPrivateMode(t, directory, 0o700)
	assertPrivateMode(t, store.contentPath(metadata.ID), 0o600)
	assertPrivateMode(t, store.metadataPath(metadata.ID), 0o600)

	reloaded, err := New(
		directory,
		time.Hour,
		WithClock(func() time.Time { return now }),
		WithMaxBytes(1024),
	)
	if err != nil {
		t.Fatalf("reload New() error = %v", err)
	}
	if reloaded.UsedBytes() != metadata.Size {
		t.Fatalf("reloaded UsedBytes() = %d", reloaded.UsedBytes())
	}
	if _, reloadedData, err := reloaded.Read(context.Background(), metadata.ID); err != nil || string(reloadedData) != string(data) {
		t.Fatalf("reloaded Read() = (%q, %v)", reloadedData, err)
	}
}

func TestStoreEnforcesQuotaByEvictingOldest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store, err := New(
		t.TempDir(),
		time.Hour,
		WithClock(func() time.Time { return now }),
		WithMaxBytes(5),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	oldest, err := store.Put(context.Background(), "text/plain", []byte("old"), RedactionMetadata{})
	if err != nil {
		t.Fatalf("first Put() error = %v", err)
	}
	now = now.Add(time.Second)
	newest, err := store.Put(context.Background(), "text/plain", []byte("new"), RedactionMetadata{})
	if err != nil {
		t.Fatalf("second Put() error = %v", err)
	}
	if store.UsedBytes() != 3 {
		t.Fatalf("UsedBytes() = %d, want 3", store.UsedBytes())
	}
	if _, _, err := store.Read(context.Background(), oldest.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oldest Read() error = %v", err)
	}
	if _, data, err := store.Read(context.Background(), newest.ID); err != nil || string(data) != "new" {
		t.Fatalf("newest Read() = (%q, %v)", data, err)
	}
	if _, err := store.Put(
		context.Background(),
		"application/octet-stream",
		make([]byte, 6),
		RedactionMetadata{},
	); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("oversized Put() error = %v", err)
	}
}

func TestStoreExpiresAndCleansArtifacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	store, err := New(
		t.TempDir(),
		time.Minute,
		WithClock(func() time.Time { return now }),
		WithMaxBytes(1024),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	metadata, err := store.Put(context.Background(), "text/html", []byte("<p>page</p>"), RedactionMetadata{})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	now = now.Add(time.Minute)
	removed, err := store.Cleanup()
	if err != nil || removed != 1 {
		t.Fatalf("Cleanup() = (%d, %v), want (1, nil)", removed, err)
	}
	if store.UsedBytes() != 0 {
		t.Fatalf("UsedBytes() = %d", store.UsedBytes())
	}
	if _, _, err := store.Read(context.Background(), metadata.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired Read() error = %v", err)
	}
	for _, path := range []string{store.contentPath(metadata.ID), store.metadataPath(metadata.ID)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("expired file %q still exists: %v", path, err)
		}
	}
}

func TestStoreRejectsInvalidInputsAndTraversal(t *testing.T) {
	t.Parallel()

	if _, err := New("", time.Minute); err == nil {
		t.Fatal("New(empty directory) error = nil")
	}
	if _, err := New(t.TempDir(), 0); err == nil {
		t.Fatal("New(zero TTL) error = nil")
	}
	if _, err := New(t.TempDir(), time.Minute, WithMaxBytes(0)); err == nil {
		t.Fatal("New(zero quota) error = nil")
	}

	store, err := New(t.TempDir(), time.Minute, WithMaxBytes(1024))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := store.Put(context.Background(), "not a mime type", nil, RedactionMetadata{}); err == nil {
		t.Fatal("Put(invalid MIME) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Put(cancelled, "text/plain", nil, RedactionMetadata{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(cancelled) error = %v", err)
	}
	for _, id := range []string{"../metadata", "/etc/passwd", stringsOf('a', idLength-1)} {
		if _, _, err := store.Read(context.Background(), id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Read(%q) error = %v", id, err)
		}
		if err := store.Delete(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Delete(%q) error = %v", id, err)
		}
	}
}

func TestStoreSupportsConcurrentWriters(t *testing.T) {
	t.Parallel()

	store, err := New(t.TempDir(), time.Hour, WithMaxBytes(1<<20))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	const count = 64
	ids := make(chan string, count)
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			metadata, putErr := store.Put(
				context.Background(),
				"application/octet-stream",
				[]byte{byte(index)},
				RedactionMetadata{},
			)
			if putErr != nil {
				t.Errorf("Put() error = %v", putErr)
				return
			}
			ids <- metadata.ID
		}()
	}
	group.Wait()
	close(ids)
	unique := make(map[string]struct{}, count)
	for id := range ids {
		unique[id] = struct{}{}
	}
	if len(unique) != count || store.UsedBytes() != count {
		t.Fatalf("unique IDs = %d, UsedBytes() = %d", len(unique), store.UsedBytes())
	}
}

func TestStoreRemovesOrphanedAndInvalidFilesOnLoad(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	id := stringsOf('A', idLength)
	if err := os.WriteFile(filepath.Join(directory, id+".bin"), []byte("orphan"), 0o600); err != nil {
		t.Fatalf("WriteFile(orphan) error = %v", err)
	}
	invalidID := stringsOf('B', idLength)
	if err := os.WriteFile(filepath.Join(directory, invalidID+".json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	if _, err := New(directory, time.Hour, WithMaxBytes(1024)); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(directory, id+".bin"),
		filepath.Join(directory, invalidID+".json"),
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("invalid store file %q remains: %v", path, err)
		}
	}
}

func assertPrivateMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode for %q = %04o, want %04o", path, got, want)
	}
}

func stringsOf(character byte, count int) string {
	return string(bytes.Repeat([]byte{character}, count))
}
