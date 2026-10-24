# macOS

Each [release](https://github.com/sausje/OmniPing/releases/latest) has the program for Apple silicon (`omniping-<version>-darwin-arm64`) and for Intel Macs (`omniping-<version>-darwin-amd64`). It pings without administrator rights.

## Run it

OmniPing isn't signed by Apple, so macOS blocks a downloaded copy until you allow it. In Terminal:

```bash
cd ~/Downloads
mv omniping-*-darwin-arm64 omniping
chmod +x omniping
xattr -d com.apple.quarantine omniping
./omniping
```

Then open `http://localhost:8080`. It keeps its data in a `data` folder in the current directory; `-data <folder>` changes that, and `-addr` the listen address, like `-addr 127.0.0.1:8080` to keep it to this Mac.

The traceroute that runs when the internet goes down isn't available on macOS; everything else is.

## Start it at login

Move the program to a fixed place, like `/usr/local/bin/omniping`, and save this as `~/Library/LaunchAgents/org.omniping.plist`, with your own user name:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>org.omniping</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/omniping</string>
    <string>-data</string>
    <string>/Users/you/Library/Application Support/OmniPing</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
</dict>
</plist>
```

Start it now with `launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/org.omniping.plist`. From then on it starts whenever you log in. `launchctl bootout gui/$(id -u)/org.omniping` stops it.

## Updating

Replace the program with the new one (and run the `xattr` command on it again), then restart it. Settings and history stay in the data folder.
