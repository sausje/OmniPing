# Docker

The image is `ghcr.io/sausje/omniping`, for amd64, arm64 and armv7. The [docker-compose.yml](../docker-compose.yml) in the repository has every option with a comment; the short version is in the [README](../README.md#docker).

## Updating

- **Portainer:** open the stack, choose **Update the stack**, turn on **Re-pull image and redeploy**, and confirm.
- **Compose:** run `docker compose pull`, then `docker compose up -d`.

Settings and history stay in the data volume. OmniPing checks once a day whether there's a newer version and shows it in the top bar; you can turn that off under Settings → General.

| Tag | |
|---|---|
| `latest` | The newest release |
| `1.0`, `1.0.0` | A fixed version or minor version, for updating by hand |
| `edge` | The newest code on `main`, for testing |

## Host network or bridge

The compose file uses `network_mode: host`, which suits a network monitor best: pings go out directly without Docker's address translation, and network scans and IPv6 work without extra setup.

For a normal bridge network, remove `network_mode: host` and add:

```yaml
    ports:
      - "8080:8080"
```

## Permission to ping

Sending pings needs the `NET_RAW` capability. Docker grants it by default, and the compose file asks for it explicitly.

OmniPing starts as root only to open its ping sockets and the web port. It then gives `/data` to `PUID`:`PGID` (1000:1000 by default) and continues as that user. `PUID=0` keeps it running as root.

If you run the container as a non-root user yourself (`user: 1000:1000`), OmniPing uses unprivileged ping sockets instead. These need `net.ipv4.ping_group_range` to include that group, which Docker sets for bridge networks and most Linux systems set for the host.

## Settings at start-up

| Variable | Default | |
|---|---|---|
| `TZ` | UTC | Time zone for alerts, logs, reports and expected-off hours |
| `OMNIPING_PORT` | `8080` | Port of the web page |
| `OMNIPING_ADDR` | `:8080` | Full listen address, to use one network only |
| `OMNIPING_DATA` | `/data` | Folder for settings and history |
| `OMNIPING_PASSWORD` | none | Password for the page and the API; wins over the one set in the page |
| `OMNIPING_ALLOW_SCRIPTS` | off | `true` allows the "Run a script" alert channel |
| `PUID`, `PGID` | `1000` | User and group OmniPing runs as; they own the files in `/data` |

## Your data

Everything is in `/data` inside the container, which the compose file keeps in a volume called `omniping-data`. To keep it in a folder you can see and back up, use a full path instead, like `/opt/omniping:/data`. See [Data and backups](data.md).

On a Raspberry Pi, keep long histories on an SSD over USB rather than the SD card. OmniPing writes in batches, every 30 seconds by default, to spare the card.
