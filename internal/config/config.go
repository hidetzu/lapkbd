// Package config は設定ファイルを読み書きする。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config は lapkbd が扱うキーボードの名前。KWin が報告する名前と完全一致で比べる。
type Config struct {
	Internal string   `toml:"internal"`
	External []string `toml:"external"`
}

// Validate は設定が安全に使えるかを確かめる。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Internal) == "" {
		return errors.New("internal is required")
	}
	if len(c.External) == 0 {
		return errors.New("external needs at least one keyboard name")
	}
	for _, name := range c.External {
		if strings.TrimSpace(name) == "" {
			return errors.New("external contains an empty name")
		}
	}
	// 両方に挙げると、そのキーボードは自分で自分を無効にする
	if slices.Contains(c.External, c.Internal) {
		return fmt.Errorf("%q is listed as both internal and external", c.Internal)
	}
	return nil
}

// DefaultPath はユーザーの設定ディレクトリの下の設定ファイルの場所を返す。
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lapkbd", "config.toml"), nil
}

// Load は設定ファイルを読んで確かめる。無いときのエラーは fs.ErrNotExist で判別できる。
func Load(path string) (Config, error) {
	var c Config
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %q", path, undecoded[0].String())
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Write は設定ファイルを書く。force でなければ既存のファイルを置き換えない。
func Write(path string, c Config, force bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.WriteString("# Names as KWin reports them. See them with: lapkbd status\n")
	buf.WriteString("# A keyboard can have a different name over USB and Bluetooth; list each one you use.\n")
	if err := toml.NewEncoder(&buf).Encode(c); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists (use -force to replace it)", path)
	}
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
