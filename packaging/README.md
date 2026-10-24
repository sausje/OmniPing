# Packaging

Ways to install OmniPing on Windows besides the release page. Each can only be done once the repository is public, because they download the release files.

## Scoop

`scoop/omniping.json` is a manifest for the portable version. It keeps the `data` folder between versions (`persist`), so updates keep settings and history. To submit it:
1. Fill in the two `hash` values from the release's `SHA256SUMS`.
2. Test it with `scoop install ./packaging/scoop/omniping.json`.
3. Submit it to the [Extras bucket](https://github.com/ScoopInstaller/Extras).

After that, `checkver` and `autoupdate` pick up new releases by themselves.

## winget

winget wants the installer (`OmniPing-<version>-windows-setup.exe`). Run [wingetcreate](https://github.com/microsoft/winget-create) with the release's download URL:

```
wingetcreate new https://github.com/sausje/OmniPing/releases/download/v1.0.0/OmniPing-1.0.0-windows-setup.exe
```

Use the package identifier `sausje.OmniPing`, and submit the pull request it opens to [winget-pkgs](https://github.com/microsoft/winget-pkgs). For later versions, run `wingetcreate update sausje.OmniPing --version <version> --urls <url>`.

## Signing the Windows program

Unsigned programs get "Windows protected your PC" until enough people have run them. [SignPath](https://signpath.org) signs open-source projects for free.
1. Apply with the public repository.
2. Add its GitHub action to the `windows` and `installer` jobs in `.github/workflows/release.yml`, so `OmniPing.exe` and the installer are signed before they're zipped and uploaded.
