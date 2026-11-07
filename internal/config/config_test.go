package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRejectsBadFiles(t *testing.T) {
	tests := map[string]string{
		"no internal":    `external = ["A"]`,
		"no external":    `internal = "A"`,
		"empty external": `internal = "A"` + "\n" + `external = [""]`,
		"same on both":   `internal = "A"` + "\n" + `external = ["A"]`,
		"unknown key":    `internal = "A"` + "\n" + `external = ["B"]` + "\n" + `extrenal = ["C"]`,
		"not toml":       `internal`,
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFile(t, content)); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestLoadOfMissingFileIsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want fs.ErrNotExist so that status can suggest init", err)
	}
}

func TestWriteRoundTripsAndProtectsAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	want := Config{Internal: "Apple Internal", External: []string{"HHKB", "HHKB USB"}}
	if err := Write(path, want, false); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Internal != want.Internal || !slices.Equal(got.External, want.External) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	replacement := Config{Internal: "Other", External: []string{"HHKB"}}
	if err := Write(path, replacement, false); err == nil {
		t.Fatal("want an error when the file exists")
	}
	if got, _ := Load(path); got.Internal != want.Internal {
		t.Error("existing config was changed without force")
	}
	if err := Write(path, replacement, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(path); got.Internal != "Other" {
		t.Error("force did not replace the config")
	}
}
