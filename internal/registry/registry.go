// Package registry tracks connected browser extension instances.
package registry

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hightemp/go_mcp_browser_ext_tool/internal/protocol"
)

// Connection is the minimal transport contract required by the registry and
// request router.
type Connection interface {
	ID() string
	Send(ctx context.Context, message protocol.Message) error
	Close() error
}

// Registration contains metadata supplied by an authenticated browser
// handshake.
type Registration struct {
	BrowserID        string
	DisplayName      string
	ExtensionVersion string
	Browser          protocol.BrowserMetadata
	Capabilities     []string
	Permissions      []string
	Incognito        bool
	RemoteAddress    string
}

// Browser is an immutable snapshot safe to return to callers.
type Browser struct {
	BrowserID        string                   `json:"browserId"`
	ConnectionID     string                   `json:"connectionId"`
	DisplayName      string                   `json:"displayName"`
	ExtensionVersion string                   `json:"extensionVersion"`
	Browser          protocol.BrowserMetadata `json:"browser"`
	Capabilities     []string                 `json:"capabilities"`
	Permissions      []string                 `json:"permissions"`
	Incognito        bool                     `json:"incognito"`
	RemoteAddress    string                   `json:"remoteAddress,omitempty"`
	FirstSeen        time.Time                `json:"firstSeen"`
	ConnectedAt      time.Time                `json:"connectedAt"`
	LastSeen         time.Time                `json:"lastSeen"`
	DisconnectedAt   *time.Time               `json:"disconnectedAt,omitempty"`
	DisconnectReason string                   `json:"disconnectReason,omitempty"`
	LatencyMS        *int64                   `json:"latencyMs,omitempty"`
	Connected        bool                     `json:"connected"`
}

type entry struct {
	browser    Browser
	connection Connection
}

// Registry is a concurrency-safe store of active connections and retained
// disconnected browser snapshots.
type Registry struct {
	mu       sync.RWMutex
	browsers map[string]*entry
	now      func() time.Time
}

// New creates an empty browser registry.
func New() *Registry {
	return &Registry{
		browsers: make(map[string]*entry),
		now:      time.Now,
	}
}

// Register adds or atomically replaces a browser connection. It returns the
// replaced connection, if one existed, so callers can close it outside the
// registry lock.
func (r *Registry) Register(registration Registration, connection Connection) (Connection, error) {
	if strings.TrimSpace(registration.BrowserID) == "" {
		return nil, protocol.NewError(protocol.CodeInvalidMessage, "browserId is required", false)
	}
	if connection == nil || connection.ID() == "" {
		return nil, protocol.NewError(protocol.CodeInvalidMessage, "connection is required", false)
	}

	now := r.now().UTC()
	displayName := strings.TrimSpace(registration.DisplayName)
	browser := Browser{
		BrowserID:        registration.BrowserID,
		ConnectionID:     connection.ID(),
		DisplayName:      displayName,
		ExtensionVersion: registration.ExtensionVersion,
		Browser:          registration.Browser,
		Capabilities:     normalizedStrings(registration.Capabilities),
		Permissions:      normalizedStrings(registration.Permissions),
		Incognito:        registration.Incognito,
		RemoteAddress:    registration.RemoteAddress,
		FirstSeen:        now,
		ConnectedAt:      now,
		LastSeen:         now,
		Connected:        true,
	}

	r.mu.Lock()
	oldEntry := r.browsers[registration.BrowserID]
	if oldEntry != nil {
		browser.FirstSeen = oldEntry.browser.FirstSeen
		if displayName == "" {
			browser.DisplayName = oldEntry.browser.DisplayName
		}
	}
	if browser.DisplayName == "" {
		browser.DisplayName = registration.BrowserID
	}
	r.browsers[registration.BrowserID] = &entry{
		browser:    browser,
		connection: connection,
	}
	r.mu.Unlock()

	if oldEntry == nil || oldEntry.connection == nil {
		return nil, nil
	}
	return oldEntry.connection, nil
}

// Unregister marks a browser disconnected only if connectionID still owns the
// entry. The retained snapshot remains available for diagnostics.
func (r *Registry) Unregister(browserID, connectionID string) bool {
	return r.Disconnect(browserID, connectionID, "connection closed")
}

// Disconnect records an offline transition for the current connection.
func (r *Registry) Disconnect(browserID, connectionID, reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok || !current.browser.Connected || current.browser.ConnectionID != connectionID {
		return false
	}
	now := r.now().UTC()
	current.browser.Connected = false
	current.browser.LastSeen = now
	current.browser.DisconnectedAt = &now
	current.browser.DisconnectReason = strings.TrimSpace(reason)
	current.connection = nil
	return true
}

// Touch updates the last-seen timestamp of the current connection.
func (r *Registry) Touch(browserID, connectionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok || !current.browser.Connected || current.browser.ConnectionID != connectionID {
		return false
	}
	current.browser.LastSeen = r.now().UTC()
	return true
}

// UpdateCapabilities replaces capabilities and permissions for the current
// browser connection.
func (r *Registry) UpdateCapabilities(
	browserID string,
	connectionID string,
	capabilities []string,
	permissions []string,
) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok || !current.browser.Connected || current.browser.ConnectionID != connectionID {
		return false
	}
	current.browser.Capabilities = normalizedStrings(capabilities)
	current.browser.Permissions = normalizedStrings(permissions)
	current.browser.LastSeen = r.now().UTC()
	return true
}

// RecordLatency updates the most recent round-trip latency for the current
// connection.
func (r *Registry) RecordLatency(browserID, connectionID string, latency time.Duration) bool {
	if latency < 0 {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok || !current.browser.Connected || current.browser.ConnectionID != connectionID {
		return false
	}
	milliseconds := latency.Milliseconds()
	current.browser.LatencyMS = &milliseconds
	current.browser.LastSeen = r.now().UTC()
	return true
}

// Rename changes the display name of a known browser snapshot.
func (r *Registry) Rename(browserID, displayName string) (Browser, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return Browser{}, protocol.NewError(protocol.CodeInvalidMessage, "displayName is required", false)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok {
		return Browser{}, protocol.NewError(protocol.CodeBrowserNotFound, "browser not found", false)
	}
	current.browser.DisplayName = displayName
	return cloneBrowser(current.browser), nil
}

// Get returns the retained snapshot for a known browser.
func (r *Registry) Get(browserID string) (Browser, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current, ok := r.browsers[browserID]
	if !ok {
		return Browser{}, false
	}
	return cloneBrowser(current.browser), true
}

// List returns connected browsers sorted by display name and browser ID.
func (r *Registry) List() []Browser {
	return r.list(false)
}

// ListAll returns connected and retained disconnected browser snapshots.
func (r *Registry) ListAll() []Browser {
	return r.list(true)
}

func (r *Registry) list(includeDisconnected bool) []Browser {
	r.mu.RLock()
	browsers := make([]Browser, 0, len(r.browsers))
	for _, current := range r.browsers {
		if !includeDisconnected && !current.browser.Connected {
			continue
		}
		browsers = append(browsers, cloneBrowser(current.browser))
	}
	r.mu.RUnlock()

	sort.Slice(browsers, func(i, j int) bool {
		if browsers[i].DisplayName == browsers[j].DisplayName {
			return browsers[i].BrowserID < browsers[j].BrowserID
		}
		return browsers[i].DisplayName < browsers[j].DisplayName
	})
	return browsers
}

// PruneDisconnected removes retained offline snapshots disconnected before
// cutoff. Active browsers are never removed.
func (r *Registry) PruneDisconnected(cutoff time.Time) int {
	cutoff = cutoff.UTC()
	removed := 0
	r.mu.Lock()
	for browserID, current := range r.browsers {
		if current.browser.Connected || current.browser.DisconnectedAt == nil {
			continue
		}
		if current.browser.DisconnectedAt.Before(cutoff) {
			delete(r.browsers, browserID)
			removed++
		}
	}
	r.mu.Unlock()
	return removed
}

// Connection returns the current connection for browserID.
func (r *Registry) Connection(browserID string) (Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current, ok := r.browsers[browserID]
	if !ok || !current.browser.Connected || current.connection == nil {
		return nil, false
	}
	return current.connection, true
}

// Count returns the number of connected browser instances.
func (r *Registry) Count() int {
	r.mu.RLock()
	count := 0
	for _, current := range r.browsers {
		if current.browser.Connected {
			count++
		}
	}
	r.mu.RUnlock()
	return count
}

func cloneBrowser(browser Browser) Browser {
	browser.Capabilities = append([]string(nil), browser.Capabilities...)
	browser.Permissions = append([]string(nil), browser.Permissions...)
	if browser.DisconnectedAt != nil {
		value := *browser.DisconnectedAt
		browser.DisconnectedAt = &value
	}
	if browser.LatencyMS != nil {
		value := *browser.LatencyMS
		browser.LatencyMS = &value
	}
	return browser
}

func normalizedStrings(values []string) []string {
	unique := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			unique[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
