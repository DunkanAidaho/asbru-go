package ui

import (
	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// onMethodChange — логика смены типа в редакторе: обновляет метод и
// конвертирует опционную строку в диалект нового типа (фикс бага
// rdesktop→xfreerdp применяется в момент смены, а не при сохранении).
func onMethodChange(env *config.Environment, newMethod string) (options string, unknown []string) {
	if env.Method == newMethod {
		return env.Options, nil
	}
	from, ok1 := method.FromDisplay(env.Method)
	to, ok2 := method.FromDisplay(newMethod)
	if !ok1 || !ok2 {
		// неизвестный/несовместимый тип: диалект не трогаем, просто меняем имя
		env.Method = newMethod
		return env.Options, nil
	}
	converted, unknowns := method.ConvertOptions(from, to, env.Options)
	env.Method = newMethod
	env.Options = converted
	return converted, unknowns
}

// treeLabel — подпись узла дерева.
func treeLabel(env *config.Environment, isGroup bool) string {
	if isGroup {
		return env.Name + i18n.Tr("Tree", " (группа)")
	}
	return env.Name
}

// canonicalMethod — строка для combo по значению конфига: известные
// легаси-имена (rdesktop/xfreerdp/mosh и т.п.) приводятся к каноническим
// display-именам; незнакомое возвращается как есть (иначе combo сбрасывал
// бы метод в "SSH" — ревью F4).
func canonicalMethod(m string) string {
	if d, ok := method.FromDisplay(m); ok {
		return method.DisplayName(d)
	}
	// legacy-значения диспетчера (asbru_conn), которых нет в %METHODS редактора
	for _, alias := range []struct{ legacy, canonical string }{
		{"rdesktop", "RDP (rdesktop)"},
		{"xfreerdp", "RDP (xfreerdp)"},
		{"ssh", "SSH"}, {"sftp", "SFTP"}, {"telnet", "Telnet"}, {"ftp", "FTP"},
		{"cadaver", "WebDAV"}, {"mosh", "MOSH"}, {"cu", "Serial (cu)"},
		{"remote-tty", "Serial (remote-tty)"}, {"c3270", "IBM 3270/5250"},
		{"vncviewer", "VNC"}, {"generic", "Generic Command"},
		{"PACShell", "Local Shell"},
	} {
		if m == alias.legacy {
			return alias.canonical
		}
	}
	return m
}

// comboTarget — выбор строки для combo редактора: каноническое display-имя,
// если метод известен; иначе само значение (и тот должен быть добавлен
// дополнительным пунктом — F4/R4: незнакомый метод не должен исчезнуть
// и стать "SSH" при сохранении).
func comboTarget(method string) (target string, known bool) {
	target = canonicalMethod(method)
	for _, n := range methodNames {
		if n == target {
			return target, true
		}
	}
	return target, false
}
