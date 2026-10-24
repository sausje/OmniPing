# Windows

On Windows, OmniPing runs in the background with its icon next to the clock. Each [release](https://github.com/sausje/OmniPing/releases/latest) has it in two forms:

- `OmniPing-<version>-windows-setup.exe` installs it for you only, without administrator rights, and adds it to the Start menu.
- `OmniPing-<version>-windows-amd64-portable.zip` runs from any folder or a USB stick. Unzip it and start `OmniPing.exe`. Use the `arm64` zip on an ARM PC.

OmniPing isn't code-signed yet (see the [code signing policy](code-signing.md)), so Windows may say it protected your PC. Click **More info**, then **Run anyway**.

## Using it

OmniPing opens its page in your browser when it starts. After that:

- **Left-click** the icon to open the page. Windows 11 first puts new icons under the **^** arrow; drag it next to the clock to keep it in view.
- **Right-click** it for the menu: pause pinging, start with Windows, settings, the data folder, the log, and Quit. The icon turns red while an alert is active.
- **Settings → This computer** has the app's own settings: the port, access from other devices, starting with Windows, opening the page at start, and the data folder. They can only be changed on the PC itself.

It pings through the same Windows service as `ping.exe`, so it needs no administrator rights.

## Good to know

- **Your files.** The installed app keeps its settings, history and log in `%LOCALAPPDATA%\OmniPing`; you can move the settings and history to another folder in Settings → This computer. The portable version keeps everything in the `data` folder next to `OmniPing.exe`.
- **Start with Windows.** The installer turns this on unless you untick it. Change it in the menu, in Settings → This computer, or in Task Manager under **Startup apps**.
- **Other devices.** Phones and other PCs on your network can open OmniPing too; Settings → This computer shows the address. The first time, Windows asks whether to let OmniPing through its firewall. Without a password (see [Security](security.md)), turn off **Other devices on your network** on networks you don't trust.
- **The port.** The page is at `http://127.0.0.1:8080`. If another program already uses that port, OmniPing takes the next free one and tells you.
- **Host names.** The app answers to IP addresses, `localhost` and the PC's name. With other devices allowed, it also answers to the name with `.local` or your network's suffix (like `mypc.fritz.box`). Other names are refused, so a website can't reach OmniPing through a name of its own that points at your PC. To use another name, such as a hosts-file alias or a reverse proxy, put it in the environment variable `OMNIPING_ALLOWED_HOSTS` (separate names with commas) and restart OmniPing.
- **Windows notifications.** Add a **Windows notification** channel in Settings → Notifications to see alerts on this PC, even when no OmniPing page is open. They follow Do Not Disturb, so use ntfy, Telegram or email for alerts you can't miss.
- **Updates.** Once a day OmniPing checks for a new version. When there is one, you get a notification once per version, and a note in the page's top bar and under Settings → About. Run the new installer to update in place; your settings and history stay. Turn the check off under Settings → General.
- **In a terminal.** `OmniPing.exe --console` runs it as a plain server in the terminal instead: data in a `data` folder in the current directory, the page on port 8080, and its output in the window.
