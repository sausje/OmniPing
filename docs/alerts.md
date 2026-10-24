# Alerts and notifications

## Alert rules

A rule has conditions and actions. The conditions are packet loss, average latency, no reply for a while, or a changed address. Each action picks a notification channel and when to send: when the alert starts, on every sample while it lasts, when it ends, or while all is well.

A rule watches all targets, or the targets and groups you pick. Under **Except** you leave some out, like a PC that is off at night.

## Outage rules

Tick **Treat these targets as one** and the rule only fires when all of its targets fail at the same time. While it fires, other alerts for those targets are held back. **+ Internet outage rule** sets one up for your Internet group. It sends phone notifications once the connection is back, since they can't arrive before that, with a message like "The internet was down for 12 min (14:03 – 14:15)".

When an outage rule starts, OmniPing also runs a traceroute to one of its internet hosts (at most once every 5 minutes) and keeps it in the alert history and the report. The last hop that still answered shows where it breaks: if your router answers and nothing after it does, the problem is the line or your provider. It needs no extra permissions. It isn't available on macOS.

## Channels

Add channels under **Settings → Notifications**:

- **ntfy**, **Telegram**, **Discord**, **Pushover** and **Gotify** for push notifications
- **Email** over SMTP
- **Webhook**, for Home Assistant and the like
- **Log file**, written inside the data folder's `logs` folder
- **Browser**: a pop-up with sound in open OmniPing pages. Browsers only allow these over HTTPS or on localhost.
- **Windows notification**, in the Windows app
- **Run a script**, off unless OmniPing is started with `OMNIPING_ALLOW_SCRIPTS=true`. The script gets the alert in environment variables (`OMNIPING_EVENT`, `OMNIPING_TITLE`, `OMNIPING_MESSAGE`, `OMNIPING_TARGET`, `OMNIPING_IP` and more).

A notification that can't be sent, because the internet is down for example, is retried for up to six hours.

## ntfy on your phone and PC

[ntfy](https://ntfy.sh) is the quickest way to get notifications on both:

1. In OmniPing, go to **Settings → Notifications → ntfy push → Add**. A random topic name is filled in. Keep it random, because anyone who knows the topic can read it.
2. Install the ntfy app on your phone and subscribe to that topic. On your PC, open `ntfy.sh/app`, subscribe to the same topic and allow notifications.
3. Press **Test**, then add the channel to a rule under **Alerts**.

A self-hosted ntfy server works the same way: use its address instead of `ntfy.sh`.

## Weekly summary

Turn it on under **Settings → Notifications → Weekly summary** and pick a day, a time and channels. Once a week you get a short message about the last 7 days: how often and how long the internet was down (from your outage rules), the lowest uptime, the most packet loss and the alerts. It uses the same numbers as the report. The time is the server's; in Docker, set `TZ`.
