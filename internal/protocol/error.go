// Package protocol defines the versioned wire contract shared by the server
// and browser extension.
package protocol

import (
	"context"
	"errors"
	"fmt"
)

// ErrorCode is a stable, machine-readable failure code.
type ErrorCode string

const (
	CodeNoBrowserConnected         ErrorCode = "NO_BROWSER_CONNECTED"
	CodeAmbiguousBrowser           ErrorCode = "AMBIGUOUS_BROWSER"
	CodeBrowserNotFound            ErrorCode = "BROWSER_NOT_FOUND"
	CodeBrowserDisconnected        ErrorCode = "BROWSER_DISCONNECTED"
	CodeTabNotFound                ErrorCode = "TAB_NOT_FOUND"
	CodeFrameNotFound              ErrorCode = "FRAME_NOT_FOUND"
	CodeStaleTarget                ErrorCode = "STALE_TARGET"
	CodeElementNotFound            ErrorCode = "ELEMENT_NOT_FOUND"
	CodeStrictModeViolation        ErrorCode = "STRICT_MODE_VIOLATION"
	CodePermissionRequired         ErrorCode = "PERMISSION_REQUIRED"
	CodeCapabilityUnavailable      ErrorCode = "CAPABILITY_UNAVAILABLE"
	CodePairingRequired            ErrorCode = "PAIRING_REQUIRED"
	CodeUnsupportedProtocolVersion ErrorCode = "UNSUPPORTED_PROTOCOL_VERSION"
	CodeInvalidMessage             ErrorCode = "INVALID_MESSAGE"
	CodeInvalidCommand             ErrorCode = "INVALID_COMMAND"
	CodeTimeout                    ErrorCode = "TIMEOUT"
	CodeCancelled                  ErrorCode = "CANCELLED"
	CodePayloadTooLarge            ErrorCode = "PAYLOAD_TOO_LARGE"
	CodeRestrictedURL              ErrorCode = "RESTRICTED_URL"
	CodeConfirmationRequired       ErrorCode = "CONFIRMATION_REQUIRED"
	CodeInternal                   ErrorCode = "INTERNAL_ERROR"
)

// Error is the structured error transported between the server, extension,
// and MCP tools.
type Error struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	RequestID string         `json:"requestId,omitempty"`
	Target    *Target        `json:"target,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewError creates a structured protocol error.
func NewError(code ErrorCode, message string, retryable bool) *Error {
	return &Error{
		Code:      code,
		Message:   message,
		Retryable: retryable,
	}
}

// WithContext returns a copy enriched with request and target diagnostics.
func (e *Error) WithContext(requestID string, target *Target) *Error {
	if e == nil {
		return nil
	}
	copyErr := *e
	if copyErr.RequestID == "" {
		copyErr.RequestID = requestID
	}
	if copyErr.Target == nil {
		copyErr.Target = cloneTarget(target)
	} else {
		copyErr.Target = cloneTarget(copyErr.Target)
	}
	return &copyErr
}

// ErrorFrom converts an arbitrary error into a safe protocol error.
func ErrorFrom(err error) *Error {
	if err == nil {
		return nil
	}

	var protocolErr *Error
	if errors.As(err, &protocolErr) {
		copyErr := *protocolErr
		copyErr.Target = cloneTarget(protocolErr.Target)
		return &copyErr
	}

	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return NewError(CodeTimeout, "the browser command timed out", true)
	case errors.Is(err, context.Canceled):
		return NewError(CodeCancelled, "the browser command was cancelled", false)
	default:
		return NewError(CodeInternal, "an internal error occurred", false)
	}
}

func cloneTarget(target *Target) *Target {
	if target == nil {
		return nil
	}
	cloned := *target
	if target.WindowID != nil {
		value := *target.WindowID
		cloned.WindowID = &value
	}
	if target.TabID != nil {
		value := *target.TabID
		cloned.TabID = &value
	}
	if target.FrameID != nil {
		value := *target.FrameID
		cloned.FrameID = &value
	}
	return &cloned
}
