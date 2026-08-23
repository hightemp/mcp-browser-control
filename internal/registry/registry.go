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
	ConnectedAt      time.Time                `json:"connectedAt"`
	LastSeen         time.Time                `json:"lastSeen"`
	Connected        bool                     `json:"connected"`
}

type entry struct {
	browser    Browser
	connection Connection
}

// Registry is a concurrency-safe store of active browser connections.
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
	if displayName == "" {
		displayName = registration.BrowserID
	}

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
		ConnectedAt:      now,
		LastSeen:         now,
		Connected:        true,
	}

	r.mu.Lock()
	oldEntry := r.browsers[registration.BrowserID]
	r.browsers[registration.BrowserID] = &entry{
		browser:    browser,
		connection: connection,
	}
	r.mu.Unlock()

	if oldEntry == nil {
		return nil, nil
	}
	return oldEntry.connection, nil
}

// Unregister removes a browser only if connectionID still owns the entry.
func (r *Registry) Unregister(browserID, connectionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok || current.browser.ConnectionID != connectionID {
		return false
	}
	delete(r.browsers, browserID)
	return true
}

// Touch updates the last-seen timestamp of the current connection.
func (r *Registry) Touch(browserID, connectionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.browsers[browserID]
	if !ok || current.browser.ConnectionID != connectionID {
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
	if !ok || current.browser.ConnectionID != connectionID {
		return false
	}
	current.browser.Capabilities = normalizedStrings(capabilities)
	current.browser.Permissions = normalizedStrings(permissions)
	current.browser.LastSeen = r.now().UTC()
	return true
}

// Rename changes the display name of a connected browser.
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

// Get returns a snapshot for a connected browser.
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
	r.mu.RLock()
	browsers := make([]Browser, 0, len(r.browsers))
	for _, current := range r.browsers {
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

// Connection returns the current connection for browserID.
func (r *Registry) Connection(browserID string) (Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	current, ok := r.browsers[browserID]
	if !ok {
		return nil, false
	}
	return current.connection, true
}

// Count returns the number of connected browser instances.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.browsers)
}

func cloneBrowser(browser Browser) Browser {
	browser.Capabilities = append([]string(nil), browser.Capabilities...)
	browser.Permissions = append([]string(nil), browser.Permissions...)
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
