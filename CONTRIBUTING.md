# Contributing

Bug reports, ideas and pull requests are all welcome.

## Reporting a bug or an idea

Use [Issues](https://github.com/sausje/OmniPing/issues/new/choose). For a bug, say:
- what you did, what you expected, and what happened;
- the version (Settings → About);
- how you run it: Docker on a Raspberry Pi, the Windows app, the Linux package, and so on.

Don't paste your own addresses, tokens or webhook URLs.

For security problems, see [SECURITY.md](SECURITY.md) instead.

## Working on the code

OmniPing is one Go program with the page built in. It uses only Go's standard library, so there's nothing else to install. You need Go 1.25 or newer.

```bash
go test ./...
go run . -web web -addr 127.0.0.1:8099
```

With `-web web`, the page is served from disk: edit a file in `web/` and reload. There's no build step.

Before a pull request:
- run `gofmt -l .`, `go vet ./...` and `go test ./...`;
- for changes to the page, run the browser check, which opens every page at desktop and phone width and fails on any script error. It needs Node 22 and Chrome, Chromium or Edge:
  ```bash
  go build -o omniping . && node scripts/browser-check.mjs ./omniping
  ```
- keep new features configurable rather than hard-coded, and keep OmniPing a single program without a database;
- write texts in plain English, and say what a control does.

CI runs the tests on Linux, Windows and macOS, installs the Linux packages, starts the Docker image, builds every platform and opens every page.

## Project layout

| Path | |
|---|---|
| `main.go` | Start-up: options, the data folder, permission to ping, the web server |
| `engine.go` | Pings every target on its interval and keeps names resolved |
| `tcpping.go`, `dnsping.go` | TCP connect times, and DNS lookups as a check |
| `api.go`, `hub.go` | The API the page uses, and live updates to open pages (Server-Sent Events) |
| `config.go`, `groups.go` | Settings, targets and rules, saved safely in `config.json`; automatic groups |
| `alerts.go`, `notify.go` | Alert rules and outage rules, the alert history, and sending notifications with retries |
| `report.go`, `weekly.go` | The report, and the weekly summary built from it |
| `offtime.go`, `traceroute.go` | Expected-off hours; the traceroute when an outage rule starts |
| `auth.go` | The optional password |
| `metrics.go`, `updates.go` | Prometheus metrics; the daily check for a new version |
| `discover.go`, `parse.go` | Network scans; reading what you type and target list files |
| `privdrop_*.go` | Continuing as an ordinary user after start-up in Docker |
| `shell.go`, `appsettings.go`, `appapi.go`, `instance.go`, `*_windows.go` | The Windows app: tray icon and menu, its own settings, notifications, start with Windows, one copy at a time |
| `internal/icmp/` | Sending pings and traceroutes on Linux, macOS and Windows |
| `internal/history/` | Samples in memory and in per-day files on disk |
| `web/` | The page: plain HTML, CSS and JavaScript, built into the program |
| `winres/`, `installer/` | The Windows app's icon and manifest, and its installer (Inno Setup) |
| `packaging/` | The Linux packages and their service, and Scoop and winget notes |
| `docs/` | The documentation |
| `.github/` | CI, releases, and the scripts they use |

## Building the Windows app

As the releases do, with its icon and without a console window:

```bash
go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64
go build -ldflags=-H=windowsgui -o OmniPing.exe .
```

The first command writes `rsrc_windows_amd64.syso` from `winres/winres.json`. [Inno Setup 6](https://jrsoftware.org/isinfo.php) then makes the installer: `ISCC /DAppVersion=1.0.0 installer\omniping.iss`.

## Releases

Every push to `main` is tested and published as `ghcr.io/sausje/omniping:edge`. A release is an annotated tag; its message becomes the release notes, in Markdown. `--cleanup=whitespace` keeps git from dropping headings, whose lines start with `#`:

```bash
git tag -a --cleanup=whitespace v1.0.1
git push origin v1.0.1
```

GitHub Actions then tests the image, publishes it as `:1.0.1`, `:1.0` and `:latest` for amd64, arm64 and armv7, builds the programs, the Linux packages, the Windows zips and the installer, and creates the release with SHA-256 checksums. A version with a dash, like `v1.1.0-rc.1`, becomes a pre-release and never `:latest`.

OmniPing is built with Claude Code. Pull requests from people are just as welcome, and are reviewed the same way.
