// Package artifacts stores bounded, temporary browser command results.
package artifacts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxBytes = int64(512 << 20)
	idByteCount     = 32
	idLength        = 43
)

var (
	// ErrNotFound indicates that an artifact does not exist or has expired.
	ErrNotFound = errors.New("artifact not found")
	// ErrQuotaExceeded indicates that one artifact cannot fit within the store quota.
	ErrQuotaExceeded = errors.New("artifact quota exceeded")
	// ErrInvalidID indicates that an artifact ID is not a generated safe identifier.
	ErrInvalidID = errors.New("invalid artifact ID")
)

// RedactionMetadata records which redaction rules were applied without
// retaining any original sensitive values.
type RedactionMetadata struct {
	Applied bool     `json:"applied"`
	Rules   []string `json:"rules"`
}

// Metadata describes a stored artifact.
type Metadata struct {
	ID        string            `json:"id"`
	URI       string            `json:"uri"`
	MIMEType  string            `json:"mimeType"`
	Size      int64             `json:"size"`
	CreatedAt time.Time         `json:"createdAt"`
	ExpiresAt time.Time         `json:"expiresAt"`
	Redaction RedactionMetadata `json:"redaction"`
}

// Option configures a Store.
type Option func(*Store)

// WithMaxBytes changes the total on-disk artifact quota.
func WithMaxBytes(maxBytes int64) Option {
	return func(store *Store) {
		store.maxBytes = maxBytes
	}
}

// WithClock changes the clock used for expiration, primarily for tests.
func WithClock(now func() time.Time) Option {
	return func(store *Store) {
		if now != nil {
			store.now = now
		}
	}
}

// WithRandom changes the cryptographic random source, primarily for tests.
func WithRandom(random io.Reader) Option {
	return func(store *Store) {
		if random != nil {
			store.random = random
		}
	}
}

// Store owns an artifact directory and its in-memory metadata index.
type Store struct {
	directory string
	ttl       time.Duration
	maxBytes  int64
	now       func() time.Time
	random    io.Reader

	mu        sync.RWMutex
	entries   map[string]Metadata
	usedBytes int64
}

// New opens an artifact store, removes expired or orphaned store files, and
// enforces the configured quota.
func New(directory string, ttl time.Duration, options ...Option) (*Store, error) {
	directory = strings.TrimSpace(directory)
	if directory == "" {
		return nil, errors.New("artifact directory is required")
	}
	if ttl <= 0 {
		return nil, errors.New("artifact TTL must be positive")
	}
	store := &Store{
		directory: directory,
		ttl:       ttl,
		maxBytes:  defaultMaxBytes,
		now:       time.Now,
		random:    rand.Reader,
		entries:   make(map[string]Metadata),
	}
	for _, option := range options {
		option(store)
	}
	if store.maxBytes <= 0 {
		return nil, errors.New("artifact quota must be positive")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("secure artifact directory: %w", err)
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Put stores data with an unpredictable ID and returns its metadata. Oldest
// artifacts are evicted when necessary, while an artifact larger than the
// total quota is rejected.
func (s *Store) Put(
	ctx context.Context,
	mimeType string,
	data []byte,
	redaction RedactionMetadata,
) (Metadata, error) {
	if err := contextError(ctx); err != nil {
		return Metadata{}, err
	}
	mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(mimeType))
	if err != nil || mediaType == "" {
		return Metadata{}, errors.New("artifact MIME type is invalid")
	}
	mimeType = mime.FormatMediaType(mediaType, parameters)
	size := int64(len(data))
	if size > s.maxBytes {
		return Metadata{}, ErrQuotaExceeded
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := contextError(ctx); err != nil {
		return Metadata{}, err
	}
	if err := s.makeRoomLocked(size); err != nil {
		return Metadata{}, err
	}
	id, err := s.newIDLocked()
	if err != nil {
		return Metadata{}, err
	}
	now := s.now().UTC()
	metadata := Metadata{
		ID:        id,
		URI:       URI(id),
		MIMEType:  mimeType,
		Size:      size,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
		Redaction: normalizeRedaction(redaction),
	}
	if err := writeAtomic(s.contentPath(id), data); err != nil {
		return Metadata{}, fmt.Errorf("write artifact content: %w", err)
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		_ = os.Remove(s.contentPath(id))
		return Metadata{}, fmt.Errorf("marshal artifact metadata: %w", err)
	}
	if err := writeAtomic(s.metadataPath(id), metadataJSON); err != nil {
		_ = os.Remove(s.contentPath(id))
		return Metadata{}, fmt.Errorf("write artifact metadata: %w", err)
	}
	s.entries[id] = cloneMetadata(metadata)
	s.usedBytes += size
	return cloneMetadata(metadata), nil
}

// Read returns metadata and a copy of artifact bytes.
func (s *Store) Read(ctx context.Context, id string) (Metadata, []byte, error) {
	if err := validateID(id); err != nil {
		return Metadata{}, nil, err
	}
	if err := contextError(ctx); err != nil {
		return Metadata{}, nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	metadata, ok := s.entries[id]
	if !ok {
		return Metadata{}, nil, ErrNotFound
	}
	if !s.now().UTC().Before(metadata.ExpiresAt) {
		if err := s.removeLocked(id); err != nil {
			return Metadata{}, nil, err
		}
		return Metadata{}, nil, ErrNotFound
	}
	data, err := os.ReadFile(s.contentPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			delete(s.entries, id)
			s.usedBytes -= metadata.Size
			return Metadata{}, nil, ErrNotFound
		}
		return Metadata{}, nil, fmt.Errorf("read artifact content: %w", err)
	}
	if int64(len(data)) != metadata.Size {
		return Metadata{}, nil, errors.New("artifact size does not match metadata")
	}
	return cloneMetadata(metadata), data, nil
}

// Delete removes one artifact. Repeated deletion is safe.
func (s *Store) Delete(id string) error {
	if err := validateID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[id]; !ok {
		return nil
	}
	return s.removeLocked(id)
}

// Cleanup removes expired artifacts and any oldest entries required to restore
// the configured quota. It returns the number of removed indexed artifacts.
func (s *Store) Cleanup() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.entries)
	if err := s.cleanupExpiredLocked(); err != nil {
		return before - len(s.entries), err
	}
	if err := s.enforceQuotaLocked(); err != nil {
		return before - len(s.entries), err
	}
	return before - len(s.entries), nil
}

// RunCleanup removes expired artifacts periodically until ctx is cancelled.
func (s *Store) RunCleanup(ctx context.Context) {
	interval := s.ttl / 2
	if interval > time.Hour {
		interval = time.Hour
	}
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.Cleanup()
		}
	}
}

// UsedBytes returns the indexed artifact content size.
func (s *Store) UsedBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usedBytes
}

// URI returns the MCP resource URI for an artifact ID.
func URI(id string) string {
	return "browser://artifacts/" + id
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	files, err := os.ReadDir(s.directory)
	if err != nil {
		return fmt.Errorf("read artifact directory: %w", err)
	}
	now := s.now().UTC()
	for _, file := range files {
		name := file.Name()
		if strings.HasPrefix(name, ".artifact-") {
			if err := os.Remove(filepath.Join(s.directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove incomplete artifact file: %w", err)
			}
			continue
		}
		id, ok := strings.CutSuffix(name, ".json")
		if file.IsDir() || !ok || validateID(id) != nil {
			continue
		}
		if !file.Type().IsRegular() {
			if err := s.removeFiles(id); err != nil {
				return err
			}
			continue
		}
		metadataJSON, err := os.ReadFile(s.metadataPath(id))
		if err != nil {
			return fmt.Errorf("read artifact metadata: %w", err)
		}
		var metadata Metadata
		if json.Unmarshal(metadataJSON, &metadata) != nil || !validMetadata(metadata, id) ||
			!now.Before(metadata.ExpiresAt) {
			if err := s.removeFiles(id); err != nil {
				return err
			}
			continue
		}
		info, err := os.Lstat(s.contentPath(id))
		if err != nil || !info.Mode().IsRegular() || info.Size() != metadata.Size {
			if err := s.removeFiles(id); err != nil {
				return err
			}
			continue
		}
		metadata.Redaction = normalizeRedaction(metadata.Redaction)
		if err := os.Chmod(s.contentPath(id), 0o600); err != nil {
			return fmt.Errorf("secure artifact content: %w", err)
		}
		if err := os.Chmod(s.metadataPath(id), 0o600); err != nil {
			return fmt.Errorf("secure artifact metadata: %w", err)
		}
		s.entries[id] = metadata
		s.usedBytes += metadata.Size
	}
	for _, file := range files {
		id, ok := strings.CutSuffix(file.Name(), ".bin")
		if file.IsDir() || !ok || validateID(id) != nil {
			continue
		}
		if _, indexed := s.entries[id]; !indexed {
			if err := os.Remove(s.contentPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove orphaned artifact content: %w", err)
			}
		}
	}
	return s.enforceQuotaLocked()
}

func (s *Store) makeRoomLocked(size int64) error {
	if err := s.cleanupExpiredLocked(); err != nil {
		return err
	}
	for s.usedBytes+size > s.maxBytes {
		oldest := s.oldestIDLocked()
		if oldest == "" {
			return ErrQuotaExceeded
		}
		if err := s.removeLocked(oldest); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) cleanupExpiredLocked() error {
	now := s.now().UTC()
	for id, metadata := range s.entries {
		if !now.Before(metadata.ExpiresAt) {
			if err := s.removeLocked(id); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) enforceQuotaLocked() error {
	for s.usedBytes > s.maxBytes {
		oldest := s.oldestIDLocked()
		if oldest == "" {
			return ErrQuotaExceeded
		}
		if err := s.removeLocked(oldest); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) oldestIDLocked() string {
	ids := make([]string, 0, len(s.entries))
	for id := range s.entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := s.entries[ids[i]]
		right := s.entries[ids[j]]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return ids[i] < ids[j]
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func (s *Store) removeLocked(id string) error {
	metadata, ok := s.entries[id]
	if err := s.removeFiles(id); err != nil {
		return err
	}
	if ok {
		delete(s.entries, id)
		s.usedBytes -= metadata.Size
	}
	return nil
}

func (s *Store) removeFiles(id string) error {
	var result error
	for _, path := range []string{s.contentPath(id), s.metadataPath(id)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && result == nil {
			result = fmt.Errorf("remove artifact file: %w", err)
		}
	}
	return result
}

func (s *Store) newIDLocked() (string, error) {
	for range 8 {
		bytes := make([]byte, idByteCount)
		if _, err := io.ReadFull(s.random, bytes); err != nil {
			return "", fmt.Errorf("generate artifact ID: %w", err)
		}
		id := base64.RawURLEncoding.EncodeToString(bytes)
		if _, exists := s.entries[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("generate unique artifact ID")
}

func (s *Store) contentPath(id string) string {
	return filepath.Join(s.directory, id+".bin")
}

func (s *Store) metadataPath(id string) string {
	return filepath.Join(s.directory, id+".json")
}

func validateID(id string) error {
	if len(id) != idLength {
		return ErrInvalidID
	}
	for _, character := range id {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return ErrInvalidID
	}
	return nil
}

func validMetadata(metadata Metadata, id string) bool {
	if metadata.ID != id || metadata.URI != URI(id) || metadata.Size < 0 ||
		metadata.MIMEType == "" || metadata.CreatedAt.IsZero() || metadata.ExpiresAt.IsZero() ||
		!metadata.ExpiresAt.After(metadata.CreatedAt) {
		return false
	}
	_, _, err := mime.ParseMediaType(metadata.MIMEType)
	return err == nil
}

func normalizeRedaction(redaction RedactionMetadata) RedactionMetadata {
	rules := make(map[string]struct{}, len(redaction.Rules))
	for _, rule := range redaction.Rules {
		if rule = strings.TrimSpace(rule); rule != "" {
			rules[rule] = struct{}{}
		}
	}
	redaction.Rules = make([]string, 0, len(rules))
	for rule := range rules {
		redaction.Rules = append(redaction.Rules, rule)
	}
	sort.Strings(redaction.Rules)
	redaction.Applied = redaction.Applied || len(redaction.Rules) > 0
	if redaction.Rules == nil {
		redaction.Rules = []string{}
	}
	return redaction
}

func cloneMetadata(metadata Metadata) Metadata {
	metadata.Redaction.Rules = append([]string(nil), metadata.Redaction.Rules...)
	if metadata.Redaction.Rules == nil {
		metadata.Redaction.Rules = []string{}
	}
	return metadata
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	return ctx.Err()
}

func writeAtomic(path string, data []byte) (returnErr error) {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".artifact-")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); closeErr != nil && returnErr == nil {
				returnErr = closeErr
			}
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}
