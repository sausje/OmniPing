# Data and backups

## Where it is

| Installed as | Data folder |
|---|---|
| Docker | `/data` in the container (the `omniping-data` volume in the compose file) |
| Linux package | `/var/lib/omniping` |
| Windows installer | `%LOCALAPPDATA%\OmniPing` (can be moved in Settings → This computer) |
| Windows portable | the `data` folder next to `OmniPing.exe` |
| A program started by hand | a `data` folder in the current directory, or the folder given with `-data` |

In it:

| | |
|---|---|
| `config.json` | Targets, rules, channels and settings. `config.json.bak` is the version before the last save, used automatically if a power cut damages `config.json`. |
| `history/` | Every ping, in one file per day |
| `events.jsonl` | The alert history |
| `logs/` | Files written by log channels |
| `auth.json` | The password, as a hash, when one is set |

## Disk use

Each ping takes eight bytes. Ten targets pinged every second come to about 7 MB a day, so 90 days of full detail is about 600 MB. Older data can be kept as one-minute summaries, at about 0.3 MB a day for those ten targets. Set how long to keep each under **Settings → Storage**.

OmniPing writes in batches, every 30 seconds by default, to spare SD cards. On a Raspberry Pi, keep long histories on an SSD over USB.

## Backups

**Settings → Targets & data → Download settings backup** saves every target, rule, channel and setting in one file. **Restore a backup** on the same page puts one back, on the same server or a new one. The settings from before a restore are kept as `config.before-restore.json`.

The backup doesn't include the history or the password. To keep the history too, back up the whole data folder.
