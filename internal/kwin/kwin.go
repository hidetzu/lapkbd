// Package kwin はセッションバス越しに KWin の入力デバイス API と話す。
package kwin

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	service      = "org.kde.KWin"
	root         = dbus.ObjectPath("/org/kde/KWin/InputDevice")
	managerIface = "org.kde.KWin.InputDeviceManager"
	deviceIface  = "org.kde.KWin.InputDevice"
	propsIface   = "org.freedesktop.DBus.Properties"

	callTimeout = 3 * time.Second
)

// Device は KWin が報告する入力デバイス。
type Device struct {
	SysName      string
	Name         string
	Enabled      bool
	AlphaNumeric bool
	Touchpad     bool
	Vendor       uint32
	Product      uint32
}

// Client はセッションバス越しに KWin と話す。
type Client struct {
	conn *dbus.Conn
}

// Connect はセッションバスに繋ぐ。この時点で KWin が動いている必要はない。
func Connect() (*Client, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}
	return &Client{conn: conn}, nil
}

func (k *Client) call(path dbus.ObjectPath, method string, out any, args ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	call := k.conn.Object(service, path).CallWithContext(ctx, method, 0, args...)
	if call.Err != nil {
		return call.Err
	}
	if out != nil {
		return call.Store(out)
	}
	return nil
}

// Devices は読めたデバイスと、KWin が挙げたのに読めなかった sysname を返す。
func (k *Client) Devices() ([]Device, []string, error) {
	var names dbus.Variant
	if err := k.call(root, propsIface+".Get", &names, managerIface, "devicesSysNames"); err != nil {
		return nil, nil, fmt.Errorf("list devices: %w", err)
	}
	sysNames, ok := names.Value().([]string)
	if !ok {
		return nil, nil, fmt.Errorf("list devices: unexpected type %s", names.Signature())
	}

	var devices []Device
	var missing []string
	for _, sysName := range sysNames {
		var props map[string]dbus.Variant
		path := root + dbus.ObjectPath("/"+sysName)
		if err := k.call(path, propsIface+".GetAll", &props, deviceIface); err != nil {
			missing = append(missing, sysName)
			continue
		}
		devices = append(devices, deviceFromProps(sysName, props))
	}
	return devices, missing, nil
}

func deviceFromProps(sysName string, props map[string]dbus.Variant) Device {
	name, _ := props["name"].Value().(string)
	enabled, _ := props["enabled"].Value().(bool)
	alpha, _ := props["alphaNumericKeyboard"].Value().(bool)
	touchpad, _ := props["touchpad"].Value().(bool)
	vendor, _ := props["vendor"].Value().(uint32)
	product, _ := props["product"].Value().(uint32)
	return Device{
		SysName:      sysName,
		Name:         name,
		Enabled:      enabled,
		AlphaNumeric: alpha,
		Touchpad:     touchpad,
		Vendor:       vendor,
		Product:      product,
	}
}

// SetEnabled はデバイスの enabled を書く。
func (k *Client) SetEnabled(sysName string, enabled bool) error {
	path := root + dbus.ObjectPath("/"+sysName)
	return k.call(path, propsIface+".Set", nil, deviceIface, "enabled", dbus.MakeVariant(enabled))
}

// SaveEnabled は kcminputrc にそのデバイスの Enabled=true を書く。
func (k *Client) SaveEnabled(d Device) error {
	cmd := exec.Command("kwriteconfig6", "--file", "kcminputrc",
		"--group", "Libinput",
		"--group", strconv.FormatUint(uint64(d.Vendor), 10),
		"--group", strconv.FormatUint(uint64(d.Product), 10),
		"--group", d.Name,
		"--key", "Enabled", "true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kwriteconfig6: %w: %s", err, out)
	}
	return nil
}

// ReloadConfig は KWin に kcminputrc を読み直させる。KWin は設定をメモリに持っていて、
// kwriteconfig6 の書き込みだけでは、次に作るデバイスに古い値を使う。
// 値の 3, 0 は KGlobalSettings の SettingsChanged と SETTINGS_MOUSE。
// KWin はこのときポインタのデバイスにも設定を適用し直す。
func (k *Client) ReloadConfig() error {
	if err := k.conn.Emit("/KGlobalSettings", "org.kde.KGlobalSettings.notifyChange", int32(3), int32(0)); err != nil {
		return fmt.Errorf("notify KWin of the config change: %w", err)
	}
	return nil
}

// EventKind は Event の種類。
type EventKind string

const (
	DeviceAdded   EventKind = "added"
	DeviceRemoved EventKind = "removed"
	KWinStarted   EventKind = "kwin-started"
)

// Event はデバイスの変化か KWin の起動。SysName はデバイスの変化のときだけ入る。
type Event struct {
	Kind    EventKind
	SysName string
}

// Events はデバイスの変化と KWin の起動を知らせる。セッションバスが切れるとチャネルが閉じる。
func (k *Client) Events() (<-chan Event, error) {
	for _, member := range []string{"deviceAdded", "deviceRemoved"} {
		err := k.conn.AddMatchSignal(
			dbus.WithMatchObjectPath(root),
			dbus.WithMatchInterface(managerIface),
			dbus.WithMatchMember(member),
		)
		if err != nil {
			return nil, fmt.Errorf("watch %s: %w", member, err)
		}
	}
	err := k.conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchArg(0, service),
	)
	if err != nil {
		return nil, fmt.Errorf("watch KWin: %w", err)
	}

	signals := make(chan *dbus.Signal, 64)
	k.conn.Signal(signals)

	events := make(chan Event, 64)
	go func() {
		defer close(events)
		for sig := range signals {
			event, ok := eventFromSignal(sig)
			if !ok {
				continue
			}
			select {
			case events <- event:
			default: // 未処理の通知があれば、この変化も含めて合わせられる。失うのはログの 1 行だけ
			}
		}
	}()
	return events, nil
}

func eventFromSignal(sig *dbus.Signal) (Event, bool) {
	switch sig.Name {
	case managerIface + ".deviceAdded":
		return deviceEvent(DeviceAdded, sig), true
	case managerIface + ".deviceRemoved":
		return deviceEvent(DeviceRemoved, sig), true
	case "org.freedesktop.DBus.NameOwnerChanged":
		// 本体は name, old owner, new owner。new owner が空なら KWin は去っていて、戻るまで話せない
		if len(sig.Body) != 3 {
			return Event{}, false
		}
		name, _ := sig.Body[0].(string)
		owner, _ := sig.Body[2].(string)
		return Event{Kind: KWinStarted}, name == service && owner != ""
	}
	return Event{}, false
}

// deviceEvent は sysname が読めなくても Event を返す。状態を合わせるのに sysname は要らない。
func deviceEvent(kind EventKind, sig *dbus.Signal) Event {
	event := Event{Kind: kind}
	if len(sig.Body) == 1 {
		event.SysName, _ = sig.Body[0].(string)
	}
	return event
}
