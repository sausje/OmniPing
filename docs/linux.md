# Linux

Without Docker, the easiest way to run OmniPing on Linux is the package. It installs OmniPing as a service that starts right away and at every boot.

## Install the package

Each [release](https://github.com/sausje/OmniPing/releases/latest) has packages for three kinds of computer:

| Computer | Debian, Ubuntu, Raspberry Pi OS | Fedora and other RPM systems |
|---|---|---|
| Ordinary 64-bit PC or server | `omniping_amd64.deb` | `omniping.x86_64.rpm` |
| 64-bit ARM: Raspberry Pi 3, 4, 5 with a 64-bit OS | `omniping_arm64.deb` | `omniping.aarch64.rpm` |
| 32-bit Raspberry Pi OS | `omniping_armhf.deb` | `omniping.armv7hl.rpm` |

`dpkg --print-architecture` tells which `.deb` you need. Then, for example:

```bash
curl -fsSLO https://github.com/sausje/OmniPing/releases/latest/download/omniping_arm64.deb
sudo apt install ./omniping_arm64.deb
```

On Fedora:

```bash
sudo dnf install https://github.com/sausje/OmniPing/releases/latest/download/omniping.x86_64.rpm
```

Open `http://<computer>:8080` in a browser.

## What the package does

- The program goes to `/usr/bin/omniping`, and a systemd service called `omniping` starts it.
- The service runs as a user of its own that systemd creates, with only the permission to ping (and to use a port below 1024, if you choose one). It can't write anywhere except its data folder.
- Settings and history are kept in `/var/lib/omniping`.

## Settings

The port and the password are in `/etc/default/omniping`:

```bash
sudo nano /etc/default/omniping
sudo systemctl restart omniping
```

Everything else is set in the page. Times in alerts and reports follow the computer's time zone (`timedatectl`).

## The service

| | |
|---|---|
| `systemctl status omniping` | Is it running? |
| `journalctl -u omniping` | Its log |
| `sudo systemctl restart omniping` | Restart it |
| `sudo systemctl disable --now omniping` | Stop it and don't start it at boot |

## Updating

Download the new package and install it the same way. The service restarts by itself; settings and history stay. OmniPing checks once a day whether there's a newer version and shows it in the top bar (Settings → General turns that off).

## Removing it

```bash
sudo apt remove omniping
```

or `sudo dnf remove omniping`. This keeps your settings and history in `/var/lib/omniping`, so installing it again picks them up. To delete them too, run `sudo rm -rf /var/lib/private/omniping /var/lib/omniping`.

## Without the package

Each release also has the bare program: `omniping-<version>-linux-amd64`, `-linux-arm64` and `-linux-armv7`. Make it executable and start it:

```bash
chmod +x omniping-*-linux-amd64
./omniping-*-linux-amd64
```

It keeps its data in a `data` folder in the current directory; `-data <folder>` changes that, and `-addr` the listen address. It needs permission to ping, in one of these ways:

- most distributions allow unprivileged ping sockets already, so it just works;
- give the program the capability: `sudo setcap cap_net_raw+ep omniping-*-linux-amd64`;
- allow unprivileged ping sockets for everyone: `sudo sysctl net.ipv4.ping_group_range="0 2147483647"`;
- or run it as root.

OmniPing says which way it pings in Settings → About, and in its first log line.
