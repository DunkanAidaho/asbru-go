# asbru-go — Ásbrú Connection Manager (Go/Qt6 rewrite)

> **Status: v0.1.0 — first public release.** This is a Go + Qt6 rewrite of
> [Ásbrú Connection Manager](https://github.com/asbru-cm/asbru-cm) (formerly PAC
> Manager). The original application is written in Perl/GTK3; this project keeps
> its configuration format and workflow while replacing the implementation.

Ásbrú Connection Manager is a GUI that helps organize remote terminal sessions
and automate repetitive tasks. asbru-go provides the same connection tree,
connection editor, terminal tabs and automation model on top of a Go core and
Qt6 widgets (via [miqt](https://github.com/mappu/miqt)).

## Features

- **Connection methods:** SSH, SFTP, Telnet, FTP, WebDAV, MOSH, serial (`cu` and
  remote-tty), IBM 3270/5250, RDP (rdesktop and xfreerdp dialects), VNC,
  Proxmox VE, generic commands and local shell.
- **Embedded RDP and VNC viewers:** FreeRDP 3 and libvncclient based, with
  fullscreen, audio (rdpsnd), clipboard file transfer and RDP drive redirection.
- **Automation:** Expect-style prompts, reconnects, jump hosts, HTTP/SOCKS
  proxies, clusters and Power Cluster Management (PCC).
- **Credentials:** legacy `asbru.yml` encryption, KeePass databases, GUI password
  and Secret Service integration.
- **Workflow:** groups/tree, connection editor, preferences, hotkeys,
  import/export, session logs, wake-on-LAN, startup commands, tray icon, panels.
- **Legacy compatibility:** reads and writes `asbru.yml` files created by
  asbru-cm (including encrypted fields); unknown keys are preserved verbatim.
- **CLI:** `asbru gui`, `asbru version`, `--config-dir`, `--debug`.

## Build

Requirements: Go 1.27+, Qt6 (Widgets), qtermwidget6, FreeRDP 3, libvncclient and
a C/C++ toolchain for cgo.

On Arch Linux:

```bash
sudo pacman -S --needed base-devel go qt6-base qtermwidget freerdp libvncserver
go build -trimpath -ldflags "-s -w" -o asbru ./cmd/asbru
```

If `/tmp` is a small tmpfs, keep the Go build temp directory on disk:

```bash
mkdir -p .gotmp
GOTMPDIR="$PWD/.gotmp" go build -trimpath -ldflags "-s -w" -o asbru ./cmd/asbru
```

## Run

```bash
./asbru gui
```

The configuration lives in `~/.config/asbru/asbru.yml`. Override it with
`ASBRU_CFG=/path/to/dir` or `--config-dir`.

## Install

An AUR package recipe is provided in [`packaging/aur/PKGBUILD`](packaging/aur/PKGBUILD):

```bash
cd packaging/aur
makepkg -si
```

It installs `/usr/bin/asbru`, the desktop entry, the application icons and the
Russian translation, and declares `conflicts`/`provides` for `asbru-cm` (the
Perl/GTK application), because both provide the `asbru` binary and desktop name.

## License and attribution

asbru-go is free software: you can redistribute it and/or modify it under the
terms of the **GNU General Public License, version 3 or later** (see
[LICENSE](LICENSE)).

This project is based on and derived from **Ásbrú Connection Manager**
([asbru-cm](https://github.com/asbru-cm/asbru-cm)):

- Copyright (C) 2017-2022 Ásbrú Connection Manager team (https://asbru-cm.net)
- Copyright (C) 2010-2016 David Torrejon Vaquerizas

Modifications for the Go/Qt6 rewrite: Copyright (C) 2026 Dunkan Aidaho.

This is a **modified work**: the user interface and session handling are
rewritten in Go with Qt6, while the `asbru.yml` configuration format and the
legacy encryption scheme are kept for compatibility. The upstream copyright
notices and GPL license are preserved; this notice is the statement of
modification required by GPLv3 §5(a). The complete corresponding source of this
version is this repository.

## Known limitations

- Some legacy features are still in progress: the PACScripts engine and parts of
  the connection menu.
- Embedded RDP/VNC is new and under live validation. External viewers
  (`xfreerdp`, `rdesktop`, `vncviewer`) remain available as fallbacks.
- The Proxmox VE integration is experimental.
