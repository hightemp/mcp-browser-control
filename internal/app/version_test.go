package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestVersionRequested(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "short", args: []string{"-version"}, want: true},
		{name: "long", args: []string{"--version"}, want: true},
		{name: "unrelated", args: []string{"-t", "stdio"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := versionRequested(test.args); got != test.want {
				t.Fatalf("versionRequested(%v) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestRunPrintsVersionWithoutStartingServer(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := Run(
		context.Background(),
		[]string{"--version"},
		strings.NewReader(""),
		&output,
		&bytes.Buffer{},
	)
	if err != nil {
		t.Fatalf("Run(--version) error = %v", err)
	}
	for _, expected := range []string{
		"mcp-browser-control " + Version,
		"commit: " + Commit,
		"built: " + BuildDate,
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("version output %q does not contain %q", output.String(), expected)
		}
	}
}
