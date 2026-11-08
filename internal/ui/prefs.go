package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
)

// M19: предпочтения (Preferences-диалог) и GUI-пароль (P1-09/10 аудита).
// Вся логика — чистая; Qt-привязка в showPreferences.

// prefKind — тип виджета поля.
type prefKind int

const (
	prefText prefKind = iota
	prefCheck
	prefPass
	prefNum
	prefCombo // M35: выпадающий список (defaults.proxy селектор No/Proxy/Jump)
)

// Табы диалога (легаси PreferencesDialog: General/Regexprompts/Keepass/
// Proxy/Jump). M29: + Main/Terminal/Shell/Logs.
const (
	tabGeneral = "general"
	tabPrompts = "prompts"
	tabKeepass = "keepass"
	tabProxy   = "proxy"
	tabJump    = "jump"
	// M29:
	tabMain  = "main"
	tabTerm  = "terminal"
	tabShell = "shell"
	tabLogs  = "logs"
	// backlog №2 (M29-остаток):
	tabNet    = "network"
	tabScreen = "screenshots"
)

// prefField — одно редактируемое поле defaults. Get/Set — строка-представление.
type prefField struct {
	Tab, Key, Label string
	Kind            prefKind
	Combo           []string // prefCombo: допустимые значения (label == value)
	Get             func(*config.Defaults) string
	Set             func(*config.Defaults, string)
}

func flexStr(f config.FlexInt) string { return strconv.Itoa(int(f)) }

// rawStr — Raw-значение defaults как строка (M29: опции без типизированного поля).
func rawStr(d *config.Defaults, key string) string {
	if d == nil {
		return ""
	}
	if v, ok := d.Raw[key].(string); ok {
		return v
	}
	return ""
}

// rawInt — Raw-значение defaults как int (для check/num полей).
func rawInt(d *config.Defaults, key string) int {
	if d == nil {
		return 0
	}
	switch v := d.Raw[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// rawIntOr — rawInt с дефолтом для ОТСУТСТВУЮЩЕГО или нечислового ключа.
// Числовая строка ('1000' из YAML-кавычек) понимается как число; пустая и
// нечисловая дают дефолт (rawInt вернул бы 0 — и «ключа нет» превратилось бы в
// «без истории»). Единственная такая функция в пакете: вторая с другой
// семантикой однажды уже разошлась с этой (ревью M44, остаток №1).
func rawIntOr(d *config.Defaults, key string, def int) int {
	if d == nil {
		return def
	}
	v, ok := d.Raw[key]
	if !ok {
		return def
	}
	if s, isStr := v.(string); isStr {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return def
		}
		return n
	}
	return rawInt(d, key)
}

// rawSet — запись в Raw с инициализацией карты (M29).
func rawSet(d *config.Defaults, key string, val any) {
	if d.Raw == nil {
		d.Raw = map[string]any{}
	}
	d.Raw[key] = val
}

// valueFrac — произвольное Raw-значение как 0-1 доля (M29-ревью I1:
// 'terminal transparency' в конфиге/сиде — доля 0.3, а не проценты).
func valueFrac(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f
	}
	return 0
}

// fracVal — Raw-значение defaults-ключа как 0-1 доля.
func fracVal(d *config.Defaults, key string) float64 {
	if d == nil {
		return 0
	}
	return valueFrac(d.Raw[key])
}

// fracToPercent — 0-1 доля → проценты (0-100), с клампом границ.
func fracToPercent(f float64) int {
	p := int(math.Round(f * 100))
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}

// percentToFrac — строка процентов → 0-1 доля; мусор/пустая → prev.
func percentToFrac(s string, prev float64) float64 {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return prev
	}
	if n < 0 {
		n = 0
	}
	if n > 100 {
		n = 100
	}
	return float64(n) / 100.0
}

// prefFields — реестр полей предпочтений (покрытие — TestPrefFieldsCoverage).
func prefFields() []prefField {
	f := func(tab, key, label string, kind prefKind,
		get func(*config.Defaults) string, set func(*config.Defaults, string)) prefField {
		return prefField{Tab: tab, Key: key, Label: label, Kind: kind, Get: get, Set: set}
	}
	// fc — prefCombo-поле: значения списка (label == value).
	fc := func(tab, key, label string, combo []string,
		get func(*config.Defaults) string, set func(*config.Defaults, string)) prefField {
		return prefField{Tab: tab, Key: key, Label: label, Kind: prefCombo, Combo: combo, Get: get, Set: set}
	}
	return []prefField{
		f(tabGeneral, "use gui password", i18n.Tr("Prefs", "Защищать GUI паролем"), prefCheck,
			func(d *config.Defaults) string { return flexStr(d.UseGUIPassword) },
			func(d *config.Defaults, v string) { d.UseGUIPassword = config.FlexInt(atoiOr(v, 0)) }),
		f(tabGeneral, "use gui password tray", i18n.Tr("Prefs", "Требовать пароль при восстановлении из трея"), prefCheck,
			func(d *config.Defaults) string { return flexStr(d.UseGUIPasswordTray) },
			func(d *config.Defaults, v string) { d.UseGUIPasswordTray = config.FlexInt(atoiOr(v, 0)) }),
		f(tabGeneral, "auto accept key", i18n.Tr("Prefs", "Принимать незнакомые SSH-ключи автоматически"), prefCheck,
			func(d *config.Defaults) string { return flexStr(d.AutoAcceptKey) },
			func(d *config.Defaults, v string) { d.AutoAcceptKey = config.FlexInt(atoiOr(v, 0)) }),
		f(tabGeneral, "timeout connect", i18n.Tr("Prefs", "Таймаут подключения (сек)"), prefNum,
			func(d *config.Defaults) string { return flexStr(d.TimeOutConnect) },
			func(d *config.Defaults, v string) {
				d.TimeOutConnect = config.FlexInt(atoiOr(v, int(d.TimeOutConnect)))
			}),
		f(tabGeneral, "sudo show password", i18n.Tr("Prefs", "Показывать пароль sudo"), prefCheck,
			func(d *config.Defaults) string { return flexStr(d.SudoShowPassword) },
			func(d *config.Defaults, v string) { d.SudoShowPassword = config.FlexInt(atoiOr(v, 0)) }),
		f(tabGeneral, "sudo password", i18n.Tr("Prefs", "Пароль sudo"), prefPass,
			func(d *config.Defaults) string { return d.SudoPassword },
			func(d *config.Defaults, v string) { d.SudoPassword = v }),
		f(tabGeneral, "timeout command", i18n.Tr("Prefs", "Таймаут команды (сек)"), prefNum,
			func(d *config.Defaults) string { return flexStr(d.TimeOutCommand) },
			func(d *config.Defaults, v string) {
				d.TimeOutCommand = config.FlexInt(atoiOr(v, int(d.TimeOutCommand)))
			}),
		// бэклог «статус-бар терминала»: defaults 'terminal show status bar' (Raw).
		f(tabGeneral, "terminal show status bar", i18n.Tr("Prefs", "Показывать строку состояния терминала"), prefCheck,
			func(d *config.Defaults) string {
				if d.ShowStatusBar() {
					return "1"
				}
				return ""
			},
			func(d *config.Defaults, v string) {
				if d.Raw == nil {
					d.Raw = map[string]any{}
				}
				d.Raw["terminal show status bar"] = atoiOr(v, 0)
			}),
		// NOTE: seed-ключи defaults 'debug' (Expect DEBUG, security-adjacent) и
		// 'only one instance' сюда НЕ регистрируем: в Go-порте они пока не
		// потребляются, а 'only one instance' противоречит безусловному
		// single-instance (flock). Регистрация + привязка — в фазе встраивания
		// поведения (ревью M29-остаток).

		f(tabPrompts, "command prompt", i18n.Tr("Prefs", "Приглашение команды"), prefText,
			func(d *config.Defaults) string { return d.CommandPrompt },
			func(d *config.Defaults, v string) { d.CommandPrompt = v }),
		f(tabPrompts, "username prompt", i18n.Tr("Prefs", "Приглашение пользователя"), prefText,
			func(d *config.Defaults) string { return d.UsernamePrompt },
			func(d *config.Defaults, v string) { d.UsernamePrompt = v }),
		f(tabPrompts, "password prompt", i18n.Tr("Prefs", "Приглашение пароля"), prefText,
			func(d *config.Defaults) string { return d.PasswordPrompt },
			func(d *config.Defaults, v string) { d.PasswordPrompt = v }),
		f(tabPrompts, "hostkey changed prompt", i18n.Tr("Prefs", "Изменённый ключ хоста"), prefText,
			func(d *config.Defaults) string { return d.HostkeyChangedPrompt },
			func(d *config.Defaults, v string) { d.HostkeyChangedPrompt = v }),
		f(tabPrompts, "press any key prompt", i18n.Tr("Prefs", "«Нажмите любую клавишу»"), prefText,
			func(d *config.Defaults) string { return d.PressAnyKeyPrompt },
			func(d *config.Defaults, v string) { d.PressAnyKeyPrompt = v }),
		f(tabPrompts, "remote host changed prompt", i18n.Tr("Prefs", "Смена удалённого хоста"), prefText,
			func(d *config.Defaults) string { return d.RemoteHostChangedPrompt },
			func(d *config.Defaults, v string) { d.RemoteHostChangedPrompt = v }),
		f(tabPrompts, "sudo prompt", i18n.Tr("Prefs", "Приглашение sudo"), prefText,
			func(d *config.Defaults) string { return d.SudoPrompt },
			func(d *config.Defaults, v string) { d.SudoPrompt = v }),

		f(tabKeepass, "use_keepass", i18n.Tr("Prefs", "Использовать KeePass"), prefCheck,
			func(d *config.Defaults) string { return flexStr(d.Keepass.UseKeepass) },
			func(d *config.Defaults, v string) { d.Keepass.UseKeepass = config.FlexInt(atoiOr(v, 0)) }),
		f(tabKeepass, "database", i18n.Tr("Prefs", "База данных"), prefText,
			func(d *config.Defaults) string { return d.Keepass.Database },
			func(d *config.Defaults, v string) { d.Keepass.Database = v }),
		f(tabKeepass, "pathcli", i18n.Tr("Prefs", "Путь к keepassxc-cli (устарел: нативный доступ)"), prefText,
			func(d *config.Defaults) string { return d.Keepass.PathCLI },
			func(d *config.Defaults, v string) { d.Keepass.PathCLI = v }),
		f(tabKeepass, "keyfile", i18n.Tr("Prefs", "Файл ключа"), prefText,
			func(d *config.Defaults) string { return d.Keepass.KeyFile },
			func(d *config.Defaults, v string) { d.Keepass.KeyFile = v }),
		f(tabKeepass, "password", i18n.Tr("Prefs", "Пароль базы"), prefPass,
			func(d *config.Defaults) string { return d.Keepass.Password },
			func(d *config.Defaults, v string) { d.Keepass.Password = v }),

		fc(tabProxy, "proxy", i18n.Tr("Prefs", "Прокси"), []string{"No", "Proxy", "Jump"},
			func(d *config.Defaults) string { return d.Proxy },
			func(d *config.Defaults, v string) { d.Proxy = v }),
		f(tabProxy, "proxy ip", i18n.Tr("Prefs", "IP прокси"), prefText,
			func(d *config.Defaults) string { return d.ProxyIP },
			func(d *config.Defaults, v string) { d.ProxyIP = v }),
		f(tabProxy, "proxy port", i18n.Tr("Prefs", "Порт прокси"), prefNum,
			func(d *config.Defaults) string { return d.ProxyPort },
			func(d *config.Defaults, v string) {
				if v == "" {
					d.ProxyPort = ""
					return
				}
				d.ProxyPort = strconv.Itoa(atoiOr(v, atoiOr(d.ProxyPort, 0)))
			}),
		f(tabProxy, "proxy user", i18n.Tr("Prefs", "Пользователь прокси"), prefText,
			func(d *config.Defaults) string { return d.ProxyUser },
			func(d *config.Defaults, v string) { d.ProxyUser = v }),
		f(tabProxy, "proxy pass", i18n.Tr("Prefs", "Пароль прокси"), prefPass,
			func(d *config.Defaults) string { return d.ProxyPass },
			func(d *config.Defaults, v string) { d.ProxyPass = v }),
		// M29-остаток: use system proxy (seed-ключ defaults, регистрация).
		f(tabProxy, "use system proxy", i18n.Tr("Prefs", "Использовать системный прокси"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "use system proxy")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "use system proxy", atoiOr(v, 0)) }),

		f(tabJump, "jump ip", i18n.Tr("Prefs", "IP jump-хоста"), prefText,
			func(d *config.Defaults) string { return d.JumpIP },
			func(d *config.Defaults, v string) { d.JumpIP = v }),
		f(tabJump, "jump port", i18n.Tr("Prefs", "Порт jump-хоста"), prefNum,
			func(d *config.Defaults) string { return flexStr(d.JumpPort) },
			func(d *config.Defaults, v string) { d.JumpPort = config.FlexInt(atoiOr(v, int(d.JumpPort))) }),
		f(tabJump, "jump user", i18n.Tr("Prefs", "Пользователь jump-хоста"), prefText,
			func(d *config.Defaults) string { return d.JumpUser },
			func(d *config.Defaults, v string) { d.JumpUser = v }),
		f(tabJump, "jump pass", i18n.Tr("Prefs", "Пароль jump-хоста"), prefPass,
			func(d *config.Defaults) string { return d.JumpPass },
			func(d *config.Defaults, v string) { d.JumpPass = v }),
		f(tabJump, "jump key", i18n.Tr("Prefs", "Ключ jump-хоста"), prefText,
			func(d *config.Defaults) string { return d.JumpKey },
			func(d *config.Defaults, v string) { d.JumpKey = v }),

		// M29: окно/вкладки (легаси MainOptions.md, res/asbru.yml).
		f(tabMain, "close to tray", i18n.Tr("Prefs", "Сворачивать в трей вместо закрытия"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "close to tray"))) },
			func(d *config.Defaults, v string) { rawSet(d, "close to tray", atoiOr(v, 0)) }),
		f(tabMain, "hide on connect", i18n.Tr("Prefs", "Прятать окно при подключении"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "hide on connect"))) },
			func(d *config.Defaults, v string) { rawSet(d, "hide on connect", atoiOr(v, 0)) }),
		f(tabMain, "start iconified", i18n.Tr("Prefs", "Запускать свёрнутым в трей"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "start iconified"))) },
			func(d *config.Defaults, v string) { rawSet(d, "start iconified", atoiOr(v, 0)) }),
		f(tabMain, "confirm exit", i18n.Tr("Prefs", "Подтверждать выход"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "confirm exit"))) },
			func(d *config.Defaults, v string) { rawSet(d, "confirm exit", atoiOr(v, 0)) }),
		f(tabMain, "auto save on exit", i18n.Tr("Prefs", "Автосохранение при выходе"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "auto save on exit"))) },
			func(d *config.Defaults, v string) { rawSet(d, "auto save on exit", atoiOr(v, 0)) }),
		f(tabMain, "open connections in tabs", i18n.Tr("Prefs", "Открывать соединения вкладками"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "open connections in tabs"))) },
			func(d *config.Defaults, v string) { rawSet(d, "open connections in tabs", atoiOr(v, 0)) }),
		f(tabMain, "tabs in main window", i18n.Tr("Prefs", "Вкладки внутри главного окна"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "tabs in main window"))) },
			func(d *config.Defaults, v string) { rawSet(d, "tabs in main window", atoiOr(v, 0)) }),
		f(tabMain, "save on exit", i18n.Tr("Prefs", "Сохранять конфиг при выходе"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "save on exit"))) },
			func(d *config.Defaults, v string) { rawSet(d, "save on exit", atoiOr(v, 0)) }),
		// M29-остаток: seed-ключи defaults, ещё не заведённые в реестр.
		// Регистрация (конфиг/ядро); привязка поведения — в UI-фазе.
		f(tabMain, "connected color", i18n.Tr("Prefs", "Цвет подключённого узла"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "connected color") },
			func(d *config.Defaults, v string) { rawSet(d, "connected color", v) }),
		f(tabMain, "disconnected color", i18n.Tr("Prefs", "Цвет отключённого узла"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "disconnected color") },
			func(d *config.Defaults, v string) { rawSet(d, "disconnected color", v) }),
		f(tabMain, "new data color", i18n.Tr("Prefs", "Цвет новых данных"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "new data color") },
			func(d *config.Defaults, v string) { rawSet(d, "new data color", v) }),
		f(tabMain, "start maximized", i18n.Tr("Prefs", "Запускать окно развёрнутым"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "start maximized"))) },
			func(d *config.Defaults, v string) { rawSet(d, "start maximized", atoiOr(v, 0)) }),
		f(tabMain, "start main maximized", i18n.Tr("Prefs", "Запускать главное окно развёрнутым"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "start main maximized")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "start main maximized", atoiOr(v, 0)) }),

		// M29: терминал (легаси TermLookAndFeel/TermOptions, res/asbru.yml).
		f(tabTerm, "terminal font", i18n.Tr("Prefs", "Шрифт терминала"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "terminal font") },
			func(d *config.Defaults, v string) { rawSet(d, "terminal font", v) }),
		f(tabTerm, "text color", i18n.Tr("Prefs", "Цвет текста"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "text color") },
			func(d *config.Defaults, v string) { rawSet(d, "text color", v) }),
		f(tabTerm, "back color", i18n.Tr("Prefs", "Цвет фона"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "back color") },
			func(d *config.Defaults, v string) { rawSet(d, "back color", v) }),
		f(tabTerm, "cursor shape", i18n.Tr("Prefs", "Форма курсора"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "cursor shape") },
			func(d *config.Defaults, v string) { rawSet(d, "cursor shape", v) }),
		// M44-ревью MAJOR-1: отсутствие ключа — это НЕ «без истории». Показываем и
		// записываем тот же дефолт, что применяет рантайм (defaultScrollbackLines):
		// иначе открыть настройки и нажать «Сохранить» означало молча выключить
		// историю терминала (а с ней — прокрутку колесом).
		f(tabTerm, "terminal scrollback lines", i18n.Tr("Prefs", "Строк истории"), prefNum,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawIntOr(d, "terminal scrollback lines", defaultScrollbackLines)))
			},
			func(d *config.Defaults, v string) {
				rawSet(d, "terminal scrollback lines", atoiOr(v, defaultScrollbackLines))
			}),
		f(tabTerm, "terminal transparency", i18n.Tr("Prefs", "Прозрачность (%)"), prefNum,
			func(d *config.Defaults) string {
				return strconv.Itoa(fracToPercent(fracVal(d, "terminal transparency")))
			},
			func(d *config.Defaults, v string) {
				rawSet(d, "terminal transparency", percentToFrac(v, fracVal(d, "terminal transparency")))
			}),
		f(tabTerm, "terminal windows hsize", i18n.Tr("Prefs", "Ширина окна терминала"), prefNum,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "terminal windows hsize"))) },
			func(d *config.Defaults, v string) {
				rawSet(d, "terminal windows hsize", atoiOr(v, rawInt(d, "terminal windows hsize")))
			}),
		f(tabTerm, "terminal windows vsize", i18n.Tr("Prefs", "Высота окна терминала"), prefNum,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "terminal windows vsize"))) },
			func(d *config.Defaults, v string) {
				rawSet(d, "terminal windows vsize", atoiOr(v, rawInt(d, "terminal windows vsize")))
			}),
		// M29-остаток: автопереподключение + история команд (seed-ключи).
		f(tabTerm, "intro reconnects", i18n.Tr("Prefs", "Автопереподключение при разрыве (раз)"), prefNum,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "intro reconnects"))) },
			func(d *config.Defaults, v string) {
				rawSet(d, "intro reconnects", atoiOr(v, rawInt(d, "intro reconnects")))
			}),
		f(tabTerm, "record command history", i18n.Tr("Prefs", "Запоминать историю команд"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "record command history")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "record command history", atoiOr(v, 0)) }),
		// #4: панель макросов терминала (легаси 'show commands box'/'show global
		// commands box'). Комбо хранит строку-метку, macroBarMode парсит её.
		// Ревью MINOR: Get маппит int/string-режим → метку (rawStr не умел читать
		// int-значение → любой save затирал конфиг в "Never").
		fc(tabTerm, "show commands box", i18n.Tr("Prefs", "Панель макросов"),
			[]string{"Never", "Combobox", "Buttons"},
			func(d *config.Defaults) string {
				switch macroBarMode(d) {
				case 1:
					return "Combobox"
				case 2:
					return "Buttons"
				}
				return "Never"
			},
			func(d *config.Defaults, v string) { rawSet(d, "show commands box", v) }),
		f(tabTerm, "show global commands box", i18n.Tr("Prefs", "Показывать глобальные команды"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "show global commands box")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "show global commands box", atoiOr(v, 0)) }),

		// M29: shell (легаси ShellOptions).
		f(tabShell, "shell binary", i18n.Tr("Prefs", "Shell"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "shell binary") },
			func(d *config.Defaults, v string) { rawSet(d, "shell binary", v) }),
		f(tabShell, "shell options", i18n.Tr("Prefs", "Опции shell"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "shell options") },
			func(d *config.Defaults, v string) { rawSet(d, "shell options", v) }),

		// M29: логи (легаси TermOptions «Auto save Session logs»).
		f(tabLogs, "save session logs", i18n.Tr("Prefs", "Сохранять логи сессий"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "save session logs"))) },
			func(d *config.Defaults, v string) { rawSet(d, "save session logs", atoiOr(v, 0)) }),
		f(tabLogs, "session logs amount", i18n.Tr("Prefs", "Кол-во логов"), prefNum,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "session logs amount"))) },
			func(d *config.Defaults, v string) {
				rawSet(d, "session logs amount", atoiOr(v, rawInt(d, "session logs amount")))
			}),
		f(tabLogs, "session logs folder", i18n.Tr("Prefs", "Каталог логов"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "session logs folder") },
			func(d *config.Defaults, v string) { rawSet(d, "session logs folder", v) }),

		// backlog №2 (M29-остаток): seed-ключи в окно/вкладки.
		f(tabMain, "close terminal on disconnect", i18n.Tr("Prefs", "Закрывать терминал при отключении"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "close terminal on disconnect")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "close terminal on disconnect", atoiOr(v, 0)) }),
		f(tabMain, "tabs position", i18n.Tr("Prefs", "Позиция вкладок"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "tabs position") },
			func(d *config.Defaults, v string) { rawSet(d, "tabs position", v) }),
		f(tabMain, "use bw icon", i18n.Tr("Prefs", "Чёрно-белые иконки"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "use bw icon"))) },
			func(d *config.Defaults, v string) { rawSet(d, "use bw icon", atoiOr(v, 0)) }),
		f(tabMain, "auto hide connections list", i18n.Tr("Prefs", "Автоскрывать список соединений"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "auto hide connections list")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "auto hide connections list", atoiOr(v, 0)) }),
		f(tabMain, "minimize to tray", i18n.Tr("Prefs", "Сворачивать в трей"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "minimize to tray"))) },
			func(d *config.Defaults, v string) { rawSet(d, "minimize to tray", atoiOr(v, 0)) }),
		f(tabMain, "force split tabs to 50%", i18n.Tr("Prefs", "Делить вкладки пополам"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "force split tabs to 50%"))) },
			func(d *config.Defaults, v string) { rawSet(d, "force split tabs to 50%", atoiOr(v, 0)) }),

		// backlog №2: сеть (check port status).
		f(tabNet, "ping port before connect", i18n.Tr("Prefs", "Проверять порт перед подключением"), prefCheck,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "ping port before connect"))) },
			func(d *config.Defaults, v string) { rawSet(d, "ping port before connect", atoiOr(v, 0)) }),
		f(tabNet, "ping port timeout", i18n.Tr("Prefs", "Таймаут проверки порта (сек)"), prefNum,
			func(d *config.Defaults) string { return flexStr(config.FlexInt(rawInt(d, "ping port timeout"))) },
			func(d *config.Defaults, v string) {
				rawSet(d, "ping port timeout", atoiOr(v, rawInt(d, "ping port timeout")))
			}),

		// backlog №2: скриншоты-просмотр (внешний вьювер).
		f(tabScreen, "screenshots use external viewer", i18n.Tr("Prefs", "Внешний просмотр скриншотов"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "screenshots use external viewer")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "screenshots use external viewer", atoiOr(v, 0)) }),
		f(tabScreen, "screenshots external viewer", i18n.Tr("Prefs", "Команда внешнего просмотрщика"), prefText,
			func(d *config.Defaults) string { return rawStr(d, "screenshots external viewer") },
			func(d *config.Defaults, v string) { rawSet(d, "screenshots external viewer", v) }),
		// M29-остаток: show screenshots (seed-ключ defaults, регистрация).
		f(tabScreen, "show screenshots", i18n.Tr("Prefs", "Показывать скриншоты"), prefCheck,
			func(d *config.Defaults) string {
				return flexStr(config.FlexInt(rawInt(d, "show screenshots")))
			},
			func(d *config.Defaults, v string) { rawSet(d, "show screenshots", atoiOr(v, 0)) }),
	}
}

// guiPassTrayRequired — легаси PACMain.pm:2214/2347: восстановление из
// трея требует пароль только при use gui password && use gui password tray.
func guiPassTrayRequired(d *config.Defaults) bool {
	return d.UseGUIPassword == 1 && d.UseGUIPasswordTray == 1
}

// guiPassCheck — проверка GUI-пароля: легаси
// $pass ne decrypt_hex(defaults 'gui password') (PACMain.pm:256).
func guiPassCheck(d *config.Defaults, given string) bool {
	if d.UseGUIPassword != 1 {
		return true
	}
	return config.NewCBC().Decrypt(d.GUIPassword) == given
}

// guiPassSet — смена GUI-пароля: encrypt_hex(новый) в defaults (PACUtils.pm:2035).
func guiPassSet(d *config.Defaults, current, next string) error {
	if d.UseGUIPassword == 1 && config.NewCBC().Decrypt(d.GUIPassword) != current {
		return fmt.Errorf("текущий пароль неверен")
	}
	d.GUIPassword = config.NewCBC().Encrypt(next)
	return nil
}

// applyPrefs — применение значений диалога к defaults.
func applyPrefs(d *config.Defaults, vals map[string]string) error {
	for _, f := range prefFields() {
		if v, ok := vals[f.Key]; ok {
			f.Set(d, v)
		}
	}
	return nil
}

// comboIndex — индекс значения в списке; пустое/неизвестное → 0 (первый пункт).
func comboIndex(items []string, val string) int {
	for i, c := range items {
		if c == val {
			return i
		}
	}
	return 0
}
