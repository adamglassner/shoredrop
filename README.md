# ShoreDrop 1.0.0

A modern FTP, FTPS and SFTP client for Windows with a dual-pane browser, transfer
queue, site manager, synchronized browsing and full light/dark themes.

ShoreDrop is a native Win32 application written in Go with **no third-party
dependencies**: the SSH/SFTP and FTP/FTPS protocol code, the UI toolkit, the icon
and the installer are all in this repository. The result is a single
self-contained `ShoreDrop.exe` — users don't need Python, Node, .NET or anything
else installed.

## Install (end users)

1. Run **ShoreDrop-Setup.exe**.
2. Keep the default location (`%LOCALAPPDATA%\Programs\ShoreDrop`) or choose another, and click **Install**.
   No administrator rights are needed; ShoreDrop installs for the current user.
3. Start ShoreDrop from the Start menu or the desktop shortcut.

Uninstall from **Settings › Apps › Installed apps › ShoreDrop › Uninstall** (or run
`Uninstall ShoreDrop.exe` in the install folder). Silent install/uninstall:
`ShoreDrop-Setup.exe /S` and `"Uninstall ShoreDrop.exe" /uninstall /S`.

Because the exe isn't code-signed, Windows SmartScreen may show "Windows protected
your PC" the first time; click **More info › Run anyway**. (Signing with a code-signing
certificate removes this; see below.)

## Build on Windows

1. Install Go 1.24 or newer from https://go.dev/dl/ (needed only to build).
2. Open PowerShell in this folder and run:

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\build.ps1
   ```

3. The results are in `build\`:
   - `ShoreDrop.exe` — the app (portable; runs from anywhere)
   - `ShoreDrop-Setup.exe` — the installer (contains ShoreDrop.exe)

To build from Linux or macOS instead, run `./build.sh` (same outputs).

Optional code signing: `signtool sign /fd SHA256 /a /tr http://timestamp.digicert.com /td SHA256 build\ShoreDrop.exe`
before building the installer, then sign `build\ShoreDrop-Setup.exe` as well.

## macOS

Not built yet. The protocol, queue, settings and credential code (`internal/core`,
`internal/remote`, `internal/sshc`, `internal/sftp`, `internal/ftpc`) is portable and
already includes macOS Keychain support; a macOS version needs a native Cocoa UI
layer in place of `internal/ui` (which is Win32-specific) and an `.app` bundle.

## Features

- **Protocols:** SFTP (SSH-2: curve25519/ECDH/DH-group14 key exchange, AES-GCM/AES-CTR,
  Ed25519/ECDSA/RSA host keys, password, keyboard-interactive and key-file login with
  OpenSSH, PEM and PuTTY .ppk keys), FTPS explicit (AUTH TLS) and implicit (port 990),
  plain FTP, passive and active modes, MLSD with Unix/DOS LIST fallback, resume (REST).
- **Quick Connect** bar (host, username, password, port, protocol) — also accepts
  `sftp://`, `ftps://`, `ftp://` and `host:port`.
- **Site Manager:** sites in folders, search, new/duplicate/rename/delete, test connection,
  per-site remote/local folders and FTP mode.
- **Dual pane:** local left, remote right; name/size/date/permissions columns, sortable,
  filter box (substring or `*.txt` wildcards), back/forward/up/refresh, typed path bar,
  Windows drives view, hidden-file toggle.
- **Transfers:** drag-and-drop between panes (and from Explorer onto the remote pane),
  right-click Upload/Download, double-click, recursive folders, queue with progress,
  speed, time left, pause, resume, cancel, retry failed, parallel connections (1–10).
- **Remote actions:** rename (inline, F2), delete (recursive), new folder, chmod with
  checkboxes or octal and optional recursion, refresh, copy URL; drag onto a folder to move.
- **Synchronized browsing:** local and remote follow each other relative to the folder pair
  active when it was switched on, with in-sync / modified / only-here badges.
- **Transfer log** with protocol traffic (passwords masked).
- **Light, dark and Follow System** themes (menu, Settings, Ctrl+T), remembered between runs;
  every text colour is checked for WCAG AA contrast in `internal/ui/theme_test.go`.
- **Security:** saved passwords go to Windows Credential Manager (target
  `ShoreDrop/site/<id>`), never to a file. SSH host keys and FTPS certificates are verified
  and remembered, with warnings if they change. Plain FTP shows an "unencrypted" warning
  with a one-click switch to FTPS.

Settings live in `%APPDATA%\ShoreDrop` (`settings.json`, `sites.json`, `trust.json`).

## Keyboard shortcuts

| Keys | Action | Keys | Action |
|---|---|---|---|
| F5 / Ctrl+R | Refresh | Ctrl+S | Site Manager |
| Ctrl+U | Upload selected | Ctrl+Q | Quick Connect |
| Ctrl+D | Download selected | Ctrl+W | Disconnect |
| F2 | Rename | Ctrl+Shift+R | Reconnect |
| Del | Delete | Ctrl+Y | Synchronized browsing |
| F7 | New folder | Ctrl+P / Ctrl+Shift+P | Pause / resume all |
| Backspace, Alt+↑ | Up one level | Ctrl+T | Toggle light/dark |
| Alt+← / Alt+→ | Back / forward | Ctrl+H | Hidden files |
| Ctrl+L | Type a path | Ctrl+, | Settings |
| Ctrl+F | Filter | F1 | Shortcut list |

## Tests

```sh
go test ./internal/ui/                   # theme contrast
# protocol + queue integration tests against local servers:
python3 testsrv/sshd.py 2222 testsrv/root testsrv/keys &        # libssh-based SSH/SFTP server
python3 testsrv/ftpd.py 2121 2990 testsrv/froot ftp.crt ftp.key & # FTP/FTPS server
SD_SSH_PORT=2222 SD_KEYDIR=testsrv/keys SD_FTP_PORT=2121 SD_FTPS_IMPLICIT_PORT=2990 go test ./internal/...
```

`tools/testftpd` is a small FTP/FTPS server (127.0.0.1 only) for trying ShoreDrop on Windows:
`testftpd.exe -port 2121` then connect to `ftps://127.0.0.1:2121` as `sdtest` / `sdtest-2026`.

## Layout

```
cmd/shoredrop   app entry point (+ generated .syso resources)
cmd/setup       installer entry point (embeds payload/ShoreDrop.exe)
internal/sshc   SSH-2 client      internal/sftp  SFTP v3 client
internal/ftpc   FTP/FTPS client   internal/remote  common FS interface
internal/core   settings, sites, credentials, transfer queue, log
internal/ui     Win32 UI toolkit, windows, dialogs, installer UI
internal/logo   logo geometry + icon rasterizer
tools/mkres     icon/manifest/version-info resource generator
tools/testftpd  local FTP/FTPS test server
testsrv         integration-test servers (Python)
```
