package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A missing config file is not an error — it yields an empty config (env/flags still drive things).
func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Repo != "" || len(c.Secrets) != 0 {
		t.Errorf("missing config should be empty, got %+v", c)
	}
}

func TestLoadParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{
	  "host": "ekin-mini",
	  "repo": "s3:https://x/bucket",
	  "exclude": ["projects/*/secret-*"],
	  "secrets": {
	    "RESTIC_PASSWORD": {"command": ["printf", "pw"]},
	    "AWS_ACCESS_KEY_ID": {"file": "/tmp/keyid"}
	  }
	}`), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "ekin-mini" || c.Repo != "s3:https://x/bucket" {
		t.Errorf("host/repo wrong: %+v", c)
	}
	if len(c.Exclude) != 1 || c.Exclude[0] != "projects/*/secret-*" {
		t.Errorf("exclude wrong: %+v", c.Exclude)
	}
}

// Malformed JSON is a real error the user must see (not silently ignored).
func TestLoadMalformedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{not json`), 0o600)
	if _, err := Load(path); err == nil {
		t.Error("malformed config must error")
	}
}

func TestSecretResolveCommand(t *testing.T) {
	v, err := Secret{Command: []string{"printf", "hunter2"}}.Resolve()
	if err != nil || v != "hunter2" {
		t.Errorf("command secret = %q,%v want hunter2", v, err)
	}
}

func TestSecretResolveFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "s")
	os.WriteFile(f, []byte("filesecret\n"), 0o600) // trailing newline must be trimmed
	v, err := Secret{File: f}.Resolve()
	if err != nil || v != "filesecret" {
		t.Errorf("file secret = %q,%v want filesecret", v, err)
	}
}

func TestSecretResolveEnv(t *testing.T) {
	t.Setenv("SOME_SRC_VAR", "envsecret")
	v, err := Secret{Env: "SOME_SRC_VAR"}.Resolve()
	if err != nil || v != "envsecret" {
		t.Errorf("env secret = %q,%v want envsecret", v, err)
	}
}

func TestSecretResolveEmptyErrors(t *testing.T) {
	if _, err := (Secret{}).Resolve(); err == nil {
		t.Error("a secret with no source must error")
	}
}

// A source that yields an empty value must error (clear message), not silently inject an empty
// credential that restic later rejects with a confusing error.
func TestSecretResolveEmptyValueErrors(t *testing.T) {
	t.Setenv("EMPTY_SRC", "")
	if _, err := (Secret{Env: "EMPTY_SRC"}).Resolve(); err == nil {
		t.Error("a secret resolving to empty must error")
	}
	if _, err := (Secret{Command: []string{"printf", ""}}).Resolve(); err == nil {
		t.Error("a command yielding empty output must error")
	}
}

// ResolveEnv respects the resolution order: an env var already set wins over config (skipped).
func TestResolveEnvSkipsAlreadySet(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "from-env")
	c := &Config{Secrets: map[string]Secret{
		"AWS_ACCESS_KEY_ID":     {Command: []string{"printf", "from-config"}},
		"AWS_SECRET_ACCESS_KEY": {Command: []string{"printf", "secret-from-config"}},
	}}
	env, err := c.ResolveEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, present := env["AWS_ACCESS_KEY_ID"]; present {
		t.Error("env-set secret must NOT be overridden by config")
	}
	if env["AWS_SECRET_ACCESS_KEY"] != "secret-from-config" {
		t.Errorf("unset secret should resolve from config, got %q", env["AWS_SECRET_ACCESS_KEY"])
	}
}
