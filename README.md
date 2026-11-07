# lapkbd

Turns a laptop's internal keyboard off while an external keyboard is connected, so you can rest the external one on top of it. For KDE Plasma's Wayland session. No root needed.

When the external keyboard goes away, or lapkbd stops, the internal keyboard comes back. lapkbd never switches the trackpad.

## Requirements

- KDE Plasma 6 on Wayland, with `kwriteconfig6`
- systemd, for the user service
- Go 1.27 or later, to install

## Setup

```sh
go install github.com/hidetzu/lapkbd/cmd/lapkbd@latest
lapkbd init              # choose your keyboards from the ones connected now
lapkbd install-service
systemctl --user daemon-reload
systemctl --user enable --now lapkbd.service
```

Check it with `lapkbd status`. The log is in `journalctl --user -u lapkbd`, and `lapkbd -h` lists the commands.

The service runs the binary that ran `install-service`, with the default config path. Run `install-service` again if the binary moves.

## Config

`~/.config/lapkbd/config.toml` (or under `$XDG_CONFIG_HOME`):

```toml
internal = "Apple Inc. Apple Internal Keyboard / Trackpad"
external = ["HHKB-Hybrid_1 Keyboard", "PFU Limited HHKB-Hybrid"]
```

Names match exactly. A keyboard may have a different name over USB and Bluetooth: list each one you want to count. A name that is not connected at `init` time can be passed with `lapkbd init -internal NAME -external NAME`, repeating `-external` as needed.

Restart the service after changing the config.

## How it works

An external keyboard counts as connected when KWin reports an enabled keyboard with one of the `external` names. lapkbd checks this at startup, about a second after KWin's device signals stop, and when KWin restarts.

It errs on the side of keeping a keyboard usable:

- A device it cannot read counts as not connected, so the internal keyboard is turned on.
- On SIGTERM or SIGINT it turns the internal keyboard back on. After a crash or SIGKILL, the service's `ExecStopPost` runs `lapkbd restore`.
- If KWin is not running, lapkbd changes nothing and waits for it.

KWin saves a keyboard it turned off as `Enabled=false` in `kcminputrc`, and the next session would start without it. So lapkbd writes `Enabled=true` back and changes no other key. On a MacBook the trackpad shares that setting, so it is also saved as on.

## If you have no keyboard

Disconnecting the external keyboard brings the internal one back, as does `systemctl --user stop lapkbd.service`. So does `lapkbd restore`, but if the service is running it lasts only until the next device change. These all need the D-Bus object described under Limits.

Logging in again also starts with the internal keyboard on, because of the saved setting above. If the service is enabled and the external keyboard is still connected, lapkbd then turns it off again.

## Limits

- It works through KWin, so other desktops and the SDDM login screen are not covered.
- External keyboards are named explicitly. A virtual keyboard should not switch your real one off.
- KWin can lose the D-Bus object of the internal keyboard when waking from sleep. This happened at 3 of 15 wakes in one day's logs, on one T2 MacBook Pro with KWin 6.8.2. While the object is missing, lapkbd cannot switch the internal keyboard. `lapkbd status` shows a warning and the log shows `unreadable-devices=`. The object came back later each time, usually at the next wake. Logging in again should also bring it back. The cause is not confirmed, and typing on the internal keyboard has not been tested in this state.

## License

[MIT](LICENSE)
