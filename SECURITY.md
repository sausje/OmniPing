# Security

## Reporting a problem

Please don't open a public issue for a security problem. Use **Report a vulnerability** on the repository's [Security tab](https://github.com/sausje/OmniPing/security/advisories/new) instead, so it can be fixed before it's known. Say what you found, how to reproduce it, and which version (Settings → About) or image tag you used.

You'll get an answer within a week. Fixes go into a new release; the release notes say what was fixed once it's out.

## What to expect from OmniPing

OmniPing is made for a home or office network.

- **No password by default.** Anyone who can open the page can see your hosts, change settings and send notifications through your channels. Set a password (Settings → General, or `OMNIPING_PASSWORD`) when OmniPing can be reached from outside your network, and put a reverse proxy with HTTPS in front of it rather than opening its port to the internet.
- **Commands from alerts are off** unless you set `OMNIPING_ALLOW_SCRIPTS=true`, because they run anything on the server.
- **Other websites can't use OmniPing through your browser:** changes made from another site are refused (cross-site request forgery), and the Windows app only answers to its own addresses (DNS rebinding).

Problems in these protections, ways around the password, and ways to run commands or read files on the server are all worth reporting.
