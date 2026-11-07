// Package cli はサブコマンド、出力、終了コードを持つ。
// 人が読む文と終了コードを決めるのはこのパッケージだけ。
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/hidetzu/lapkbd/internal/config"
	"github.com/hidetzu/lapkbd/internal/guard"
	"github.com/hidetzu/lapkbd/internal/kwin"
)

// 終了コード。
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

const usage = `lapkbd turns the internal keyboard off while an external keyboard is connected.

Usage:
  lapkbd init             choose the keyboards and write the config
  lapkbd status           show the config, the service and the keyboards KWin sees
  lapkbd run              stay running and keep the internal keyboard in step
  lapkbd restore          turn the internal keyboard back on
  lapkbd install-service  write the systemd user unit

Run "lapkbd <command> -h" for a command's flags.
`

const serviceName = "lapkbd.service"

// usageError は使い方の誤り。終了コードを ExitUsage にする。
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// app は各コマンドの出力先を持つ。
type app struct {
	stdout io.Writer
	stderr io.Writer
}

// Run はコマンドを実行して終了コードを返す。
func Run(args []string, stdout, stderr io.Writer) int {
	a := app{stdout: stdout, stderr: stderr}
	err := a.dispatch(args)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
		return ExitOK
	case errors.Is(err, errFlagParse):
		// flag パッケージがすでに stderr へ出している
		return ExitUsage
	}
	fmt.Fprintln(stderr, "lapkbd:", err)
	var ue usageError
	if errors.As(err, &ue) {
		return ExitUsage
	}
	return ExitFailure
}

func (a app) dispatch(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.stdout, usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "init":
		return a.cmdInit(rest)
	case "status":
		return a.cmdStatus(rest)
	case "run":
		return a.cmdRun(rest)
	case "restore":
		return a.cmdRestore(rest)
	case "install-service":
		return a.cmdInstallService(rest)
	case "help", "-h", "--help":
		fmt.Fprint(a.stdout, usage)
		return nil
	}
	return usageError{fmt.Sprintf("unknown command %q (run lapkbd help)", cmd)}
}

// errFlagParse は flag パッケージが報告済みのフラグの誤り。
var errFlagParse = errors.New("invalid flags")

func (a app) newFlagSet(name string) *flag.FlagSet {
	fset := flag.NewFlagSet(name, flag.ContinueOnError)
	fset.SetOutput(a.stderr)
	return fset
}

func parseFlags(fset *flag.FlagSet, args []string) error {
	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errFlagParse
	}
	if fset.NArg() > 0 {
		return usageError{fmt.Sprintf("unexpected argument %q", fset.Arg(0))}
	}
	return nil
}

// parseWithConfig はフラグを解釈して設定ファイルの場所を決める。
func parseWithConfig(fset *flag.FlagSet, args []string) (string, error) {
	path := fset.String("config", "", "config file (default: ~/.config/lapkbd/config.toml)")
	if err := parseFlags(fset, args); err != nil {
		return "", err
	}
	if *path != "" {
		return *path, nil
	}
	return config.DefaultPath()
}

func (a app) cmdRun(args []string) error {
	path, err := parseWithConfig(a.newFlagSet("run"), args)
	if err != nil {
		return err
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	client, err := kwin.Connect()
	if err != nil {
		return err
	}
	// 最初に合わせる前に購読して、その間の変化を取りこぼさない
	events, err := client.Events()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return guard.Watch(ctx, client, c, events, guard.QuietPeriod, log.New(a.stdout, "", 0))
}

func (a app) cmdRestore(args []string) error {
	path, err := parseWithConfig(a.newFlagSet("restore"), args)
	if err != nil {
		return err
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	client, err := kwin.Connect()
	if err != nil {
		return err
	}
	return guard.Restore(client, c)
}

func (a app) cmdStatus(args []string) error {
	path, err := parseWithConfig(a.newFlagSet("status"), args)
	if err != nil {
		return err
	}
	client, err := kwin.Connect()
	if err != nil {
		return err
	}
	devices, missing, err := client.Devices()
	if err != nil {
		return fmt.Errorf("%w (is this a KDE Plasma Wayland session?)", err)
	}

	c, configErr := config.Load(path)
	switch {
	case errors.Is(configErr, fs.ErrNotExist):
		fmt.Fprintf(a.stdout, "config    none yet at %s (run: lapkbd init)\n", path)
	case configErr != nil:
		fmt.Fprintf(a.stdout, "config    invalid: %v\n", configErr)
	default:
		fmt.Fprintf(a.stdout, "config    %s\n", path)
	}
	fmt.Fprintf(a.stdout, "service   %s\n", serviceState())
	if configErr == nil {
		printState(a.stdout, devices, c)
	}
	if len(missing) > 0 {
		fmt.Fprintf(a.stdout, "warning   KWin lists %s but has no D-Bus object for it, so it cannot be switched\n", strings.Join(missing, ", "))
	}

	fmt.Fprintln(a.stdout, "\nkeyboards KWin sees:")
	for _, d := range keyboards(devices) {
		role := ""
		if configErr == nil {
			role = roleOf(d, c)
		}
		fmt.Fprintf(a.stdout, "  %-8s %-3s %-8s %s\n", d.SysName, onOff(d.Enabled), role, d.Name)
	}
	return nil
}

func printState(w io.Writer, devices []kwin.Device, c config.Config) {
	var external []string
	var internal []string
	for _, d := range devices {
		if guard.IsUsableExternal(d, c) {
			external = append(external, d.Name)
		}
		if guard.IsInternalKeyboard(d, c) {
			internal = append(internal, onOff(d.Enabled))
		}
	}
	if len(external) > 0 {
		fmt.Fprintf(w, "external  connected: %s\n", strings.Join(external, ", "))
	} else {
		fmt.Fprintln(w, "external  not connected")
	}
	if len(internal) > 0 {
		fmt.Fprintf(w, "internal  %s\n", strings.Join(internal, ", "))
	} else {
		fmt.Fprintf(w, "internal  not found: %q\n", c.Internal)
	}
}

func roleOf(d kwin.Device, c config.Config) string {
	switch {
	case guard.IsInternalKeyboard(d, c):
		return "internal"
	case slices.Contains(c.External, d.Name):
		return "external"
	}
	return ""
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func keyboards(devices []kwin.Device) []kwin.Device {
	var out []kwin.Device
	for _, d := range devices {
		if d.AlphaNumeric {
			out = append(out, d)
		}
	}
	return out
}

func serviceState() string {
	// is-active は active 以外で非 0 を返すが、状態は標準出力に出す
	out, _ := exec.Command("systemctl", "--user", "is-active", serviceName).Output()
	state := strings.TrimSpace(string(out))
	if state == "" {
		return "unknown"
	}
	return state
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ", ") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func (a app) cmdInit(args []string) error {
	fset := a.newFlagSet("init")
	internal := fset.String("internal", "", "internal keyboard name (skips the prompts)")
	var external stringList
	fset.Var(&external, "external", "external keyboard name, repeatable (skips the prompts)")
	force := fset.Bool("force", false, "replace an existing config")
	path, err := parseWithConfig(fset, args)
	if err != nil {
		return err
	}

	c := config.Config{Internal: *internal, External: external}
	if *internal == "" && len(external) == 0 {
		client, err := kwin.Connect()
		if err != nil {
			return err
		}
		devices, _, err := client.Devices()
		if err != nil {
			return fmt.Errorf("%w (is this a KDE Plasma Wayland session?)", err)
		}
		if c, err = pickKeyboards(os.Stdin, a.stdout, devices); err != nil {
			return err
		}
	}
	if err := config.Write(path, c, *force); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "wrote %s\n\nNext:\n  lapkbd install-service\n  systemctl --user daemon-reload\n  systemctl --user enable --now %s\n", path, serviceName)
	return nil
}

// pickKeyboards は、今繋がっているキーボードのどれが内蔵でどれが外付けかを尋ねる。
func pickKeyboards(in io.Reader, out io.Writer, devices []kwin.Device) (config.Config, error) {
	var names []string
	for _, d := range keyboards(devices) {
		if !slices.Contains(names, d.Name) {
			names = append(names, d.Name)
		}
	}
	if len(names) < 2 {
		return config.Config{}, errors.New("need both keyboards connected to choose from; connect the external one, or pass -internal and -external")
	}

	fmt.Fprintln(out, "Keyboards KWin sees now:")
	for i, name := range names {
		fmt.Fprintf(out, "  %d) %s\n", i+1, name)
	}

	reader := bufio.NewReader(in)
	fmt.Fprint(out, "Internal keyboard (number): ")
	internal, err := readChoices(reader, names)
	if err != nil {
		return config.Config{}, err
	}
	if len(internal) != 1 {
		return config.Config{}, errors.New("choose exactly one internal keyboard")
	}
	fmt.Fprint(out, "External keyboards (numbers, comma-separated): ")
	external, err := readChoices(reader, names)
	if err != nil {
		return config.Config{}, err
	}
	c := config.Config{Internal: internal[0], External: external}
	return c, c.Validate()
}

func readChoices(r *bufio.Reader, names []string) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	var chosen []string
	for field := range strings.SplitSeq(line, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 || n > len(names) {
			return nil, fmt.Errorf("%q is not a number from the list", field)
		}
		if name := names[n-1]; !slices.Contains(chosen, name) {
			chosen = append(chosen, name)
		}
	}
	return chosen, nil
}

func (a app) cmdInstallService(args []string) error {
	if err := parseFlags(a.newFlagSet("install-service"), args); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "systemd", "user", serviceName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(serviceUnit(exe)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "wrote %s\n\nNext:\n  systemctl --user daemon-reload\n  systemctl --user enable --now %s\n", path, serviceName)
	return nil
}

func serviceUnit(exe string) string {
	quoted := `"` + strings.ReplaceAll(exe, "%", "%%") + `"`
	return `[Unit]
Description=Turn the internal keyboard off while an external keyboard is connected
After=plasma-kwin_wayland.service
PartOf=graphical-session.target

[Service]
ExecStart=` + quoted + ` run
# Covers a crash or SIGKILL, when run cannot clean up after itself.
ExecStopPost=-` + quoted + ` restore
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
`
}
