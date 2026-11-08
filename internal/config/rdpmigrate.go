package config

// user-тест #3: разовая миграция опций RDP — привести к диалекту метода.
// Если пользователь сменил rdesktop↔xfreerdp (или метод мигрировал), а опции
// остались от старого диалекта — автоматически конвертируем (method.ConvertOptions).
// Неопределённый диалект/не-токены не трогаем; не-мапируемые оставляем (лог).

import (
	"log/slog"

	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// migrateRdpOptions — конвертация опций соединения под его метод (rdesktop/xfreerdp).
func migrateRdpOptions(env *Environment) {
	m := env.Method
	rd := m == "RDP (rdesktop)" || m == "rdesktop"
	xf := m == "RDP (xfreerdp)" || m == "xfreerdp"
	if (!rd && !xf) || env.Options == "" {
		return
	}
	d := method.DialectOf(env.Options)
	var from, to method.Dialect
	switch {
	case xf && d == method.RDesktop:
		from, to = method.RDesktop, method.XFreeRDP
	case rd && d == method.XFreeRDP:
		from, to = method.XFreeRDP, method.RDesktop
	default:
		return // диалект совпадает или неопределён — не трогаем
	}
	converted, unknown := method.ConvertOptions(from, to, env.Options)
	// Пара диалектов применима (switch выше), поэтому пустой результат — это
	// «все токены намеренно дропнуты» (напр. xfreerdp + "-4"), а не
	// «не применимо». Иначе старый токен навсегда застревал в опциях.
	env.Options = converted
	if len(unknown) > 0 {
		// Не-мапируемые/неизвестные ключи оставляем (не молчим); интерактивный
		// выбор «заменить/оставить» — в редакторе при смене типа RDP.
		slog.Warn("RDP-опции: необработанные ключи", "unknown", unknown, "from", from, "to", to)
	}
}
