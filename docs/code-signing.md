# Code signing policy

**Status:** OmniPing has applied to [SignPath Foundation](https://signpath.org) for free code signing of its Windows programs. Until the application is approved, the Windows programs are not signed, and Windows may say it protected your PC when you start one.

## What gets signed

The Windows programs in each [release](https://github.com/sausje/OmniPing/releases):
- `OmniPing.exe` in the portable zips (amd64 and arm64)
- the installer, `OmniPing-<version>-windows-setup.exe`, and the `OmniPing.exe` inside it

Only programs built from this repository's source by its release workflow (`.github/workflows/release.yml`) on GitHub Actions are signed, from a version tag on `main`. Nothing built elsewhere, and no third-party program on its own, is signed. The installer is made with [Inno Setup](https://jrsoftware.org/isinfo.php).

## Team

| Role | Members |
|---|---|
| Committers and reviewers | [sausje](https://github.com/sausje) |
| Approvers | [sausje](https://github.com/sausje) |

- Changes reach `main` only through pull requests, and every one must pass the tests on Linux, Windows and macOS first. Pull requests from people outside the team are reviewed by a reviewer before they are merged, and their workflows only run once a reviewer allows it.
- An approver approves each signing request by hand, for each release.
- Everyone in the team signs in to GitHub and SignPath with two-factor authentication.

OmniPing is written with Claude Code; the committers decide what goes in and review every change.

## Privacy

See [Privacy](privacy.md): what OmniPing sends where, and how to turn off the one thing it does by itself, the daily check for a new version.
