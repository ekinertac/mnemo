// Package config loads Mnemo's per-machine config file (DESIGN §6.1) so commands work without the
// caller having to export RESTIC_REPOSITORY / AWS_* / RESTIC_PASSWORD by hand. It holds only
// non-secret settings (repo location, host id, exclude globs) plus *references* to secrets — never
// secret values themselves. A secret is fetched by a retrieval command (e.g. a macOS Keychain
// `security` call or a Windows Credential Manager command), a file, or another env var, so the
// secret store is the OS's, not Mnemo's, and the same scheme works cross-platform.
//
// Resolution order (highest wins, per DESIGN §6.1): CLI flags → environment → config file →
// defaults. This package supplies the "config file" tier; ResolveEnv deliberately skips any secret
// already present in the environment so env keeps priority. The file is JSON (stdlib, zero deps).
//
// Related: internal/command/root.go (resolveRepo consults this and sets restic's env),
// internal/restic (receives the resolved env), docs/DESIGN.md §6.1.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
)

// Secret describes HOW to obtain a secret value, not the value. Exactly one source should be set;
// they are tried command → file → env. Keeping the value out of the file means config.json carries
// no plaintext credentials.
type Secret struct {
	Command []string `json:"command,omitempty"` // argv (no shell) — e.g. ["security","find-generic-password",...]
	File    string   `json:"file,omitempty"`    // read trimmed file contents
	Env     string   `json:"env,omitempty"`     // indirect through another env var
}

// Config is the parsed config file. All fields are optional; an absent file is an empty Config.
type Config struct {
	Host    string            `json:"host,omitempty"`    // this machine's id (else MNEMO_HOST/hostname)
	Repo    string            `json:"repo,omitempty"`    // restic repo URL (s3:.../b2:.../path)
	Exclude []string          `json:"exclude,omitempty"` // extra ephemeral-filter globs
	Secrets map[string]Secret `json:"secrets,omitempty"` // env-var-name -> how to fetch it
}

// Load reads the config file at path. A missing file yields an empty Config (not an error), so
// env/flags alone still work; malformed JSON is an error the caller must surface.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

// Resolve fetches the secret's value via its configured source. An empty result is treated as an
// error: injecting an empty credential would only surface later as a confusing restic auth failure.
func (s Secret) Resolve() (string, error) {
	var v string
	switch {
	case len(s.Command) > 0:
		out, err := exec.Command(s.Command[0], s.Command[1:]...).Output()
		if err != nil {
			return "", fmt.Errorf("secret command %v: %w", s.Command, err)
		}
		v = strings.TrimSpace(string(out))
	case s.File != "":
		b, err := os.ReadFile(s.File)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(string(b))
	case s.Env != "":
		v = os.Getenv(s.Env)
	default:
		return "", fmt.Errorf("secret has no source (set command, file, or env)")
	}
	if v == "" {
		return "", fmt.Errorf("secret resolved to an empty value")
	}
	return v, nil
}

// ResolveEnv resolves every configured secret into an env map for the restic child process,
// SKIPPING any whose env var is already set (environment outranks config — DESIGN §6.1). An error
// resolving a needed secret is returned so the caller fails loudly rather than running restic with
// missing credentials.
func (c *Config) ResolveEnv() (map[string]string, error) {
	env := map[string]string{}
	for name, s := range c.Secrets {
		if os.Getenv(name) != "" {
			continue // env wins
		}
		v, err := s.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolving secret %s: %w", name, err)
		}
		env[name] = v
	}
	return env, nil
}
