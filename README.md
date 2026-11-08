<p align="center">
  <img src="internal/ui/assets/asbru-icon-256.png" width="128" height="128" alt="Иконка asbru-go">
</p>

# asbru-go — Ásbrú Connection Manager на Go/Qt6

**Русский** | [English](README.en.md)

> **Статус: v0.1.1 — актуальный релиз.** Это переписанная на Go + Qt6
> версия [Ásbrú Connection Manager](https://github.com/asbru-cm/asbru-cm)
> (ранее PAC Manager). Оригинальная программа написана на Perl/GTK3; этот
> проект сохраняет её формат конфигурации и рабочий процесс, заменяя
> реализацию.

Ásbrú Connection Manager — графический менеджер, который помогает
упорядочивать удалённые терминальные сессии и автоматизировать повторяющиеся
задачи. asbru-go предоставляет то же дерево подключений, редактор подключений,
вкладки терминалов и модель автоматизации на основе ядра на Go и виджетов Qt6
(через [miqt](https://github.com/mappu/miqt)).

## Зачем этот проект

В Linux до сих пор нет современного менеджера подключений, дающего привычный
опыт работы с удалёнными сессиями: единое дерево, вкладки, автоматизацию и
встроенные RDP/VNC-клиенты в одном окне. Оригинальный Ásbrú Connection Manager
(asbru-cm) решает эту задачу и остаётся нашим референсом и источником
вдохновения. asbru-go — попытка дать этому опыту современную реализацию на Go
и Qt6, сохранив совместимость с конфигурацией `asbru.yml`.

## Важно: проект написан с помощью LLM

Значительная часть кода этого проекта создана большими языковыми моделями
(LLM) — иными словами, это «вайб-кодинг» (vibe coding). Код покрыт тестами,
но ошибки возможны. Если вы нашли проблему, пожалуйста, создайте issue:
<https://github.com/DunkanAidaho/asbru-go/issues>.

Первый релиз **v0.1.0** был сфокусирован на RDP: встроенный клиент на FreeRDP с
пробросом дисков, полноэкранным режимом и звуком. Текущий релиз **v0.1.1** —
документация и упаковка: бинарник `asbru-go` (устанавливается рядом с asbru-cm)
и двуязычный README. Подробности —
[в описании релиза](https://github.com/DunkanAidaho/asbru-go/releases/tag/v0.1.1).

## Возможности

- **Методы подключения:** SSH, SFTP, Telnet, FTP, WebDAV, MOSH, последовательный
  порт (`cu` и remote-tty), IBM 3270/5250, RDP (диалекты rdesktop и xfreerdp),
  VNC, Proxmox VE, произвольные команды и локальная оболочка.
- **Встроенные RDP- и VNC-клиенты:** на базе FreeRDP 3 и libvncclient, с
  полноэкранным режимом, звуком (rdpsnd), передачей файлов через буфер обмена и
  пробросом дисков в RDP.
- **Автоматизация:** Expect-подобные промпты, переподключения, jump hosts,
  HTTP/SOCKS-прокси, кластеры и управление питанием кластеров (PCC).
- **Учётные данные:** шифрование legacy `asbru.yml`, базы KeePass, пароли в GUI
  и интеграция с Secret Service.
- **Рабочий процесс:** группы/дерево, редактор подключений, настройки, горячие
  клавиши, импорт/экспорт, журналы сессий, wake-on-LAN, команды запуска, значок
  в трее, панели.
- **Совместимость:** читает и пишет `asbru.yml`, созданные asbru-cm (включая
  зашифрованные поля); неизвестные ключи сохраняются без изменений.
- **CLI:** `asbru-go gui`, `asbru-go version`, `--config-dir`, `--debug`.

## Сборка

Требования: Go 1.27+, Qt6 (Widgets), qtermwidget6, FreeRDP 3, libvncclient и
C/C++-тулчейн для cgo.

В Arch Linux:

```bash
sudo pacman -S --needed base-devel go qt6-base qtermwidget freerdp libvncserver
go build -trimpath -ldflags "-s -w" -o asbru-go ./cmd/asbru
```

Если `/tmp` — небольшой tmpfs, держите временный каталог сборки Go на диске:

```bash
mkdir -p .gotmp
GOTMPDIR="$PWD/.gotmp" go build -trimpath -ldflags "-s -w" -o asbru-go ./cmd/asbru
```

## Запуск

```bash
./asbru-go gui
```

Конфигурация хранится в `~/.config/asbru/asbru.yml`. Переопределить её можно
переменной `ASBRU_CFG=/path/to/dir` или флагом `--config-dir`.

## Установка

В репозитории есть рецепт для AUR:
[`packaging/aur/PKGBUILD`](packaging/aur/PKGBUILD):

```bash
cd packaging/aur
makepkg -si
```

Пакет устанавливает `/usr/bin/asbru-go`, ярлык рабочего стола, иконки и русский
перевод. Он не конфликтует с asbru-cm: бинарник и `.desktop` называются
`asbru-go`, поэтому обе программы можно держать установленными одновременно.
Обе программы используют общий каталог конфигурации `~/.config/asbru` (файл
`asbru.yml`), что обеспечивает совместимость настроек.

## Лицензия и авторство

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

## Известные ограничения

- Некоторые legacy-возможности ещё в работе: движок PACScripts и часть меню
  подключений.
- Встроенные RDP/VNC — новые и находятся в процессе живой проверки. Внешние
  клиенты (`xfreerdp`, `rdesktop`, `vncviewer`) остаются доступны как запасной
  вариант.
- Интеграция с Proxmox VE экспериментальная.
