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
