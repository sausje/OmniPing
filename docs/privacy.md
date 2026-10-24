# Privacy

OmniPing runs on your own computer or server and keeps everything it records there, in its data folder. It has no account, no telemetry and no analytics. It only contacts other systems for these reasons:

| What | To where | When |
|---|---|---|
| Pings, TCP connections and DNS lookups | The targets you add | All the time, at the interval you set |
| Looking up target names | Your computer's DNS server | When you add a name, and every 10 minutes after that |
| Notifications | The channels you set up: ntfy, Telegram, Discord, Pushover, Gotify, email or a webhook | When an alert rule sends one |
| A traceroute | One of the hosts in an outage rule, and the routers on the way | When that rule starts, at most every 5 minutes |
| A network scan | The address range you enter | Only when you start one |
| The check for a new version | GitHub (`api.github.com`) | Once a day |

The check for a new version asks GitHub for the latest release of OmniPing. The request carries OmniPing's version number, like any request carries your IP address; nothing about your targets, settings or network. Turn it off under **Settings → General → Check for new versions**.

Notifications contain what the alert is about, such as a target's name and address and the time. What a notification service does with them is up to that service's own privacy policy.

The web page loads nothing from other sites: its fonts and scripts are built into OmniPing.
