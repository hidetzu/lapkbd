package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hidetzu/lapkbd/internal/config"
	"github.com/hidetzu/lapkbd/internal/kwin"
)

const (
	internalName = "Apple Internal Keyboard / Trackpad"
	bluetoothKbd = "HHKB-Hybrid_1 Keyboard"
	usbKbd       = "PFU Limited HHKB-Hybrid"
)

func keyboard(name string) kwin.Device {
	return kwin.Device{Name: name, AlphaNumeric: true, Enabled: true}
}

func trackpad() kwin.Device {
	return kwin.Device{Name: internalName, Touchpad: true, Enabled: true}
}

func TestRunExitCodes(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.toml")
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"no arguments prints usage", nil, ExitOK},
		{"help", []string{"help"}, ExitOK},
		{"command help", []string{"run", "-h"}, ExitOK},
		{"unknown command", []string{"nope"}, ExitUsage},
		{"unknown flag", []string{"run", "-nope"}, ExitUsage},
		{"stray argument", []string{"run", "extra"}, ExitUsage},
		{"run without a config fails before touching KWin", []string{"run", "-config", missing}, ExitFailure},
		{"restore without a config fails before touching KWin", []string{"restore", "-config", missing}, ExitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Run(tt.args, io.Discard, io.Discard); got != tt.want {
				t.Errorf("exit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInitWithFlagsWritesConfigAndKeepsExistingOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	args := []string{"init", "-config", path, "-internal", internalName, "-external", bluetoothKbd, "-external", usbKbd}
	if got := Run(args, io.Discard, io.Discard); got != ExitOK {
		t.Fatalf("exit = %d", got)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Internal != internalName || !slices.Equal(c.External, []string{bluetoothKbd, usbKbd}) {
		t.Errorf("got %+v", c)
	}

	var stderr bytes.Buffer
	if got := Run(args, io.Discard, &stderr); got != ExitFailure {
		t.Errorf("rerun exit = %d, want %d", got, ExitFailure)
	}
	if !strings.Contains(stderr.String(), "-force") {
		t.Errorf("stderr should point at -force: %q", stderr.String())
	}
}

func TestInitWithOnlyInternalIsRejectedAndWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if got := Run([]string{"init", "-config", path, "-internal", internalName}, io.Discard, io.Discard); got != ExitFailure {
		t.Errorf("exit = %d, want %d", got, ExitFailure)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config should not exist: %v", err)
	}
}

func TestPickKeyboardsReturnsTheChosenNames(t *testing.T) {
	devices := []kwin.Device{keyboard(internalName), trackpad(), keyboard(bluetoothKbd), keyboard(usbKbd)}
	got, err := pickKeyboards(strings.NewReader("1\n2, 3\n"), io.Discard, devices)
	if err != nil {
		t.Fatal(err)
	}
	if got.Internal != internalName || !slices.Equal(got.External, []string{bluetoothKbd, usbKbd}) {
		t.Errorf("got %+v", got)
	}
}

func TestPickKeyboardsRejectsBadInput(t *testing.T) {
	devices := []kwin.Device{keyboard(internalName), keyboard(bluetoothKbd)}
	tests := map[string]string{
		"out of range":     "9\n2\n",
		"not a number":     "x\n2\n",
		"two internal":     "1,2\n2\n",
		"no internal":      "\n2\n",
		"no external":      "1\n\n",
		"same keyboard":    "1\n1\n",
		"input ends early": "1\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := pickKeyboards(strings.NewReader(input), io.Discard, devices); err == nil {
				t.Error("want an error")
			}
		})
	}
}

func TestPickKeyboardsNeedsTwoKeyboards(t *testing.T) {
	devices := []kwin.Device{keyboard(internalName), trackpad()}
	if _, err := pickKeyboards(strings.NewReader("1\n"), io.Discard, devices); err == nil {
		t.Error("want an error with only one keyboard")
	}
}

func TestServiceUnitQuotesAndEscapesThePath(t *testing.T) {
	unit := serviceUnit("/home/a b/100%/lapkbd")
	for _, want := range []string{
		`ExecStart="/home/a b/100%%/lapkbd" run`,
		`ExecStopPost=-"/home/a b/100%%/lapkbd" restore`,
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %s:\n%s", want, unit)
		}
	}
}
