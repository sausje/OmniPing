# Security

OmniPing is made for your own network, and out of the box it has no password: anyone who can open the page can see your targets, change settings, and send notifications through your channels.

## The password

Set one under **Settings → General → Password**, or with `OMNIPING_PASSWORD`, which wins over the page so you can't lock yourself out. Every browser and device then signs in once and stays signed in for 30 days.

- The password is stored as a slow PBKDF2 hash in `auth.json` in the data folder, not in `config.json`, so settings backups don't carry it. Changing it signs every other browser out.
- After 10 wrong passwords from one address, that address has to wait 15 minutes.
- `/healthz` stays open for health checks. `/metrics` takes the password as basic auth (see [Prometheus](prometheus.md)).
- In the Windows app, the PC itself never needs the password; other devices do.

## Reaching OmniPing from outside

Don't open OmniPing's port to the internet, even with a password. Put a reverse proxy with HTTPS in front of it, or use a VPN.

## Built-in protection

- Changes that another website tries to make through your browser are refused (cross-site request forgery).
- The Windows app only answers to its own addresses, so a website can't reach it through a name of its own that points at your PC (DNS rebinding).
- The "Run a script" channel is off unless you set `OMNIPING_ALLOW_SCRIPTS=true`, because it runs commands on the server.
- Log channels can only write inside the data folder's `logs` folder.
- In Docker, OmniPing drops root after start-up; the Linux package runs it as a user of its own with only the permission to ping.

To report a security problem, see [SECURITY.md](../SECURITY.md).
