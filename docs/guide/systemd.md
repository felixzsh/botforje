# Running Botflota as a systemd service

Botflota runs in the foreground and leaves start, stop, and restart to the
supervisor. The repository ships systemd unit templates and an installer that
writes the unit, creates the config and a secrets file, and enables the
service. The binary itself never calls systemd.

## Quick start: a user service

For a Botflota instance that belongs to your login user:

```sh
make build
make install-service
$EDITOR ~/.config/botflota/secrets.env   # fill it if the config uses ${VAR}
systemctl --user restart botflota
```

What the installer lays down:

| Piece | Path |
| --- | --- |
| Unit | `~/.config/systemd/user/botflota.service` |
| Config | `~/.config/botflota/config.yml` (created with `config init` if missing) |
| Secrets | `~/.config/botflota/secrets.env` (0600) |
| Data | the config's `data_dir`, or `~/.local/share/botflota` |

Follow the logs with:

```sh
journalctl --user -u botflota -f
```

A user service stops when the last session ends. To keep it running after
logout:

```sh
loginctl enable-linger $USER
```

## A system-wide service

For a host-managed instance with a dedicated user:

```sh
make build
sudo scripts/install-service.sh --system
$EDITOR /etc/botflota/secrets.env        # as root, if the config uses ${VAR}
systemctl restart botflota
```

What it lays down:

| Piece | Path |
| --- | --- |
| Binary | `/usr/local/bin/botflota` |
| Unit | `/etc/systemd/system/botflota.service` |
| Config | `/etc/botflota/config.yml` (with `data_dir: /var/lib/botflota`) |
| Secrets | `/etc/botflota/secrets.env` (0600) |
| Data | `/var/lib/botflota` |
| User | `botflota` (created if missing) |

Follow the logs with `journalctl -u botflota -f`.

## Secrets

Secrets never go in the unit file. The unit reads an `EnvironmentFile`, and
the process resolves every `${VAR}` in the config from its environment:

```
BOTFLOTA_WEBHOOK_SECRET=una-cadena-larga
OPENAI_API_KEY=sk-...
```

Keep the file at mode `0600`. The installer only creates a placeholder; it
never stores a value. The same secret must reach whatever consumes the event,
for example the HMAC key a webhook receiver verifies with.

## Operating the service

Start, stop, restart, and inspect through systemd; the CLI has no `stop` or
`restart`, and config reload is automatic, so there is no `reload` either:

```sh
systemctl --user status botflota
systemctl --user restart botflota
systemctl --user stop botflota
```

After rebuilding the binary, re-run the installer so the unit points at the
new path, then restart.

## Graceful shutdown and the stop timeout

On SIGTERM Botflota stops the fleet and drains each bot's outbox, waiting out
its pacing between messages. The unit gives it 120 seconds
(`TimeoutStopSec=120`); raise that if your bots queue large bursts. If the
timeout wins, systemd kills the process: queued messages survive and resume on
the next start, but a send already in flight can be duplicated (delivery is
at-least-once).

## Hardening

The units set `NoNewPrivileges`, `PrivateTmp`, `ProtectSystem=true`,
`ProtectControlGroups`, and `RestrictSUIDSGID`. `ProtectSystem=true` makes
`/usr` and `/boot` read-only but leaves the config directory writable, which
the config publication API needs. If you move `data_dir` or the config to an
unusual path, no change is needed; the process only writes its own data
directory and config files.

## Installer options

```
--user            install a user service (default)
--system          install a system-wide service; needs root
--bin PATH        binary to run (default: <repo>/bin/botflota)
--config PATH     config file to run with
--data-dir PATH   data directory of the system service (default: /var/lib/botflota)
--no-start        install files only; do not enable or start the service
--force           rewrite an existing unit and secrets file
```

## Troubleshooting

- `missing environment variable(s): ...` at start: fill `secrets.env` and
  restart the service.
- The unit starts and exits at once: `journalctl --user -u botflota` (or
  `journalctl -u botflota`) shows the config error.
- The bot is skipped as not ready or the data is empty: the running `data_dir` is not
  the one you expect; check `data_dir` in the config the unit passes with
  `--config`.
