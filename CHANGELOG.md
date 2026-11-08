# Changelog

## v0.1.1 — 2026-11-08

Documentation and packaging release. No functional code changes.

### Changed

- Documentation: bilingual README — Russian by default (`README.md`), English
  translation (`README.en.md`); add icon header, project motivation and the
  LLM/vibe-coding disclaimer.
- Packaging: the installed binary is now `asbru-go` (instead of `asbru`), so the
  package can coexist with `asbru-cm`; the desktop entry uses
  `Exec=asbru-go gui` and `StartupWMClass=asbru-go`.
- Packaging: drop `conflicts`/`provides` for `asbru-cm`; regenerate `.SRCINFO`.
- Version: `asbru-go version` now reports `0.1.1`.

## v0.1.0 — 2026-11-08

First public release of the Go/Qt6 rewrite of Ásbrú Connection Manager.

### Added

- Go + Qt6 (miqt) implementation of the connection manager: tree, connection
  editor, terminal tabs, preferences, tray icon and panels.
- Connection methods: SSH, SFTP, Telnet, FTP, WebDAV, MOSH, serial (`cu`,
  remote-tty), IBM 3270/5250, RDP (rdesktop/xfreerdp), VNC, Proxmox VE, generic
  commands and local shell.
- Embedded RDP (FreeRDP 3) and VNC (libvncclient) viewers with fullscreen,
  audio, clipboard file transfer and drive redirection.
- Expect automation, reconnects, jump hosts, proxies, clusters/PCC.
- KeePass, GUI password and Secret Service credential integration.
- Legacy `asbru.yml` compatibility, including the original encryption scheme;
  unknown configuration keys are preserved.
- CLI: `asbru gui`, `asbru version`, `--config-dir`, `--debug`.
- Packaging: AUR PKGBUILD (`asbru-go`), desktop entry, icon set and Russian
  translation.

### Known limitations

- PACScripts engine and parts of the connection menu are still in progress.
- Embedded RDP/VNC is new and under live validation.
- Proxmox VE integration is experimental.
