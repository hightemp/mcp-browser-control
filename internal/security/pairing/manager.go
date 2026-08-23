// Package pairing manages one-time browser pairing codes and persistent
// browser credentials.
package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

const (
	defaultCodeTTL       = 10 * time.Minute
	defaultAttemptWindow = time.Minute
	defaultMaxAttempts   = 5
	credentialBytes      = 32
	credentialFileMode   = 0o600
	credentialDirMode    = 0o700
	storeVersion         = 1
)

// CodeObserver is called whenever a new pairing code becomes active.
type CodeObserver func(code string, expiresAt time.Time)

// Option configures a Manager.
type Option func(*Manager)

// WithStorePath configures the credential store. An empty path keeps
// credentials in memory only.
func WithStorePath(path string) Option {
	return func(manager *Manager) {
		manager.storePath = strings.TrimSpace(path)
	}
}

// WithCodeTTL configures the lifetime of each one-time pairing code.
func WithCodeTTL(ttl time.Duration) Option {
	return func(manager *Manager) {
		if ttl > 0 {
			manager.codeTTL = ttl
		}
	}
}

// WithAttemptLimit configures the global invalid-code attempt limit.
func WithAttemptLimit(maxAttempts int, window time.Duration) Option {
	return func(manager *Manager) {
		if maxAttempts > 0 {
			manager.maxAttempts = maxAttempts
		}
		if window > 0 {
			manager.attemptWindow = window
		}
	}
}

// WithCodeObserver configures a callback for displaying new pairing codes.
func WithCodeObserver(observer CodeObserver) Option {
	return func(manager *Manager) {
		manager.codeObserver = observer
	}
}

// WithRandom configures the cryptographically secure random source. It is
// intended primarily for tests.
func WithRandom(random io.Reader) Option {
	return func(manager *Manager) {
		if random != nil {
			manager.random = random
		}
	}
}

// WithClock configures the clock used for expiry and rate-limit decisions.
// It is intended primarily for tests.
func WithClock(now func() time.Time) Option {
	return func(manager *Manager) {
		if now != nil {
			manager.now = now
		}
	}
}

type credentialRecord struct {
	Hash      [sha256.Size]byte
	CreatedAt time.Time
}

type attemptState struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

// Manager authenticates browser instances and owns the active one-time code.
type Manager struct {
	mu            sync.RWMutex
	random        io.Reader
	now           func() time.Time
	storePath     string
	codeTTL       time.Duration
	maxAttempts   int
	attemptWindow time.Duration
	codeObserver  CodeObserver

	code        string
	codeHash    [sha256.Size]byte
	codeExpires time.Time
	attempts    attemptState
	credentials map[string]credentialRecord
}

type diskStore struct {
	Version     int                       `json:"version"`
	Credentials map[string]diskCredential `json:"credentials"`
}

type diskCredential struct {
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewManager loads persisted credential hashes and generates an initial
// one-time pairing code.
func NewManager(options ...Option) (*Manager, error) {
	manager := &Manager{
		random:        rand.Reader,
		now:           time.Now,
		codeTTL:       defaultCodeTTL,
		maxAttempts:   defaultMaxAttempts,
		attemptWindow: defaultAttemptWindow,
		credentials:   make(map[string]credentialRecord),
	}
	for _, option := range options {
		option(manager)
	}
	if err := manager.load(); err != nil {
		return nil, err
	}
	if err := manager.rotateCodeLocked(manager.now().UTC()); err != nil {
		return nil, fmt.Errorf("generate initial pairing code: %w", err)
	}
	manager.notifyCode(manager.code, manager.codeExpires)
	return manager, nil
}

// CurrentCode returns the active one-time pairing code and its expiry time.
func (m *Manager) CurrentCode() (string, time.Time, error) {
	m.mu.Lock()
	now := m.now().UTC()
	rotated := false
	if !now.Before(m.codeExpires) {
		if err := m.rotateCodeLocked(now); err != nil {
			m.mu.Unlock()
			return "", time.Time{}, fmt.Errorf("rotate expired pairing code: %w", err)
		}
		rotated = true
	}
	code, expiresAt := m.code, m.codeExpires
	m.mu.Unlock()
	if rotated {
		m.notifyCode(code, expiresAt)
	}
	return code, expiresAt, nil
}

// Authorize authenticates an existing credential or consumes a pairing code
// and returns a newly issued credential.
func (m *Manager) Authorize(browserID, credential, pairingCode string) (string, error) {
	if strings.TrimSpace(browserID) == "" {
		return "", protocol.NewError(protocol.CodeInvalidMessage, "browserId is required", false)
	}
	if strings.TrimSpace(credential) != "" {
		return "", m.Authenticate(browserID, credential)
	}
	if strings.TrimSpace(pairingCode) != "" {
		return m.Pair(browserID, pairingCode)
	}
	return "", pairingRequired("browser pairing is required", false, nil)
}

// Authenticate verifies a browser credential using a constant-time hash
// comparison.
func (m *Manager) Authenticate(browserID, credential string) error {
	candidate := sha256.Sum256([]byte(credential))
	m.mu.RLock()
	record, ok := m.credentials[browserID]
	m.mu.RUnlock()
	if !ok || subtle.ConstantTimeCompare(candidate[:], record.Hash[:]) != 1 {
		return pairingRequired("the browser credential is invalid or revoked", false, nil)
	}
	return nil
}

// Pair consumes the current one-time code and returns a new browser
// credential. A successful call rotates the code to prevent replay.
func (m *Manager) Pair(browserID, code string) (string, error) {
	browserID = strings.TrimSpace(browserID)
	if browserID == "" {
		return "", protocol.NewError(protocol.CodeInvalidMessage, "browserId is required", false)
	}
	normalizedCode := normalizeCode(code)
	now := m.now().UTC()

	m.mu.Lock()
	rotated, err := m.rotateExpiredCodeLocked(now)
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("rotate expired pairing code: %w", err)
	}
	codeForNotification, expiryForNotification := m.code, m.codeExpires

	if retryAfter := m.retryAfterLocked(now); retryAfter > 0 {
		m.mu.Unlock()
		if rotated {
			m.notifyCode(codeForNotification, expiryForNotification)
		}
		return "", rateLimitError(retryAfter)
	}

	candidate := sha256.Sum256([]byte(normalizedCode))
	if normalizedCode == "" || subtle.ConstantTimeCompare(candidate[:], m.codeHash[:]) != 1 {
		retryAfter := m.recordInvalidAttemptLocked(now)
		m.mu.Unlock()
		if rotated {
			m.notifyCode(codeForNotification, expiryForNotification)
		}
		if retryAfter > 0 {
			return "", rateLimitError(retryAfter)
		}
		return "", pairingRequired("the pairing code is invalid or expired", false, nil)
	}

	credential, err := m.generateCredentialLocked()
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("generate browser credential: %w", err)
	}
	nextCode, nextCodeHash, nextExpiry, err := m.generateCodeLocked(now)
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("generate next pairing code: %w", err)
	}
	nextCredentials := cloneCredentials(m.credentials)
	nextCredentials[browserID] = credentialRecord{
		Hash:      sha256.Sum256([]byte(credential)),
		CreatedAt: now,
	}
	if err := m.save(nextCredentials); err != nil {
		m.mu.Unlock()
		return "", err
	}
	m.credentials = nextCredentials
	m.code = nextCode
	m.codeHash = nextCodeHash
	m.codeExpires = nextExpiry
	m.attempts = attemptState{}
	m.mu.Unlock()

	m.notifyCode(nextCode, nextExpiry)
	return credential, nil
}

// Revoke removes a browser credential from the persistent store.
func (m *Manager) Revoke(browserID string) (bool, error) {
	m.mu.Lock()
	if _, ok := m.credentials[browserID]; !ok {
		m.mu.Unlock()
		return false, nil
	}
	nextCredentials := cloneCredentials(m.credentials)
	delete(nextCredentials, browserID)
	if err := m.save(nextCredentials); err != nil {
		m.mu.Unlock()
		return false, err
	}
	m.credentials = nextCredentials
	m.mu.Unlock()
	return true, nil
}

func (m *Manager) rotateExpiredCodeLocked(now time.Time) (bool, error) {
	if now.Before(m.codeExpires) {
		return false, nil
	}
	if err := m.rotateCodeLocked(now); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) rotateCodeLocked(now time.Time) error {
	code, codeHash, expiresAt, err := m.generateCodeLocked(now)
	if err != nil {
		return err
	}
	m.code = code
	m.codeHash = codeHash
	m.codeExpires = expiresAt
	return nil
}

func (m *Manager) generateCodeLocked(now time.Time) (string, [sha256.Size]byte, time.Time, error) {
	value, err := rand.Int(m.random, big.NewInt(100_000_000))
	if err != nil {
		return "", [sha256.Size]byte{}, time.Time{}, err
	}
	digits := fmt.Sprintf("%08d", value.Int64())
	return digits[:4] + "-" + digits[4:], sha256.Sum256([]byte(digits)), now.Add(m.codeTTL), nil
}

func (m *Manager) generateCredentialLocked() (string, error) {
	value := make([]byte, credentialBytes)
	if _, err := io.ReadFull(m.random, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (m *Manager) retryAfterLocked(now time.Time) time.Duration {
	if now.Before(m.attempts.blockedUntil) {
		return m.attempts.blockedUntil.Sub(now)
	}
	if !m.attempts.blockedUntil.IsZero() {
		m.attempts = attemptState{}
	}
	return 0
}

func (m *Manager) recordInvalidAttemptLocked(now time.Time) time.Duration {
	if m.attempts.windowStart.IsZero() || now.Sub(m.attempts.windowStart) >= m.attemptWindow {
		m.attempts = attemptState{windowStart: now}
	}
	m.attempts.count++
	if m.attempts.count < m.maxAttempts {
		return 0
	}
	m.attempts.blockedUntil = now.Add(m.attemptWindow)
	return m.attemptWindow
}

func (m *Manager) notifyCode(code string, expiresAt time.Time) {
	if m.codeObserver != nil {
		m.codeObserver(code, expiresAt)
	}
}

func (m *Manager) load() error {
	if m.storePath == "" {
		return nil
	}
	payload, err := os.ReadFile(m.storePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pairing credential store: %w", err)
	}
	var stored diskStore
	if err := json.Unmarshal(payload, &stored); err != nil {
		return fmt.Errorf("decode pairing credential store: %w", err)
	}
	if stored.Version != storeVersion {
		return fmt.Errorf("unsupported pairing credential store version %d", stored.Version)
	}
	for browserID, diskRecord := range stored.Credentials {
		hash, err := base64.RawURLEncoding.DecodeString(diskRecord.Hash)
		if err != nil || len(hash) != sha256.Size {
			return fmt.Errorf("invalid credential hash for browser %q", browserID)
		}
		var fixedHash [sha256.Size]byte
		copy(fixedHash[:], hash)
		m.credentials[browserID] = credentialRecord{Hash: fixedHash, CreatedAt: diskRecord.CreatedAt}
	}
	return nil
}

func (m *Manager) save(credentials map[string]credentialRecord) error {
	if m.storePath == "" {
		return nil
	}
	stored := diskStore{
		Version:     storeVersion,
		Credentials: make(map[string]diskCredential, len(credentials)),
	}
	for browserID, record := range credentials {
		stored.Credentials[browserID] = diskCredential{
			Hash:      base64.RawURLEncoding.EncodeToString(record.Hash[:]),
			CreatedAt: record.CreatedAt,
		}
	}
	payload, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pairing credential store: %w", err)
	}
	payload = append(payload, '\n')
	if err := atomicWriteFile(m.storePath, payload); err != nil {
		return fmt.Errorf("write pairing credential store: %w", err)
	}
	return nil
}

func atomicWriteFile(path string, payload []byte) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, credentialDirMode); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".credentials-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			if err := temporary.Close(); err != nil && returnErr == nil {
				returnErr = fmt.Errorf("close temporary credential file: %w", err)
			}
		}
		if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) && returnErr == nil {
			returnErr = fmt.Errorf("remove temporary credential file: %w", err)
		}
	}()

	if err := temporary.Chmod(credentialFileMode); err != nil {
		return fmt.Errorf("secure temporary credential file: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write temporary credential file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary credential file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary credential file: %w", err)
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace credential file: %w", err)
	}
	return nil
}

func normalizeCode(code string) string {
	return strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code))
}

func cloneCredentials(source map[string]credentialRecord) map[string]credentialRecord {
	cloned := make(map[string]credentialRecord, len(source))
	for browserID, record := range source {
		cloned[browserID] = record
	}
	return cloned
}

func pairingRequired(message string, retryable bool, details map[string]any) *protocol.Error {
	errorValue := protocol.NewError(protocol.CodePairingRequired, message, retryable)
	errorValue.Details = details
	return errorValue
}

func rateLimitError(retryAfter time.Duration) *protocol.Error {
	return pairingRequired(
		"too many invalid pairing attempts; try again later",
		true,
		map[string]any{"retryAfterMs": retryAfter.Milliseconds()},
	)
}
