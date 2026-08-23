package netguard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		host       string
		origin     string
		wantStatus int
	}{
		{name: "IPv4 without origin", host: "127.0.0.1:8896", wantStatus: http.StatusNoContent},
		{name: "localhost origin", host: "localhost:8896", origin: "http://localhost:3000", wantStatus: http.StatusNoContent},
		{name: "IPv6", host: "[::1]:8896", origin: "http://[::1]:3000", wantStatus: http.StatusNoContent},
		{name: "bad host", host: "attacker.example", wantStatus: http.StatusForbidden},
		{name: "bad origin", host: "127.0.0.1:8896", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
	}

	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := LocalOnly(next)

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8896/mcp", nil)
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestLocalOnlyWithOrigins(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := LocalOnlyWithOrigins(next, []string{"http://localhost:3000"})
	tests := []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "native client", wantStatus: http.StatusNoContent},
		{name: "allowlisted", origin: "http://localhost:3000", wantStatus: http.StatusNoContent},
		{name: "other loopback", origin: "http://127.0.0.1:3000", wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8896/mcp", nil)
			request.Header.Set("Origin", test.origin)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	handler := BearerToken(next, "correct-secret-token")
	tests := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{name: "valid", authorization: "Bearer correct-secret-token", wantStatus: http.StatusNoContent},
		{name: "case insensitive scheme", authorization: "bearer correct-secret-token", wantStatus: http.StatusNoContent},
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong", authorization: "Bearer wrong-token", wantStatus: http.StatusUnauthorized},
		{name: "wrong scheme", authorization: "Basic correct-secret-token", wantStatus: http.StatusUnauthorized},
		{name: "extra field", authorization: "Bearer correct-secret-token extra", wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8896/mcp", nil)
			request.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if test.wantStatus == http.StatusUnauthorized &&
				response.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Error("unauthorized response is missing Bearer challenge")
			}
		})
	}
}
