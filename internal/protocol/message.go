package protocol

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	// Version is the current server-to-extension protocol version.
	Version = "1.0"
)

// MessageType identifies an envelope payload.
type MessageType string

const (
	TypeHello               MessageType = "hello"
	TypeWelcome             MessageType = "welcome"
	TypeAuthError           MessageType = "auth_error"
	TypeRevoke              MessageType = "revoke"
	TypeRequest             MessageType = "request"
	TypeResponse            MessageType = "response"
	TypeCancel              MessageType = "cancel"
	TypeEvent               MessageType = "event"
	TypePing                MessageType = "ping"
	TypePong                MessageType = "pong"
	TypeCapabilitiesChanged MessageType = "capabilities_changed"
)

// Target identifies a browser object. All IDs are scoped to BrowserID in the
// enclosing Message.
type Target struct {
	WindowID   *int   `json:"windowId,omitempty"`
	TabID      *int   `json:"tabId,omitempty"`
	FrameID    *int   `json:"frameId,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
}

// Message is the common protocol v1 envelope.
type Message struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Type            MessageType     `json:"type"`
	RequestID       string          `json:"requestId,omitempty"`
	BrowserID       string          `json:"browserId,omitempty"`
	ConnectionID    string          `json:"connectionId,omitempty"`
	Command         string          `json:"command,omitempty"`
	Target          *Target         `json:"target,omitempty"`
	Params          json.RawMessage `json:"params,omitempty"`
	TimeoutMS       int64           `json:"timeoutMs,omitempty"`
	Success         *bool           `json:"success,omitempty"`
	Result          json.RawMessage `json:"result,omitempty"`
	Error           *Error          `json:"error,omitempty"`
	Timestamp       string          `json:"timestamp,omitempty"`
}

// BrowserMetadata describes the connected browser.
type BrowserMetadata struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	OS      string `json:"os,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

// HelloParams contains browser registration metadata.
type HelloParams struct {
	DisplayName      string          `json:"displayName,omitempty"`
	ExtensionVersion string          `json:"extensionVersion"`
	Credential       string          `json:"credential,omitempty"`
	PairingCode      string          `json:"pairingCode,omitempty"`
	Browser          BrowserMetadata `json:"browser"`
	Capabilities     []string        `json:"capabilities,omitempty"`
	Permissions      []string        `json:"permissions,omitempty"`
	Incognito        bool            `json:"incognito,omitempty"`
}

// WelcomeResult acknowledges a successful browser registration.
type WelcomeResult struct {
	BrowserID    string `json:"browserId"`
	ConnectionID string `json:"connectionId"`
	ServerTime   string `json:"serverTime"`
	Credential   string `json:"credential,omitempty"`
	Paired       bool   `json:"paired"`
}

// CapabilitiesChangedParams reports runtime permission and capability changes.
type CapabilitiesChangedParams struct {
	Capabilities []string `json:"capabilities,omitempty"`
	Permissions  []string `json:"permissions,omitempty"`
}

// NewMessage creates an envelope with the required common fields.
func NewMessage(messageType MessageType) Message {
	return Message{
		ProtocolVersion: Version,
		Type:            messageType,
		Timestamp:       time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// NewRequest creates a validated browser request message.
func NewRequest(requestID, browserID, command string, target *Target, params any, timeout time.Duration) (Message, error) {
	rawParams, err := marshalPayload(params)
	if err != nil {
		return Message{}, fmt.Errorf("marshal request params: %w", err)
	}

	message := NewMessage(TypeRequest)
	message.RequestID = requestID
	message.BrowserID = browserID
	message.Command = command
	message.Target = target
	message.Params = rawParams
	if timeout > 0 {
		message.TimeoutMS = timeout.Milliseconds()
	}

	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// NewResponse creates a response message.
func NewResponse(requestID, browserID string, result any, responseErr *Error) (Message, error) {
	message := NewMessage(TypeResponse)
	message.RequestID = requestID
	message.BrowserID = browserID
	success := responseErr == nil
	message.Success = &success
	message.Error = responseErr

	if result != nil {
		rawResult, err := json.Marshal(result)
		if err != nil {
			return Message{}, fmt.Errorf("marshal response result: %w", err)
		}
		message.Result = rawResult
	}

	if err := message.Validate(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// NewCancel creates a cancellation message for an in-flight request.
func NewCancel(requestID, browserID string) Message {
	message := NewMessage(TypeCancel)
	message.RequestID = requestID
	message.BrowserID = browserID
	return message
}

// DecodeParams decodes the params payload into target.
func (m Message) DecodeParams(target any) error {
	if len(m.Params) == 0 {
		return NewError(CodeInvalidMessage, "message params are required", false)
	}
	if err := json.Unmarshal(m.Params, target); err != nil {
		return NewError(CodeInvalidMessage, "message params are invalid", false)
	}
	return nil
}

// Validate validates required envelope fields.
func (m Message) Validate() error {
	if m.ProtocolVersion != Version {
		return &Error{
			Code:    CodeUnsupportedProtocolVersion,
			Message: fmt.Sprintf("unsupported protocol version %q; expected %q", m.ProtocolVersion, Version),
			Details: map[string]any{"expectedVersion": Version},
		}
	}

	switch m.Type {
	case TypeHello:
		if m.BrowserID == "" {
			return NewError(CodeInvalidMessage, "hello.browserId is required", false)
		}
		if len(m.Params) == 0 {
			return NewError(CodeInvalidMessage, "hello.params is required", false)
		}
	case TypeWelcome:
		if m.BrowserID == "" || m.ConnectionID == "" {
			return NewError(CodeInvalidMessage, "welcome browserId and connectionId are required", false)
		}
	case TypeAuthError:
		if m.BrowserID == "" || m.Error == nil {
			return NewError(CodeInvalidMessage, "auth_error browserId and error are required", false)
		}
	case TypeRequest:
		if m.RequestID == "" || m.BrowserID == "" || m.Command == "" {
			return NewError(CodeInvalidMessage, "requestId, browserId, and command are required", false)
		}
	case TypeResponse:
		if m.RequestID == "" || m.BrowserID == "" || m.Success == nil {
			return NewError(CodeInvalidMessage, "response requestId, browserId, and success are required", false)
		}
		if !*m.Success && m.Error == nil {
			return NewError(CodeInvalidMessage, "failed response must include an error", false)
		}
	case TypeCancel:
		if m.RequestID == "" || m.BrowserID == "" {
			return NewError(CodeInvalidMessage, "cancel requestId and browserId are required", false)
		}
	case TypeEvent, TypePing, TypePong, TypeCapabilitiesChanged:
		if m.BrowserID == "" {
			return NewError(CodeInvalidMessage, "browserId is required", false)
		}
	case TypeRevoke:
		if m.BrowserID == "" {
			return NewError(CodeInvalidMessage, "browserId is required", false)
		}
		if m.Success != nil && !*m.Success && m.Error == nil {
			return NewError(CodeInvalidMessage, "failed revoke acknowledgement must include an error", false)
		}
	default:
		return NewError(CodeInvalidMessage, fmt.Sprintf("unknown message type %q", m.Type), false)
	}

	return nil
}

func marshalPayload(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("{}"), nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
