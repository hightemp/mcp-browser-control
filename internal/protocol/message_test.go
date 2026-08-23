package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMessageValidate(t *testing.T) {
	success := true
	tests := []struct {
		name    string
		message Message
		wantErr ErrorCode
	}{
		{
			name: "valid request",
			message: Message{
				ProtocolVersion: Version,
				Type:            TypeRequest,
				RequestID:       "request-1",
				BrowserID:       "browser-1",
				Command:         "tabs.list",
			},
		},
		{
			name: "valid response",
			message: Message{
				ProtocolVersion: Version,
				Type:            TypeResponse,
				RequestID:       "request-1",
				BrowserID:       "browser-1",
				Success:         &success,
			},
		},
		{
			name: "wrong version",
			message: Message{
				ProtocolVersion: "2.0",
				Type:            TypePing,
				BrowserID:       "browser-1",
			},
			wantErr: CodeUnsupportedProtocolVersion,
		},
		{
			name: "request without command",
			message: Message{
				ProtocolVersion: Version,
				Type:            TypeRequest,
				RequestID:       "request-1",
				BrowserID:       "browser-1",
			},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "hello without browser",
			message: Message{ProtocolVersion: Version, Type: TypeHello, Params: json.RawMessage(`{}`)},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "hello without params",
			message: Message{ProtocolVersion: Version, Type: TypeHello, BrowserID: "browser-1"},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "welcome without connection",
			message: Message{ProtocolVersion: Version, Type: TypeWelcome, BrowserID: "browser-1"},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "failed response without error",
			message: Message{ProtocolVersion: Version, Type: TypeResponse, BrowserID: "browser-1", RequestID: "request-1", Success: boolPointer(false)},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "cancel without request",
			message: Message{ProtocolVersion: Version, Type: TypeCancel, BrowserID: "browser-1"},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "ping without browser",
			message: Message{ProtocolVersion: Version, Type: TypePing},
			wantErr: CodeInvalidMessage,
		},
		{
			name:    "unknown type",
			message: Message{ProtocolVersion: Version, Type: MessageType("unknown")},
			wantErr: CodeInvalidMessage,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.message.Validate()
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}

			var protocolErr *Error
			if !errors.As(err, &protocolErr) {
				t.Fatalf("Validate() error type = %T, want *Error", err)
			}
			if protocolErr.Code != test.wantErr {
				t.Errorf("Validate() code = %q, want %q", protocolErr.Code, test.wantErr)
			}
		})
	}
}

func TestNewRequest(t *testing.T) {
	tabID := 42
	message, err := NewRequest(
		"request-1",
		"browser-1",
		"tabs.get",
		&Target{TabID: &tabID},
		map[string]any{"includeURL": true},
		1500*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	if message.TimeoutMS != 1500 {
		t.Errorf("TimeoutMS = %d, want 1500", message.TimeoutMS)
	}
	if message.Target == nil || message.Target.TabID == nil || *message.Target.TabID != tabID {
		t.Fatalf("Target.TabID = %#v, want %d", message.Target, tabID)
	}

	var params map[string]bool
	if err := json.Unmarshal(message.Params, &params); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}
	if !params["includeURL"] {
		t.Error("includeURL = false, want true")
	}
}

func TestErrorFrom(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code ErrorCode
	}{
		{name: "protocol", err: NewError(CodeBrowserNotFound, "missing", false), code: CodeBrowserNotFound},
		{name: "deadline", err: context.DeadlineExceeded, code: CodeTimeout},
		{name: "generic", err: errors.New("secret internal detail"), code: CodeInternal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ErrorFrom(test.err); got.Code != test.code {
				t.Errorf("ErrorFrom() code = %q, want %q", got.Code, test.code)
			}
		})
	}
}

func TestDecodeParamsAndErrorString(t *testing.T) {
	t.Parallel()

	var payload map[string]string
	message := Message{Params: json.RawMessage(`{"name":"Chrome"}`)}
	if err := message.DecodeParams(&payload); err != nil {
		t.Fatalf("DecodeParams() error = %v", err)
	}
	if payload["name"] != "Chrome" {
		t.Errorf("name = %q, want Chrome", payload["name"])
	}
	for _, params := range []json.RawMessage{nil, json.RawMessage(`{`)} {
		if err := (Message{Params: params}).DecodeParams(&payload); err == nil {
			t.Fatalf("DecodeParams(%q) error = nil", params)
		}
	}
	protocolErr := NewError(CodeInvalidMessage, "bad request", false)
	if got := protocolErr.Error(); got != "INVALID_MESSAGE: bad request" {
		t.Errorf("Error() = %q", got)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
