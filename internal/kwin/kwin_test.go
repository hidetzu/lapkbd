package kwin

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestEventFromSignal(t *testing.T) {
	const nameOwnerChanged = "org.freedesktop.DBus.NameOwnerChanged"
	tests := map[string]struct {
		sig    dbus.Signal
		want   Event
		wantOK bool
	}{
		"added": {
			sig:    dbus.Signal{Name: managerIface + ".deviceAdded", Body: []any{"event16"}},
			want:   Event{Kind: DeviceAdded, SysName: "event16"},
			wantOK: true,
		},
		"removed": {
			sig:    dbus.Signal{Name: managerIface + ".deviceRemoved", Body: []any{"event16"}},
			want:   Event{Kind: DeviceRemoved, SysName: "event16"},
			wantOK: true,
		},
		"added without a sysname still reconciles": {
			sig:    dbus.Signal{Name: managerIface + ".deviceAdded"},
			want:   Event{Kind: DeviceAdded},
			wantOK: true,
		},
		"KWin started": {
			sig:    dbus.Signal{Name: nameOwnerChanged, Body: []any{service, "", ":1.5"}},
			want:   Event{Kind: KWinStarted},
			wantOK: true,
		},
		"KWin left": {
			sig: dbus.Signal{Name: nameOwnerChanged, Body: []any{service, ":1.5", ""}},
		},
		"another name": {
			sig: dbus.Signal{Name: nameOwnerChanged, Body: []any{"org.example", "", ":1.5"}},
		},
		"another signal": {
			sig: dbus.Signal{Name: "org.example.Other"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := eventFromSignal(&tt.sig)
			if ok != tt.wantOK {
				t.Fatalf("ok = %t, want %t", ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("event = %+v, want %+v", got, tt.want)
			}
		})
	}
}
