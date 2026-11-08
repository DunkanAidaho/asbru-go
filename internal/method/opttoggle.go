package method

import (
	"fmt"
	"slices"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/i18n"
)

// M34 (issue 3): основные ключи — как галки. Оригинал: каждый чекбокс эмитит
// ОДИН токен в опционную строку (~PACMethod_*.pm get_cfg/_parseOptionsToCfg).
// Здесь — токен-уровневый движок над строкой options (диалект-агностик: токены
// берутся как есть в строке). Значение-типы (bpp/size/domain/kbd/shell) пока
// остаются в сырой строке options (поле «Опции»), флаги — как галки.

// OptionToggle — одна галка-опция (флаг).
type OptionToggle struct {
	Setting string   // канонический ключ (для тестов/UI)
	Label   string   // подпись в редакторе
	Tokens  []string // токены, означающие «включено» (проверено)
}

// screenGroup — «картиночные» галки: в редакторе показываются в блоке «Экран»
// («Основные»), а не во вкладке «Опции». Данные, а не флаг в каждом литерале.
//
// ОГОВОРКА по «compression» (ревью M38): токен +compression оставлен ради
// паритета с легаси PACMethod_xfreerdp.pm, но он НИЧЕГО не меняет. Ключ
// `compression` у клиента ЕСТЬ (`xfreerdp3 --help`: «-compression  Disable
// compression»; `+compression` парсится — в отличие от мусорного `+bogusxyz`),
// но `+`/`-` — это enable/disable, а сжатие включено ПО УМОЛЧАНИЮ → `+` не
// меняет ничего. Выключить можно только `-compression`, которого в легаси не
// было. (В FreeRDP 3 сам FreeRDP_CompressionEnabled помечен deprecated —
// будущему реализатору `-compression` сверить актуальный рычаг на живом сервере.)
// Для встроенного клиента галку не прокидываем намеренно: «снятая» галка
// означала бы CompressionEnabled=FALSE у всех конфигов БЕЗ токена — то есть
// молчаливую потерю сжатия у работающих подключений. Настоящее «выключить
// сжатие» — отдельная задача (новый токен -compression), см. BACKLOG.md.
var screenGroup = map[string]bool{
	"compression": true, "fontSmooth": true, "rfx": true, "nsCodec": true,
}

// InScreenGroup — галка относится к блоку «Экран».
func InScreenGroup(setting string) bool { return screenGroup[setting] }

// OptionsFor — набор галок на диалект (по легаси PACMethod_*.pm).
func OptionsFor(d Dialect) []OptionToggle {
	switch d {
	case SSH:
		return []OptionToggle{
			{"forwardX", "X11 forwarding (-X)", []string{"-X"}},
			{"useCompression", "Compression (-C)", []string{"-C"}},
			{"allowPortConnect", "Allow remote port connect (-g)", []string{"-g"}},
			{"forwardAgent", "Agent forwarding (-A)", []string{"-A"}},
			{"noRemoteCmd", "Don't exec remote cmd (-N)", []string{"-N"}},
		}
	case SFTP:
		return []OptionToggle{
			{"useCompression", "Compression (-C)", []string{"-C"}},
		}
	case XFreeRDP:
		// M37 (п.2): полный набор легаси PACMethod_xfreerdp.pm — при переносе
		// потерялись sec-опции (без них подключение к части серверов не идёт)
		// и часть флагов. Порядок — как в легаси _parseOptionsToCfg.
		return []OptionToggle{
			{"fullscreen", i18n.Tr("Editor", "Fullscreen (/f)"), []string{"/f"}},
			{"admin", i18n.Tr("Editor", "Attach to console (/admin)"), []string{"/admin"}},
			{"compression", i18n.Tr("Editor", "Сжатие (+compression)"), []string{"+compression"}},
			{"clipboard", i18n.Tr("Editor", "Clipboard redirect (+clipboard)"), []string{"+clipboard"}},
			{"ignoreCert", i18n.Tr("Editor", "Игнорировать сертификат (/cert-ignore)"), []string{"/cert-ignore"}},
			{"noAuth", i18n.Tr("Editor", "Отключить аутентификацию (-authentication)"), []string{"-authentication"}},
			{"nofastPath", i18n.Tr("Editor", "Отключить fast-path (-fast-path)"), []string{"-fast-path"}},
			{"rfx", i18n.Tr("Editor", "RemoteFX (/rfx)"), []string{"/rfx"}},
			{"nsCodec", i18n.Tr("Editor", "NSCodec (/nsc)"), []string{"/nsc"}},
			{"dynamicResolution", i18n.Tr("Editor", "Динамическое разрешение (/dynamic-resolution)"), []string{"/dynamic-resolution"}},
			{"noRDP", i18n.Tr("Editor", "Отключить RDP-шифрование (-sec-rdp)"), []string{"-sec-rdp"}},
			{"noTLS", i18n.Tr("Editor", "Отключить TLS (-sec-tls)"), []string{"-sec-tls"}},
			{"noNLA", i18n.Tr("Editor", "Отключить NLA (-sec-nla)"), []string{"-sec-nla"}},
			{"fontSmooth", i18n.Tr("Editor", "Сглаживание шрифтов (+fonts)"), []string{"+fonts"}},
			{"grabKey", i18n.Tr("Editor", "Не перехватывать клавиатуру (-grab-keyboard)"), []string{"-grab-keyboard"}},
		}
	case RDesktop:
		return []OptionToggle{
			{"fullscreen", "Fullscreen (-f)", []string{"-f"}},
			{"admin", "Attach to console (-0)", []string{"-0"}},
			{"compression", "Compression (-z)", []string{"-z"}},
			{"clipboard", "Clipboard redirect (-r clipboard:PRIMARYCLIPBOARD)", []string{"-r", "clipboard:PRIMARYCLIPBOARD"}},
			{"bitmapCaching", i18n.Tr("Editor", "Кэш битмапов (-P)"), []string{"-P"}}, // M37: потеряна при переносе
			{"grabKey", i18n.Tr("Editor", "Не перехватывать клавиатуру (-K)"), []string{"-K"}},
		}
	case VNC:
		// M39: галок нет намеренно. Встроенному VNC опции в argv не передаются
		// (vncview.Start получает host/port/user/pass/title/security), поэтому
		// «-ViewOnly=1» оставался бы мёртвой галкой — а ViewOnly вдобавок врал бы
		// опаснее прочего: ввод и буфер обмена в этом режиме НЕ глушатся.
		// Полноэкранный режим (M39-fullscreen) НЕ галкой, а существующими
		// легаси-токенами строки options: embed.VNCParamsFrom читает
		// «-fullscreen» и «-FullScreen=N» (см. method.ParseVNCFullscreen).
		// Токены в options существующих конфигов не трогаем.
		return nil
	case PVE:
		// Единственная галка: пропуск проверки TLS-сертификата PVE (self-signed
		// у большинства инсталляций). Токен читает PVE-виджет при создании
		// pve.Client (InsecureSkipVerify), в командную строку не попадает.
		return []OptionToggle{
			{"verifyTLS", i18n.Tr("Editor", "Проверять TLS-сертификат"), []string{"verify-tls"}},
		}
	default:
		return nil
	}
}

// TogglePresent — проверена ли галка (ВСЕ токены присутствуют; для одиночного
// флага — достаточно его самого, для двухтокенного rdesktop-буфера (например
// "-r clipboard:PRIMARYCLIPBOARD") — оба).
func TogglePresent(options string, t OptionToggle) bool {
	if len(t.Tokens) == 0 {
		return false
	}
	toks := SplitArgs(options)
	if isRdpClipboardToggle(t) {
		return hasRdpClipboard(toks)
	}
	for _, token := range t.Tokens {
		found := false
		for _, x := range toks {
			if x == token {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// isRdpClipboardToggle — галка буфера rdesktop (каноническая пара -r + clipboard:*).
func isRdpClipboardToggle(t OptionToggle) bool {
	return len(t.Tokens) == 2 && t.Tokens[0] == "-r" && strings.HasPrefix(t.Tokens[1], "clipboard:")
}

// hasRdpClipboard — есть ли редирект буфера в любой форме: пара
// "-r clipboard:*" (любая селекция, в т.ч. пустая) или слитная "-rclipboard:*".
func hasRdpClipboard(toks []string) bool {
	for i, x := range toks {
		if strings.HasPrefix(x, "-rclipboard:") {
			return true
		}
		if x == "-r" && i+1 < len(toks) && strings.HasPrefix(toks[i+1], "clipboard:") {
			return true
		}
	}
	return false
}

// removeRdpClipboard — удаляет ВСЕ формы редиректа буфера, сохраняя прочие
// "-r" (sound/disk/…), чтобы повторное включение не давало дубль.
func removeRdpClipboard(toks []string) []string {
	out := make([]string, 0, len(toks))
	for i := 0; i < len(toks); {
		if strings.HasPrefix(toks[i], "-rclipboard:") {
			i++
			continue
		}
		if toks[i] == "-r" && i+1 < len(toks) && strings.HasPrefix(toks[i+1], "clipboard:") {
			i += 2
			continue
		}
		out = append(out, toks[i])
		i++
	}
	return out
}

// SetToggle — добавить/убрать токены галки: удаляем ВСЕ её токены; если on —
// добавляем ВСЕ её токены. Идемпотентно; остальные токены сохраняются.
// Для многотокенной галки (например rdesktop buffer "-r clipboard:PRIMARYCLIPBOARD")
// удаляем ТОЧНУЮ ГРУППУ, а не любой отдельный токен — иначе общий префикс "-r"
// (sound/disk) стирался бы с другими redirect-ами. Буфер обмена распознаётся
// в любой селекции/слитной форме (см. hasRdpClipboard).
func SetToggle(options string, t OptionToggle, on bool) (string, error) {
	if len(t.Tokens) == 0 {
		return options, nil
	}
	toks := SplitArgs(options)
	if isRdpClipboardToggle(t) {
		toks = removeRdpClipboard(toks)
	} else {
		toks = removeTokenGroup(toks, t.Tokens)
	}
	if on {
		toks = append(toks, t.Tokens...)
	}
	return JoinArgs(toks), nil
}

// removeTokenGroup — убирает ОДНО вхождение соседней группы group.
func removeTokenGroup(toks, group []string) []string {
	n := len(group)
	if n == 0 {
		return toks
	}
	out := make([]string, 0, len(toks))
	for i := 0; i < len(toks); {
		if i+n <= len(toks) && slices.Equal(toks[i:i+n], group) {
			i += n
			continue
		}
		out = append(out, toks[i])
		i++
	}
	return out
}

// JoinArgs — обратно в строку (единый с joinTokens из convert.go).
func JoinArgs(tokens []string) string {
	return joinTokens(tokens)
}

// OptionValue — опция-значение с фиксированным набором (легаси-комбо),
// напр. /tls-seclevel:N. Off — значение, означающее «токена нет» (выключено).
type OptionValue struct {
	Setting string
	Label   string
	Prefix  string
	Values  []string
	Off     string
}

// tlsSecLevelOption — уровень TLS xfreerdp (легаси PACMethod_xfreerdp.pm:392-397).
func tlsSecLevelOption() OptionValue {
	return OptionValue{
		Setting: "tlsSecLevel",
		Label:   i18n.Tr("Editor", "Уровень TLS (/tls-seclevel:)"),
		Prefix:  "/tls-seclevel:",
		Values:  []string{"0", "1", "2", "3", "4", "5"},
		Off:     "0",
	}
}

// OptionValuesFor — опции-значения диалекта (в редакторе — комбо).
func OptionValuesFor(d Dialect) []OptionValue {
	if d == XFreeRDP {
		return []OptionValue{tlsSecLevelOption()}
	}
	return nil
}

// ValuePresent — текущее значение опции ("" — токена нет).
func ValuePresent(options string, v OptionValue) string {
	for _, tok := range SplitArgs(options) {
		if strings.HasPrefix(tok, v.Prefix) {
			return strings.TrimPrefix(tok, v.Prefix)
		}
	}
	return ""
}

// SetValue — выставить значение опции: пусто/Off → токен убирается, иначе
// ровно один токен Prefix+val (прежний заменяется); прочие токены целы.
func SetValue(options string, v OptionValue, val string) (string, error) {
	if val != "" && val != v.Off && !slices.Contains(v.Values, val) {
		return options, fmt.Errorf("недопустимое значение %q для %s", val, v.Setting)
	}
	toks := SplitArgs(options)
	out := make([]string, 0, len(toks)+1)
	for _, tok := range toks {
		if strings.HasPrefix(tok, v.Prefix) {
			continue // прежнее значение убираем
		}
		out = append(out, tok)
	}
	if val != "" && val != v.Off {
		out = append(out, v.Prefix+val)
	}
	return JoinArgs(out), nil
}
