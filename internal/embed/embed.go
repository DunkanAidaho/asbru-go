// Package embed — параметры встраиваемых RDP/VNC-клиентов (M33).
//
// Функция этого пакета — собрать из config.Environment (+ опции соединения) и
// method.MapRdpOptions типизированный набор настроек, который отдаётся cgo-виджету
// (internal/ui/wgt/rdpview, internal/ui/wgt/vncview) для подключения к серверу и
// рендера фреймбуфера Qt-поверхностью ВНУТРИ вкладки. Пакет чистый (без cgo/Qt),
// поэтому маппинг покрывается обычными юнит-тестами.
//
// Отделено от method: method остаётся standalone (диалекты/argv/опции RDP),
// здесь — сведение окружения соединения к конкретным значениям виджета.
package embed

import (
	"slices"
	"strconv"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// RDPParams — разрешённые настройки встроенного RDP-клиента (libfreerdp3).
type RDPParams struct {
	Host     string
	Port     int // дефолт 3389
	Username string
	Domain   string
	Password string
	Width    int // 0 — виджет/сервер выбирают размер
	Height   int
	BPP      int // цветовая глубина (дефолт 16)
	// Fullscreen — «-f»/«/f»/«/fullscreen». Встроенный клиент разворачивает
	// вкладку на весь экран (M39): главное окно уходит в ShowFullScreen, боковая
	// панель и полоса вкладок прячутся, канвас занимает экран; Esc возвращает
	// вкладку к обычному размеру. Для ВНЕШНИХ методов RDP токен по-прежнему
	// уходит в argv клиента.
	Fullscreen bool
	Clipboard  bool   // «-C»/«+clipboard»/«/clipboard»
	Kbd        string // «-k»/«/kbd:» (раскладка)
	// Shell — «-s»/«/shell:». ПАРСИТСЯ, но до настроек libfreerdp3 не доезжает:
	// C-мост rdp_start не имеет параметра shell (BACKLOG.md). Для ВНЕШНИХ
	// методов RDP токен по-прежнему уходит в argv клиента.
	Shell   string
	Admin   bool     // «-0»/«/admin»
	Unknown []string // не-мапируемые токены — для UI-предупреждения (M33-г)
	// M37: галки безопасности/производительности — теперь ДОЕЗЖАЮТ до
	// libfreerdp3 (раньше опции игнорировались, и галки в редакторе врали).
	CertIgnore        bool
	NoNLA             bool // «-sec-nla»: отключить NLA
	NoTLS             bool // «-sec-tls»: отключить TLS
	NoRDP             bool // «-sec-rdp»: отключить стандартное RDP-шифрование
	NoAuth            bool // «-authentication»
	NoFastPath        bool // «-fast-path»
	RFX               bool // «/rfx»
	NSC               bool // «/nsc»
	DynamicResolution bool // «/dynamic-resolution»
	FontSmooth        bool // «+fonts»
	TLSSecLevel       int  // «/tls-seclevel:N», 0 — не задано
	// M39: перенаправление локальных каталогов (канал rdpdr). Источник —
	// существующее поле options: xfreerdp `/drive:share,path` или rdesktop
	// `-r disk:share=path`. Пусто — серверу не отдаём ни одного диска
	// (никаких неявных редиректов). libfreerdp3 не умеет read-only диск:
	// сервер получает доступ с правами пользователя.
	Drives []method.RdpDrive
	// M39: звук (канал rdpsnd). Источник — то же поле options (xfreerdp
	// `/sound:…`, rdesktop `-r sound:…`). ChannelArg пуст, если токена нет или
	// звук выключен/остаётся на сервере: встроенный клиент тогда оставляет
	// дефолтную подсистему FreeRDP sys:fake (без звука) — легаси-паритет.
	Sound method.RdpSound
}

// Битмаска настроек безопасности для встроенного клиента. Значения обязаны
// совпадать с rdp_flags_* в internal/ui/wgt/rdpview/bridge.h.
const (
	FlagCertIgnore uint32 = 1 << iota
	FlagNoNLA
	FlagNoTLS
	FlagNoRDP
	FlagNoAuth
	FlagNoFastPath
	FlagRFX
	FlagNSC
	FlagDynamicResolution
	FlagFontSmooth
	FlagAdmin
)

// Flags — битмаска для rdp_start (см. bridge.h).
func (p RDPParams) Flags() uint32 {
	var f uint32
	set := func(bit uint32, on bool) {
		if on {
			f |= bit
		}
	}
	set(FlagCertIgnore, p.CertIgnore)
	set(FlagNoNLA, p.NoNLA)
	set(FlagNoTLS, p.NoTLS)
	set(FlagNoRDP, p.NoRDP)
	set(FlagNoAuth, p.NoAuth)
	set(FlagNoFastPath, p.NoFastPath)
	set(FlagRFX, p.RFX)
	set(FlagNSC, p.NSC)
	set(FlagDynamicResolution, p.DynamicResolution)
	set(FlagFontSmooth, p.FontSmooth)
	set(FlagAdmin, p.Admin)
	return f
}

// VNCParams — разрешённые настройки встроенного VNC-клиента (libvncclient).
type VNCParams struct {
	Host     string
	Port     int // дефолт 5900
	Password string
	Username string
	Scale    int // масштаб (дефолт 1)
	// Fullscreen — «-fullscreen» (vncviewer/RealVNC) или «-FullScreen=N» N>0
	// (TigerVNC) из строки options: встроенный клиент разворачивает вкладку на
	// весь экран (M39). Прежние встроенные VNC-клиенты эти токены игнорировали.
	Fullscreen bool
	// Security — VeNCrypt-режим встроенного клиента: "" / "auto" (дефолт,
	// clientAuthSchemes=NULL — клиент берёт первый приемлемый сервис-тип
	// из списка сервера, включая VeNCrypt, без отказа от VncAuth),
	// "vencrypt" (форс VeNCrypt: только rfbVeNCrypt на верхнем уровне, сервер
	// без шифра не подключится — даунгрейд на верхнем уровне закрыт. ВАЖНО:
	// SetClientAuthSchemes НЕ гейтит внутренний выбор под-типа VeNCrypt; если
	// сервер предложил VeNCrypt, но только без TLS-подтипов, клиент обязан
	// отсечь соединение (fail-closed) — иначе VNC-пароль ушёл бы в plaintext),
	// "none" (запретить VeNCrypt → только VncAuth/NoAuth).
	Security string
}

// rdpDefaultPort — легаси-дефолт порта RDP (rdesktop/xfreerdp, /v:host:port).
const rdpDefaultPort = 3389

// vncDefaultPort — легаси-дефолт порта VNC (vncviewer host:port).
const vncDefaultPort = 5900

// RDPParamsFrom собирает RDPParams из окружения соединения и опций RDP.
// options проходит через method.MapRdpOptions: известные ключи → настройки,
// неизвестные/устаревшие → Unknown (НЕ падаем; UI покажет предупреждение).
func RDPParamsFrom(e *config.Environment, options string) RDPParams {
	p := RDPParams{
		Host:     e.IP,
		Port:     portOr(e.Port, rdpDefaultPort),
		Username: e.User,
		Password: e.Pass,
		BPP:      16,
	}
	// RDP-креды: в поле user может быть «DOMAIN\user» — домен шлём отдельным полем,
	// иначе freerdp теряет обратный слеш и логинится как «DOMAINuser» (один login)
	// → LOGON_FAILURE (очный дебаг: «Remmina работает, asbru — Logon failed»).
	// Явная опция /d:-d ниже перекрывает split.
	if i := strings.IndexByte(e.User, '\\'); i >= 0 {
		p.Domain = e.User[:i]
		p.Username = e.User[i+1:]
	}
	settings, unknown := method.MapRdpOptions(options)
	p.Unknown = unknown
	// M39: диски из того же поля options (поддерживаются оба диалекта).
	p.Drives = method.ParseRdpDrives(options)
	// M39: звук из того же поля options; без токена остаётся выключенным.
	p.Sound = method.ParseRdpSound(options)
	if v := settings["domain"]; v != "" {
		p.Domain = v
	}
	if v := settings["kbd"]; v != "" {
		p.Kbd = v
	}
	if v := settings["shell"]; v != "" {
		p.Shell = v
	}
	if v := settings["size"]; v != "" {
		p.Width, p.Height = parseSize(v)
	}
	if v := settings["bpp"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			p.BPP = n
		}
	}
	// Флаги: известные ключи дают пустое значение («/fullscreen», «/clipboard»),
	// rdesktop-булевы — «1» («-f», «-C», «-0»). Определяем по наличию ключа, а не по
	// значению; «+clipboard» (xfreerdp) не считается флагом в rdpIsFlag → лежит в
	// unknown, учитываем его отдельно.
	p.Fullscreen = hasKey(settings, "fullscreen")
	p.Clipboard = hasKey(settings, "clipboard") || slices.Contains(unknown, "+clipboard") ||
		isClipRedirect(settings["redirect"]) // rdesktop "-r clipboard:…"
	// Наличие ключа = включено: «/admin» без значения даёт пустую строку
	// (truthy("") == false — из-за этого поле Admin было мёртвым).
	p.Admin = hasKey(settings, "admin")
	// M37: галки безопасности (наличие ключа в карте = включено).
	p.CertIgnore = hasKey(settings, "certIgnore")
	p.NoNLA = hasKey(settings, "noNLA")
	p.NoTLS = hasKey(settings, "noTLS")
	p.NoRDP = hasKey(settings, "noRDP")
	p.NoAuth = hasKey(settings, "noAuth")
	p.NoFastPath = hasKey(settings, "noFastPath")
	p.RFX = hasKey(settings, "rfx")
	p.NSC = hasKey(settings, "nsc")
	p.DynamicResolution = hasKey(settings, "dynamicResolution")
	p.FontSmooth = hasKey(settings, "fontSmooth")
	// M37-ревью MINOR-4: снять ВСЕ три типа защиты — не «no security», а
	// сломанный конфиг: клиент предложил бы серверу соединение без защиты.
	// Возвращаемся к умолчанию (защита не понижается молча).
	if p.NoNLA && p.NoTLS && p.NoRDP {
		p.NoNLA, p.NoTLS, p.NoRDP = false, false, false
	}
	// Уровень TLS: 1..5 (0 = не задано); мусор/вне диапазона — игнор.
	if v := settings["tlsSecLevel"]; len(v) == 1 && v[0] >= '1' && v[0] <= '5' {
		p.TLSSecLevel = int(v[0] - '0')
	}
	return p
}

// isClipRedirect — редирект `-r clipboard:…` → включает буфер обмена.
func isClipRedirect(v string) bool {
	return strings.HasPrefix(strings.ToLower(v), "clipboard:")
}

// hasKey — присутствует ли ключ в маппинге (даже с пустым значением).
func hasKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

// VNCParamsFrom собирает VNCParams из окружения соединения.
func VNCParamsFrom(e *config.Environment) VNCParams {
	return VNCParams{
		Host:       e.IP,
		Port:       portOr(e.Port, vncDefaultPort),
		Password:   e.Pass,
		Username:   e.User,
		Scale:      1,
		Security:   normalizeVNCSecurity(e.VNCEncryption),
		Fullscreen: method.ParseVNCFullscreen(e.Options),
	}
}

// vncSecurityAuto — значение по умолчанию режима VeNCrypt (тождественно "").
const vncSecurityAuto = "auto"

// normalizeVNCSecurity — нормализация режима VeNCrypt: "" и "auto" → "auto";
// неизвестные/опечатки → "auto" (безопасный дефолт). Режим передаётся в C++
// только как "auto"/"vencrypt"/"none".
func normalizeVNCSecurity(v string) string {
	switch v {
	case "", "auto":
		return vncSecurityAuto
	case "vencrypt", "none":
		return v
	}
	return vncSecurityAuto
}

// portOr — порт или дефолт (пустой/нечисловой → дефолт). Порт ограничен
// допустимым диапазоном TCP-порта 1..65535 (иначе >65535 усечётся до 16 бит
// в freerdp_settings_set_uint32 / ConnectClientToTcpAddr6WithTimeout) — security.
func portOr(v string, def int) int {
	if v == "" {
		return def
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
		return n
	}
	return def
}

// parseSize — «WxH» (или пресет 1080p/720p) → (w,h). Пустое/неразбираемое → (0,0),
// чтобы виджет/сервер выбрали размер сами (легаси часто пускает без размера).
func parseSize(v string) (int, int) {
	if v == "" {
		return 0, 0
	}
	lower := strings.ToLower(v)
	switch lower {
	case "1080p":
		return 1920, 1080
	case "720p":
		return 1280, 720
	case "workarea", "fullscreen":
		return 0, 0
	}
	parts := strings.Split(v, "x")
	if len(parts) == 2 {
		w, err1 := strconv.Atoi(parts[0])
		h, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil && w > 0 && h > 0 {
			return w, h
		}
	}
	return 0, 0
}

// truthy — непустое «истинное» значение флага (1/on/true/yes).
func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}
