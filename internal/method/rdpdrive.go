package method

// M39: перенаправление диска для ВСТРОЕННОГО RDP-клиента (канал rdpdr).
//
// Источник — существующее поле соединения «options», никаких новых полей конфига.
// Поддерживаются оба диалекта, которые встречаются в конфигах Ásbrú:
//
//	/drive:<share>,<path>   (xfreerdp; канон редактора Ásbrú, см. convert.go)
//	-r disk:<share>=<path>  (rdesktop: отдельным значением или слитно -rdisk:…)
//
// Разбор нарочно повторяет FreeRDP (client/common/cmdline.c, 3.32.2):
//   - значение /drive: режется по запятым, share = поле[0], path = поле[1]
//     (третье и последующие поля FreeRDP игнорирует);
//   - `\` и `/` в имени device заменяются на `__`
//     (freerdp_sanitize_drive_name), иначе имя уезжает в протокол как есть;
//   - пустые share/path и токены без разделителя отбрасываются: «полу-диск»
//     (пустой шэр/путь) серверу не объявляем.
//
// Порядок строго <share>,<path> (одно-аргументный /drive:<path> и
// swap-эвристику xfreerdp не повторяем: редактор Ásbrú всегда пишет пару).
//
// Ограничение честно: libfreerdp3 не умеет read-only drive (в RDPDR_DRIVE и
// канале drive таких флагов нет), поэтому диск отдаётся серверу с правами
// пользователя. Безопасность даёт явность: перенаправление включается ТОЛЬКО
// перечисленными в options каталогами, неявных редиректов (домашний каталог,
// «все диски», `*`/`%`) нет.

import "strings"

// RdpDrive — локальный каталог, видимый серверу как \\tsclient\<Share>.
type RdpDrive struct {
	Share string // имя device в RDPDR (уже без `\`/`/`)
	Path  string // абсолютный путь к существующему каталогу (проверяет C++)
}

// ParseRdpDrives извлекает диски из опционной строки RDP. Порядок сохраняется;
// дубликаты не схлопываются (как в легаси _parseOptionsToCfg).
func ParseRdpDrives(options string) []RdpDrive {
	tokens := SplitArgs(options)
	var out []RdpDrive
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		switch {
		case strings.HasPrefix(tok, "/drive:"):
			if d, ok := driveFromXf(strings.TrimPrefix(tok, "/drive:")); ok {
				out = append(out, d)
			}
		case tok == "-r" || strings.HasPrefix(tok, "-r"):
			// «-r disk:…» отдельным значением; «-rdisk:…» — слитно.
			val := ""
			if tok == "-r" {
				if i+1 < len(tokens) {
					val = tokens[i+1]
					i++
				}
			} else if key, glued, ok := splitRdpGlued(tok); ok && key == "-r" {
				val = glued
			}
			if d, ok := driveFromRdesktop(val); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

// driveFromXf разбирает значение после «/drive:». FreeRDP режет по запятым и
// берёт поля [0] (share) и [1] (path); остальное отбрасывается.
func driveFromXf(v string) (RdpDrive, bool) {
	parts := strings.Split(v, ",")
	if len(parts) < 2 {
		return RdpDrive{}, false
	}
	return newRdpDrive(parts[0], parts[1])
}

// driveFromRdesktop разбирает «disk:<share>=<path>» (значение -r).
func driveFromRdesktop(v string) (RdpDrive, bool) {
	if !strings.HasPrefix(v, "disk:") {
		return RdpDrive{}, false
	}
	rest := strings.TrimPrefix(v, "disk:")
	eq := strings.IndexByte(rest, '=')
	if eq < 0 {
		return RdpDrive{}, false
	}
	return newRdpDrive(rest[:eq], rest[eq+1:])
}

// newRdpDrive — общая валидация и санитайз имени. NUL в строке отбрасываем:
// C.CString обрезал бы её молча, и на сервер ушёл бы другой путь.
func newRdpDrive(share, path string) (RdpDrive, bool) {
	if share == "" || path == "" {
		return RdpDrive{}, false
	}
	if strings.IndexByte(share, 0) >= 0 || strings.IndexByte(path, 0) >= 0 {
		return RdpDrive{}, false
	}
	share = strings.NewReplacer(`\`, "__", `/`, "__").Replace(share)
	return RdpDrive{Share: share, Path: path}, true
}
