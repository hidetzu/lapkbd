// Package guard は内蔵キーボードをいつ有効にするかを決めて KWin に適用する。
// 判断の決まりの正本は SPEC.md の「動作の決まり」。
package guard

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/hidetzu/lapkbd/internal/config"
	"github.com/hidetzu/lapkbd/internal/kwin"
)

// QuietPeriod は、デバイスの信号が途切れてから動くまでの時間。スリープ復帰では信号がまとめて来る。
const QuietPeriod = time.Second

// Backend は guard が使う KWin の機能。
type Backend interface {
	// Devices は読めたデバイスと、KWin が挙げたのに読めなかった sysname を返す。
	Devices() ([]kwin.Device, []string, error)
	SetEnabled(sysName string, enabled bool) error
	// SaveEnabled は kcminputrc にそのデバイスの Enabled=true を書く。
	SaveEnabled(d kwin.Device) error
}

// IsInternalKeyboard は d が切り替えの対象の内蔵キーボードかを返す。
func IsInternalKeyboard(d kwin.Device, c config.Config) bool {
	return d.Name == c.Internal && d.AlphaNumeric && !d.Touchpad
}

func isInternalTouchpad(d kwin.Device, c config.Config) bool {
	return d.Name == c.Internal && d.Touchpad
}

// IsUsableExternal は d が使える外付けキーボードかを返す。
// 入力できないデバイスで内蔵キーボードを無効にしないよう、有効な英数字キーボードだけを数える。
func IsUsableExternal(d kwin.Device, c config.Config) bool {
	return slices.Contains(c.External, d.Name) && d.AlphaNumeric && d.Enabled
}

// setIfDifferent は値が違うときだけ書く。トラックパッドの enabled は書くとタッチが切れる。
func setIfDifferent(b Backend, d kwin.Device, want bool) error {
	if d.Enabled == want {
		return nil
	}
	if err := b.SetEnabled(d.SysName, want); err != nil {
		return fmt.Errorf("set %s enabled=%t: %w", d.SysName, want, err)
	}
	return nil
}

// Reconcile は KWin の状態を設定に合わせ、結果の 1 行を返す。
func Reconcile(b Backend, c config.Config) (string, error) {
	devices, missing, err := b.Devices()
	if err != nil {
		return "", err
	}

	external := slices.ContainsFunc(devices, func(d kwin.Device) bool { return IsUsableExternal(d, c) })
	want := !external

	var errs []error
	var saveTarget *kwin.Device
	for i, d := range devices {
		switch {
		case IsInternalKeyboard(d, c):
			errs = append(errs, setIfDifferent(b, d, want))
		case isInternalTouchpad(d, c):
			errs = append(errs, setIfDifferent(b, d, true))
		default:
			continue
		}
		if saveTarget == nil {
			saveTarget = &devices[i]
		}
	}

	// KWin はキーボードを無効にすると Enabled=false を保存する。残すと lapkbd の有無によらず、
	// 次のセッションが内蔵キーボード無しで (MacBook ならトラックパッドも無しで) 始まる。
	if saveTarget != nil {
		errs = append(errs, b.SaveEnabled(*saveTarget))
	}

	return summarize(b, c, external, want, missing), errors.Join(errs...)
}

// summarize は読み直した値を使う。ログに出すのは頼んだ値ではなく KWin の実際の値。
func summarize(b Backend, c config.Config, external, want bool, missing []string) string {
	var actual []string
	devices, _, err := b.Devices()
	if err == nil {
		for _, d := range devices {
			if IsInternalKeyboard(d, c) {
				actual = append(actual, fmt.Sprintf("%s:%t", d.SysName, d.Enabled))
			}
		}
	}
	state := strings.Join(actual, ",")
	if err != nil {
		state = "unreadable"
	} else if state == "" {
		state = "not-found"
	}
	line := fmt.Sprintf("external=%t want-internal=%t internal=%s", external, want, state)
	if len(missing) > 0 {
		line += " unreadable-devices=" + strings.Join(missing, ",")
	}
	return line
}

// Restore は内蔵キーボードと同じ名前のデバイスをすべて有効に戻す。
func Restore(b Backend, c config.Config) error {
	devices, _, err := b.Devices()
	if err != nil {
		return err
	}
	var errs []error
	saved := false
	for _, d := range devices {
		if d.Name != c.Internal {
			continue
		}
		errs = append(errs, setIfDifferent(b, d, true))
		if !saved {
			errs = append(errs, b.SaveEnabled(d))
			saved = true
		}
	}
	return errors.Join(errs...)
}

// Watch は最初に 1 回、その後は信号のまとまりごとに 1 回、状態を合わせる。ctx が終わると内蔵キーボードを戻して
// nil を返し、events が閉じるとエラーを返す。KWin に届かないのはログに残すだけで、KWin が戻れば信号が来る。
func Watch(ctx context.Context, b Backend, c config.Config, events <-chan struct{}, quiet time.Duration, logger *log.Logger) error {
	apply := func() {
		summary, err := Reconcile(b, c)
		if summary != "" {
			logger.Print(summary)
		}
		if err != nil {
			logger.Printf("warning: %v", err)
		}
	}

	apply()

	timer := time.NewTimer(quiet)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := Restore(b, c); err != nil {
				logger.Printf("warning: restore: %v", err)
			}
			return nil
		case _, ok := <-events:
			if !ok {
				return errors.New("session bus connection closed")
			}
			timer.Reset(quiet)
		case <-timer.C:
			apply()
		}
	}
}
