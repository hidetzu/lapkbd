package guard

import (
	"context"
	"errors"
	"io"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hidetzu/lapkbd/internal/config"
	"github.com/hidetzu/lapkbd/internal/kwin"
)

const (
	internalName = "Apple Internal Keyboard / Trackpad"
	bluetoothKbd = "HHKB-Hybrid_1 Keyboard"
	usbKbd       = "PFU Limited HHKB-Hybrid"
)

var testConfig = config.Config{Internal: internalName, External: []string{bluetoothKbd, usbKbd}}

func internalKeyboard(enabled bool) kwin.Device {
	return kwin.Device{SysName: "event13", Name: internalName, AlphaNumeric: true, Enabled: enabled, Vendor: 1452, Product: 636}
}

func trackpad(enabled bool) kwin.Device {
	return kwin.Device{SysName: "event15", Name: internalName, Touchpad: true, Enabled: enabled, Vendor: 1452, Product: 636}
}

func externalKeyboard(name string) kwin.Device {
	return kwin.Device{SysName: "event16", Name: name, AlphaNumeric: true, Enabled: true}
}

type fakeBackend struct {
	devices []kwin.Device
	missing []string
	listErr error
	setErr  error
	writes  []string // SetEnabled に渡された sysname を順に
	saves   int
	// calls は SaveEnabled と ReloadConfig の呼び出しを順に持つ
	calls []string
}

func (f *fakeBackend) Devices() ([]kwin.Device, []string, error) {
	if f.listErr != nil {
		return nil, nil, f.listErr
	}
	return slices.Clone(f.devices), f.missing, nil
}

func (f *fakeBackend) SetEnabled(sysName string, enabled bool) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.writes = append(f.writes, sysName)
	for i := range f.devices {
		if f.devices[i].SysName == sysName {
			f.devices[i].Enabled = enabled
		}
	}
	return nil
}

func (f *fakeBackend) SaveEnabled(kwin.Device) error {
	f.saves++
	f.calls = append(f.calls, "save")
	return nil
}

func (f *fakeBackend) ReloadConfig() error {
	f.calls = append(f.calls, "reload")
	return nil
}

func (f *fakeBackend) enabled(t *testing.T, sysName string) bool {
	t.Helper()
	for _, d := range f.devices {
		if d.SysName == sysName {
			return d.Enabled
		}
	}
	t.Fatalf("no device %s", sysName)
	return false
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name         string
		devices      []kwin.Device
		wantInternal bool
		wantWrites   []string
	}{
		{
			name:         "external connected turns only the keyboard off",
			devices:      []kwin.Device{internalKeyboard(true), trackpad(true), externalKeyboard(bluetoothKbd)},
			wantInternal: false,
			wantWrites:   []string{"event13"},
		},
		{
			name:         "a second configured name counts too",
			devices:      []kwin.Device{internalKeyboard(true), trackpad(true), externalKeyboard(usbKbd)},
			wantInternal: false,
			wantWrites:   []string{"event13"},
		},
		{
			name:         "external gone turns the keyboard back on",
			devices:      []kwin.Device{internalKeyboard(false), trackpad(true)},
			wantInternal: true,
			wantWrites:   []string{"event13"},
		},
		{
			name:         "matching state writes nothing, so touch is not dropped",
			devices:      []kwin.Device{internalKeyboard(false), trackpad(true), externalKeyboard(bluetoothKbd)},
			wantInternal: false,
			wantWrites:   nil,
		},
		{
			name:         "a trackpad left off by a stale saved value comes back",
			devices:      []kwin.Device{internalKeyboard(false), trackpad(false), externalKeyboard(bluetoothKbd)},
			wantInternal: false,
			wantWrites:   []string{"event15"},
		},
		{
			name:         "an unlisted keyboard does not count",
			devices:      []kwin.Device{internalKeyboard(true), trackpad(true), externalKeyboard("Virtual Keyboard")},
			wantInternal: true,
			wantWrites:   nil,
		},
		{
			name: "a listed device that is not a keyboard does not count",
			devices: []kwin.Device{internalKeyboard(true), trackpad(true),
				{SysName: "event20", Name: bluetoothKbd, AlphaNumeric: false, Enabled: true}},
			wantInternal: true,
			wantWrites:   nil,
		},
		{
			name: "an external keyboard turned off in KWin does not count",
			devices: []kwin.Device{internalKeyboard(false), trackpad(true),
				{SysName: "event16", Name: bluetoothKbd, AlphaNumeric: true, Enabled: false}},
			wantInternal: true,
			wantWrites:   []string{"event13"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &fakeBackend{devices: tt.devices}
			if _, err := Reconcile(b, testConfig); err != nil {
				t.Fatal(err)
			}
			if got := b.enabled(t, "event13"); got != tt.wantInternal {
				t.Errorf("internal enabled = %t, want %t", got, tt.wantInternal)
			}
			if !b.enabled(t, "event15") {
				t.Error("trackpad must stay enabled")
			}
			if !slices.Equal(b.writes, tt.wantWrites) {
				t.Errorf("writes = %v, want %v", b.writes, tt.wantWrites)
			}
			if b.saves != 1 {
				t.Errorf("saves = %d, want 1", b.saves)
			}
		})
	}
}

// KWin のメモリに Enabled=false を残すと、作り直されたキーボードが無効で始まり締め出しになる。
func TestTurningTheKeyboardOffMakesKWinReloadTheSavedTrue(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true), trackpad(true), externalKeyboard(bluetoothKbd)}}
	if _, err := Reconcile(b, testConfig); err != nil {
		t.Fatal(err)
	}
	if want := []string{"save", "reload"}; !slices.Equal(b.calls, want) {
		t.Errorf("calls = %v, want %v: the reload must see the saved value", b.calls, want)
	}
}

// 読み直すと KWin はトラックパッドにも設定を適用し直すので、必要なときだけにする。
func TestReloadIsSkippedUnlessTheKeyboardWasTurnedOff(t *testing.T) {
	tests := map[string][]kwin.Device{
		"already off": {internalKeyboard(false), trackpad(true), externalKeyboard(bluetoothKbd)},
		"turned on":   {internalKeyboard(false), trackpad(true)},
		"already on":  {internalKeyboard(true), trackpad(true)},
		"no keyboard": {trackpad(true), externalKeyboard(bluetoothKbd)},
	}
	for name, devices := range tests {
		t.Run(name, func(t *testing.T) {
			b := &fakeBackend{devices: devices}
			if _, err := Reconcile(b, testConfig); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(b.calls, "reload") {
				t.Errorf("calls = %v, want no reload", b.calls)
			}
		})
	}
}

func TestReconcileWritesNothingWhenDevicesCannotBeListed(t *testing.T) {
	b := &fakeBackend{listErr: errors.New("KWin is gone")}
	if _, err := Reconcile(b, testConfig); err == nil {
		t.Fatal("want an error")
	}
	if len(b.writes) != 0 || b.saves != 0 {
		t.Errorf("writes = %v, saves = %d; want none", b.writes, b.saves)
	}
}

func TestReconcileReportsWhatKWinDid(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true), externalKeyboard(bluetoothKbd)}, missing: []string{"event9"}}
	got, err := Reconcile(b, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	want := "external=true want-internal=false internal=event13:false unreadable-devices=event9"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestReconcileReturnsWriteErrorWithSummary(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true), externalKeyboard(bluetoothKbd)}, setErr: errors.New("denied")}
	got, err := Reconcile(b, testConfig)
	if err == nil {
		t.Fatal("want an error")
	}
	want := "external=true want-internal=false internal=event13:true"
	if got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
}

func TestRestoreTurnsInternalOnAndLeavesOthers(t *testing.T) {
	ext := externalKeyboard(bluetoothKbd)
	ext.Enabled = false
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(false), trackpad(true), ext}}
	if err := Restore(b, testConfig); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(b.writes, []string{"event13"}) {
		t.Errorf("writes = %v, want only event13", b.writes)
	}
	if b.saves != 1 {
		t.Errorf("saves = %d, want 1", b.saves)
	}
}

func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// lineLogger は 1 回の Reconcile を 1 行として受け取れるロガーを作る。
func lineLogger(lines chan string) *log.Logger {
	return log.New(writerFunc(func(p []byte) (int, error) {
		lines <- string(p)
		return len(p), nil
	}), "", 0)
}

// reconcileLogger は状態を合わせた結果の行だけを渡す。
func reconcileLogger(lines chan string) *log.Logger {
	return log.New(writerFunc(func(p []byte) (int, error) {
		if strings.HasPrefix(string(p), "external=") {
			lines <- string(p)
		}
		return len(p), nil
	}), "", 0)
}

func TestWatchCollapsesBurstIntoOneReconcile(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true), trackpad(true)}}
	lines := make(chan string, 16)

	const quiet = 100 * time.Millisecond
	events := make(chan kwin.Event)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, b, testConfig, events, quiet, reconcileLogger(lines)) }()

	<-lines // 起動時の 1 回

	for range 5 {
		events <- kwin.Event{Kind: kwin.DeviceAdded, SysName: "event13"}
		time.Sleep(quiet / 5)
	}

	select {
	case <-lines:
	case <-time.After(5 * time.Second):
		t.Fatal("no reconcile after the burst")
	}
	select {
	case line := <-lines:
		t.Fatalf("second reconcile from one burst: %q", line)
	case <-time.After(3 * quiet):
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWatchLogsEveryEventAsItArrives(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true), trackpad(true)}}
	lines := make(chan string, 16)
	events := make(chan kwin.Event)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, b, testConfig, events, time.Hour, lineLogger(lines)) }()

	<-lines // 起動時の 1 回

	for _, sysName := range []string{"event13", "event15"} {
		events <- kwin.Event{Kind: kwin.DeviceRemoved, SysName: sysName}
		select {
		case line := <-lines:
			if !strings.HasPrefix(line, "event=removed device="+sysName+" ") {
				t.Errorf("line = %q", line)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("no log line for %s", sysName)
		}
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEventLine(t *testing.T) {
	hhkb := externalKeyboard(bluetoothKbd)
	const hhkbName = `name="HHKB-Hybrid_1 Keyboard"`
	tests := map[string]struct {
		devices []kwin.Device
		known   []kwin.Device
		event   kwin.Event
		want    string
	}{
		"external on": {
			devices: []kwin.Device{hhkb},
			event:   kwin.Event{Kind: kwin.DeviceAdded, SysName: "event16"},
			want:    "event=added device=event16 role=external enabled=true " + hhkbName,
		},
		"external off is named from memory": {
			known: []kwin.Device{hhkb},
			event: kwin.Event{Kind: kwin.DeviceRemoved, SysName: "event16"},
			want:  "event=removed device=event16 role=external " + hhkbName,
		},
		"internal comes back off": {
			devices: []kwin.Device{internalKeyboard(false)},
			event:   kwin.Event{Kind: kwin.DeviceAdded, SysName: "event13"},
			want:    `event=added device=event13 role=internal enabled=false name="` + internalName + `"`,
		},
		"touchpad": {
			devices: []kwin.Device{trackpad(true)},
			event:   kwin.Event{Kind: kwin.DeviceAdded, SysName: "event15"},
			want:    `event=added device=event15 role=touchpad enabled=true name="` + internalName + `"`,
		},
		"added without a D-Bus object hides the old device": {
			known: []kwin.Device{internalKeyboard(true)},
			event: kwin.Event{Kind: kwin.DeviceAdded, SysName: "event13"},
			want:  "event=added device=event13 unreadable",
		},
		"removed before it was ever read": {
			event: kwin.Event{Kind: kwin.DeviceRemoved, SysName: "event99"},
			want:  "event=removed device=event99 unreadable",
		},
		"KWin started": {
			event: kwin.Event{Kind: kwin.KWinStarted},
			want:  "event=kwin-started",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			known := map[string]kwin.Device{}
			for _, d := range tt.known {
				known[d.SysName] = d
			}
			b := &fakeBackend{devices: tt.devices}
			if got := eventLine(b, testConfig, known, tt.event); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestWatchRestoresOnStop(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true), trackpad(true), externalKeyboard(bluetoothKbd)}}
	lines := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Watch(ctx, b, testConfig, make(chan kwin.Event), time.Hour, lineLogger(lines)) }()

	<-lines
	if b.enabled(t, "event13") {
		t.Fatal("internal should be off while the external keyboard is connected")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !b.enabled(t, "event13") {
		t.Error("internal should be back on after stopping")
	}
}

func TestWatchSurvivesKWinBeingAwayAtStart(t *testing.T) {
	b := &fakeBackend{listErr: errors.New("KWin is not running")}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := Watch(ctx, b, testConfig, make(chan kwin.Event), time.Hour, discardLogger()); err != nil {
		t.Fatalf("Watch should wait for KWin, got %v", err)
	}
}

func TestWatchFailsWhenEventsClose(t *testing.T) {
	b := &fakeBackend{devices: []kwin.Device{internalKeyboard(true)}}
	events := make(chan kwin.Event)
	close(events)
	if err := Watch(context.Background(), b, testConfig, events, time.Hour, discardLogger()); err == nil {
		t.Fatal("want an error so that systemd restarts the service")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
