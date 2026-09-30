package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	toml "github.com/pelletier/go-toml/v2"
)

// trustedFile holds the map of trusted project configuration paths to their SHA256 digests.
type trustedFile struct {
	Trusted map[string]string `toml:"trusted"`
}

var trustMu sync.Mutex

// FileSHA256 returns the hex-encoded SHA256 checksum of data.
func FileSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TrustedProjectsPath returns the path to the user's trusted projects file.
func TrustedProjectsPath(getenv func(string) string) string {
	return filepath.Join(filepath.Dir(DefaultConfigPath(getenv)), "trusted.toml")
}

// IsProjectTrusted reports whether the project config at path with data is trusted.
func IsProjectTrusted(path string, data []byte, getenv func(string) string) bool {
	trustMu.Lock()
	defer trustMu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	trustedPath := TrustedProjectsPath(getenv)
	content, err := os.ReadFile(trustedPath)
	if err != nil {
		return false
	}
	var tf trustedFile
	if err := toml.Unmarshal(content, &tf); err != nil || tf.Trusted == nil {
		return false
	}
	expectedHash, ok := tf.Trusted[absPath]
	if !ok {
		return false
	}
	return expectedHash == FileSHA256(data)
}

// TrustProject records path and its SHA256 in trusted.toml.
func TrustProject(path string, getenv func(string) string) (string, string, error) {
	trustMu.Lock()
	defer trustMu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("resolving path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return "", "", fmt.Errorf("reading project config %s: %w", absPath, err)
	}

	hash := FileSHA256(data)

	trustedPath := TrustedProjectsPath(getenv)
	dir := filepath.Dir(trustedPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", fmt.Errorf("creating config dir: %w", err)
	}

	var tf trustedFile
	if content, err := os.ReadFile(trustedPath); err == nil {
		_ = toml.Unmarshal(content, &tf)
	}
	if tf.Trusted == nil {
		tf.Trusted = make(map[string]string)
	}
	tf.Trusted[absPath] = hash

	out, err := toml.Marshal(tf)
	if err != nil {
		return "", "", fmt.Errorf("marshaling trust file: %w", err)
	}

	tmp := fmt.Sprintf("%s.tmp.%d", trustedPath, os.Getpid())
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return "", "", fmt.Errorf("writing trust file: %w", err)
	}
	if err := os.Rename(tmp, trustedPath); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("renaming trust file: %w", err)
	}

	return absPath, hash, nil
}

// UntrustProject removes path from trusted.toml.
func UntrustProject(path string, getenv func(string) string) (string, error) {
	trustMu.Lock()
	defer trustMu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}

	trustedPath := TrustedProjectsPath(getenv)
	var tf trustedFile
	content, err := os.ReadFile(trustedPath)
	if err != nil {
		return absPath, nil
	}
	if err := toml.Unmarshal(content, &tf); err != nil || tf.Trusted == nil {
		return absPath, nil
	}

	delete(tf.Trusted, absPath)

	out, err := toml.Marshal(tf)
	if err != nil {
		return "", fmt.Errorf("marshaling trust file: %w", err)
	}
	tmp := fmt.Sprintf("%s.tmp.%d", trustedPath, os.Getpid())
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return "", fmt.Errorf("writing trust file: %w", err)
	}
	if err := os.Rename(tmp, trustedPath); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("renaming trust file: %w", err)
	}

	return absPath, nil
}
