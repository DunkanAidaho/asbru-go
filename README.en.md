<p align="center">
  <img src="internal/ui/assets/asbru-icon-256.png" width="128" height="128" alt="asbru-go icon">
</p>

# asbru-go — Ásbrú Connection Manager (Go/Qt6 rewrite)

[Русский](README.md) | **English**

> **Status: v0.1.1 — current release.** This is a Go + Qt6 rewrite of
> [Ásbrú Connection Manager](https://github.com/asbru-cm/asbru-cm) (formerly PAC
> Manager). The original application is written in Perl/GTK3; this project keeps
> its configuration format and workflow while replacing the implementation.

Ásbrú Connection Manager is a GUI that helps organize remote terminal sessions
and automate repetitive tasks. asbru-go provides the same connection tree,
connection editor, terminal tabs and automation model on top of a Go core and
Qt6 widgets (via [miqt](https://github.com/mappu/miqt)).

## Why this project

Linux still lacks a modern connection manager with the experience users expect
for remote sessions: a single session tree, tabs, automation and embedded
RDP/VNC clients in one window. The original Ásbrú Connection Manager (asbru-cm)
solves this problem and remains our reference and source of inspiration.
asbru-go is an attempt to give that experience a modern implementation on Go
and Qt6 while keeping compatibility with the `asbru.yml` configuration format.

## Important: this project is written with LLMs

A significant part of this project's code was written by large language models
(LLMs) — in other words, it is "vibe-coded". The code is covered by tests, but
bugs are possible. If you find a problem, please open an issue:
<https://github.com/DunkanAidaho/asbru-go/issues>.

The first release, **v0.1.0**, focused on RDP: an embedded FreeRDP client with
drive redirection, fullscreen and audio. The current **v0.1.1** release is
documentation and packaging: the `asbru-go` binary (installs alongside
asbru-cm) and a bilingual README. See the
[release notes](https://github.com/DunkanAidaho/asbru-go/releases/tag/v0.1.1)
for details.

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
- **CLI:** `asbru-go gui`, `asbru-go version`, `--config-dir`, `--debug`.

## Build

Requirements: Go 1.27+, Qt6 (Widgets), qtermwidget6, FreeRDP 3, libvncclient and
a C/C++ toolchain for cgo.

On Arch Linux:

```bash
sudo pacman -S --needed base-devel go qt6-base qtermwidget freerdp libvncserver
go build -trimpath -ldflags "-s -w" -o asbru-go ./cmd/asbru
```

If `/tmp` is a small tmpfs, keep the Go build temp directory on disk:

```bash
mkdir -p .gotmp
GOTMPDIR="$PWD/.gotmp" go build -trimpath -ldflags "-s -w" -o asbru-go ./cmd/asbru
```

## Run

```bash
./asbru-go gui
```

The configuration lives in `~/.config/asbru/asbru.yml`. Override it with
`ASBRU_CFG=/path/to/dir` or `--config-dir`.

## Install

An AUR package recipe is provided in [`packaging/aur/PKGBUILD`](packaging/aur/PKGBUILD):

```bash
cd packaging/aur
makepkg -si
```

It installs `/usr/bin/asbru-go`, the desktop entry, the application icons and
the Russian translation. The package does not conflict with asbru-cm: the
binary and the `.desktop` file are named `asbru-go`, so both applications can be
installed side by side. Both applications share the `~/.config/asbru`
configuration directory (`asbru.yml`), which keeps settings compatible.

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
