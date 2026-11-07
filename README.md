# lapkbd

Turns a laptop's internal keyboard off while an external keyboard is connected, so you can rest the external one on top of it. For KDE Plasma's Wayland session. No root needed.

When the external keyboard goes away, or lapkbd stops, the internal keyboard comes back. The trackpad stays on throughout.

## Setup

```sh
go install github.com/hidetzu/lapkbd/cmd/lapkbd@latest
lapkbd init              # choose your keyboards from the ones connected now
lapkbd install-service
systemctl --user daemon-reload
systemctl --user enable --now lapkbd.service
```

Check it with `lapkbd status`.

## Config

`~/.config/lapkbd/config.toml`:

```toml
internal = "Apple Inc. Apple Internal Keyboard / Trackpad"
external = ["HHKB-Hybrid_1 Keyboard", "PFU Limited HHKB-Hybrid"]
```

Names match exactly. A keyboard may have a different name over USB and Bluetooth: list each one you want to count. A name that is not connected at `init` time can be passed with `lapkbd init -internal NAME -external NAME`.

Restart the service after changing the config.

## If you have no keyboard

Stopping the service brings the internal keyboard back, as does `lapkbd restore`. Logging in again also does: lapkbd never leaves the internal keyboard saved as off.

## Limits

- It works through KWin, so other desktops and the SDDM login screen are not covered.
- External keyboards are named explicitly. A virtual keyboard should not switch your real one off.

The full behaviour is in [SPEC.md](SPEC.md).
