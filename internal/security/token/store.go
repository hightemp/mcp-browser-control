// Package token manages the local MCP HTTP bearer token.
package token

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	tokenBytes = 32
	dirMode    = 0o700
	fileMode   = 0o600
)

// LoadOrCreate loads a valid owner-only token or creates one with secure
// permissions. The raw token is suitable for an HTTP Bearer credential.
func LoadOrCreate(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("MCP token file path is required")
	}
	value, err := load(path)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	value, err = generate(rand.Reader)
	if err != nil {
		return "", fmt.Errorf("generate MCP bearer token: %w", err)
	}
	if err := create(path, value); err != nil {
		if errors.Is(err, os.ErrExist) {
			return load(path)
		}
		return "", err
	}
	return value, nil
}

func load(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect MCP token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("MCP token file must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("MCP token file permissions must be %04o", fileMode)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read MCP token file: %w", err)
	}
	value := strings.TrimSpace(string(payload))
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != tokenBytes {
		return "", errors.New("MCP token file contains an invalid token")
	}
	return value, nil
}

func generate(random io.Reader) (string, error) {
	payload := make([]byte, tokenBytes)
	if _, err := io.ReadFull(random, payload); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func create(path, value string) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, dirMode); err != nil {
		return fmt.Errorf("create MCP token directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return fmt.Errorf("create MCP token file: %w", err)
	}
	remove := true
	closed := false
	defer func() {
		if !closed {
			if closeErr := file.Close(); closeErr != nil && returnErr == nil {
				returnErr = fmt.Errorf("close MCP token file: %w", closeErr)
			}
		}
		if remove {
			if removeErr := os.Remove(path); removeErr != nil &&
				!errors.Is(removeErr, os.ErrNotExist) && returnErr == nil {
				returnErr = fmt.Errorf("remove incomplete MCP token file: %w", removeErr)
			}
		}
	}()
	if err := file.Chmod(fileMode); err != nil {
		return fmt.Errorf("secure MCP token file: %w", err)
	}
	if _, err := io.WriteString(file, value+"\n"); err != nil {
		return fmt.Errorf("write MCP token file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync MCP token file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close MCP token file: %w", err)
	}
	closed = true
	remove = false
	return nil
}
