# Targets

## Adding targets

Type an address or a name in the top bar and press Enter. Text after the address becomes its name: `192.168.1.1 Router`. You can also:

- paste a list, one target per line;
- load a target list from MultiPing (`.lst` or `.mpl`), as it is;
- use **Add & scan** to find the devices in an address range.

Right-click a target in the table or a timeline to rename it, delete it, move it to a group, set its expected-off hours, or open its timeline in a separate window.

## Ping, TCP or DNS

Besides a ping, a target can measure two other things. Choose them under **Ping with** in the add or edit dialog, or type them:

- **A TCP port** times opening a connection, for hosts that ignore pings: `192.168.1.10:3389 Desktop`.
- **A DNS lookup** asks a DNS server to look up a name and times the answer: `dns 1.1.1.1 example.com Cloudflare`. A server can answer pings while it can't look up names, which makes the internet seem down; this check shows that. "No such name" still counts as an answer, a server failure as lost.

## Groups

New targets sort themselves: private addresses and names like `nas` or `printer.local` go to **Local network**, well-known resolvers like 1.1.1.1 to **DNS servers**, and everything else to **Internet**. If you already have a group that means the same, like `LAN` or `Home`, that one is used. A group you choose yourself always wins.

Move targets with right-click → **Move to group…**, by dragging, or under **Settings → Targets & data → Groups**, where you also rename, reorder and remove groups, or turn automatic groups off.

## Expected-off hours

Some hosts are off on purpose, like a PC at night. Right-click it and choose **Expected off…** to add the times, such as every day 01:00–08:00 or weekdays 09:00–17:00. OmniPing keeps pinging it, but sends no alerts for it then, leaves it out of outage rules, and doesn't count that time in reports and the weekly summary. Timelines show those hours as a grey band.

The times follow the server's clock; in Docker, set `TZ`.

## Timeout

A ping that gets no answer within the timeout counts as lost. The default is 2000 ms. While a ping waits for its answer, newer results for that target are held back, so a short timeout keeps its graph current. Change it under **Settings → Packet**.

## Looking at the results

- **Timelines.** The wheel scrolls through time, Ctrl + wheel zooms, Shift + wheel scrolls the list. Double-click a moment to see the table as it was then. Shift + drag across a timeline to inspect that period: its numbers, a bigger graph, and which other hosts lost pings then. Click a red mark to see what happened at that moment.
- **History.** Open a target for its history, from 15 minutes to 30 days. Drag across the chart to get statistics and a histogram for that period, pin comments to the timeline, or export it as CSV.
- **Report.** The report page covers any period: uptime, packet loss, every outage with its start and length, the outages that hit every host at once, and the hours when problems come back. Leave out hosts that would skew it, like a PC that is off at night. Print it, save it as PDF, or export it as CSV.
