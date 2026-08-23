package token

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOrCreatePersistsOwnerOnlyToken(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "nested", "mcp-token")
	first, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate() error = %v", err)
	}
	second, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second LoadOrCreate() error = %v", err)
	}
	if first == "" || first != second {
		t.Fatalf("tokens = %q and %q", first, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != fileMode {
		t.Errorf("token file mode = %04o, want %04o", got, fileMode)
	}
}

func TestLoadOrCreateRejectsUnsafeStores(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(t *testing.T, path string)
		want    string
	}{
		{name: "empty path", want: "path is required"},
		{
			name: "invalid token",
			prepare: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("not-a-token\n"), fileMode); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			},
			want: "invalid token",
		},
		{
			name: "unsafe permissions",
			prepare: func(t *testing.T, path string) {
				t.Helper()
				value, err := generate(strings.NewReader(strings.Repeat("x", tokenBytes)))
				if err != nil {
					t.Fatalf("generate() error = %v", err)
				}
				if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			},
			want: "permissions",
		},
		{
			name: "directory",
			prepare: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Mkdir(path, dirMode); err != nil {
					t.Fatalf("Mkdir() error = %v", err)
				}
			},
			want: "regular file",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := ""
			if test.prepare != nil {
				path = filepath.Join(t.TempDir(), "mcp-token")
				test.prepare(t, path)
			}
			if _, err := LoadOrCreate(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadOrCreate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestGenerateRequiresEnoughEntropy(t *testing.T) {
	t.Parallel()
	if _, err := generate(strings.NewReader("short")); err == nil {
		t.Fatal("generate() error = nil")
	}
}
