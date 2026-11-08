package ui

import (
	"strconv"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// connForm — значение формы редактора (чистая модель; тестируется без Qt).
type connForm struct {
	Method                                   string // «Тип» (напр. "SSH"/"Telnet"/"RDP (rdesktop)") — B1: сохранялся только в combo, не в env
	Name, IP, Port                           string
	User, Pass                               string
	AuthType, AuthFallback                   string
	PublicKey, Passphrase                    string
	Options                                  string
	Prepend, Postpend                        string
	SendString                               string
	SendStringEvery                          int
	SendStringOnlyWhenIdle                   bool
	UseSudo, AutoReconnect, Autossh          bool
	EnableRandomSocks                        bool // бэклог Incognito (M30)
	UseProxy                                 int
	ProxyIP, ProxyPort, ProxyUser, ProxyPass string
	JumpIP, JumpUser, JumpPass, JumpKey      string
	JumpPort                                 int
	ExpectRows                               []expectRow
	Variables                                []map[string]any
	// M29-остаток (#2): поля соединения (Description/Title/KeePass-per-conn/
	// startup/macros), раньше не редактировались в форме.
	Description       string
	Title             string
	KPXUUID           string
	StartUpLaunch     bool
	StartUpScript     string
	StartUpScriptName string
	Macros            []map[string]any
	VNCEncryption     string // #4: 'vnc security' (auto/vencrypt/none)
	UseSecretService  bool   // #7-UI: 'use secret service' (пароль из SecretService)
}

// expectRow — табличная строка expect-блока (мап-порождающие ключи).
type expectRow struct {
	Active  bool
	Expect  string
	Send    string
	Hidden  bool
	Return  bool
	OnMatch int // -1 следующий, -2 connected, N>0 прыжок (1-based)
	OnFail  int
	TimeOut int // -1 = без таймаута
}

// formFromEnv — форма из окружения (строки/флаги/таблицы).
func formFromEnv(env *config.Environment) connForm {
	if env == nil {
		return connForm{}
	}
	f := connForm{
		Method: env.Method, // B1: «Тип» (combo) должен писать в env
		Name:   env.Name, IP: env.IP, Port: env.Port, User: env.User, Pass: env.Pass,
		// Новое соединение приходит с пустым AuthFallback (Sanitize ещё не
		// вызывался) — нормализуем здесь, чтобы combo {"0","1"} отразил легаси
		// дефолт "1", а doSave не записал "0". Та же функция, что в Sanitize.
		AuthType: env.AuthType, AuthFallback: config.NormalizeAuthFallback(env.AuthFallback),
		PublicKey: env.PublicKey, Passphrase: env.Passphrase,
		Options: env.Options, Prepend: env.PrependCommand, Postpend: env.PostpendCommand,
		SendString:             env.SendStringTxt,
		SendStringEvery:        int(env.SendStringEvery),
		SendStringOnlyWhenIdle: env.SendStringOnlyWhenIdle == 1,
		UseSudo:                env.UseSudo == 1, AutoReconnect: env.AutoReconnect == 1,
		Autossh: env.Autossh == 1, EnableRandomSocks: env.EnableRandomSocks == 1,
		UseProxy: int(env.UseProxy),
		ProxyIP:  env.ProxyIP, ProxyPort: env.ProxyPort, ProxyUser: env.ProxyUser, ProxyPass: env.ProxyPass,
		JumpIP: env.JumpIP, JumpPort: int(env.JumpPort), JumpUser: env.JumpUser,
		JumpPass: env.JumpPass, JumpKey: env.JumpKey,
		Variables:   env.Variables,
		Description: env.Description, Title: env.Title, KPXUUID: env.KPXUUID,
		StartUpLaunch: env.StartUpLaunch == 1, StartUpScript: env.StartUpScript,
		StartUpScriptName: env.StartUpScriptName,
		Macros:            env.Macros,
		VNCEncryption:     env.VNCEncryption,
		UseSecretService:  env.UseSecretService == 1,
	}
	for _, m := range env.Expect {
		r := expectRow{
			Active:  num(m, "active", 1) == 1,
			Expect:  str(m, "expect"),
			Send:    str(m, "send"),
			Hidden:  num(m, "hidden", 0) == 1,
			Return:  num(m, "return", 1) == 1,
			OnMatch: num(m, "on_match", -1),
			OnFail:  num(m, "on_fail", -1),
			TimeOut: num(m, "time_out", -1),
		}
		f.ExpectRows = append(f.ExpectRows, r)
	}
	return f
}

// formToExpect — таблица обратно в конфигурационные мапы (ключи легаси).
func formToExpect(rows []expectRow) []map[string]any {
	if len(rows) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"active":   boolToInt(r.Active),
			"expect":   r.Expect,
			"send":     r.Send,
			"hidden":   boolToInt(r.Hidden),
			"return":   boolToInt(r.Return),
			"on_match": r.OnMatch,
			"on_fail":  r.OnFail,
			"time_out": r.TimeOut,
		})
	}
	return out
}

func num(m map[string]any, k string, def int) int {
	if v, ok := m[k]; ok {
		if n, ok := v.(int); ok {
			return n
		}
	}
	return def
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// applyForm — применение формы к окружению (неизвестные поля не трогаются).
func applyForm(env *config.Environment, f connForm) {
	env.Method = f.Method // B1: тип сохранялся только в combo, не в env
	env.Name, env.IP, env.Port = f.Name, f.IP, f.Port
	env.User, env.Pass = f.User, f.Pass
	env.AuthType, env.AuthFallback = f.AuthType, f.AuthFallback
	env.PublicKey, env.Passphrase = f.PublicKey, f.Passphrase
	env.Options = f.Options
	env.PrependCommand, env.PostpendCommand = f.Prepend, f.Postpend
	env.SendStringTxt = f.SendString
	// Runtime гейтится включающими флагами (session.applyPrePost /
	// sendStringSpec), а в форме для них нет отдельных чекбоксов — выводим их
	// из непустых полей, иначе prepend/postpend/send string молча не работают.
	env.UsePrependCommand = config.FlexInt(boolToInt(strings.TrimSpace(f.Prepend) != ""))
	env.UsePostpendCommand = config.FlexInt(boolToInt(strings.TrimSpace(f.Postpend) != ""))
	env.SendStringActive = config.FlexInt(boolToInt(strings.TrimSpace(f.SendString) != ""))
	// 'send string intro' — легаси-чекбокс «добавлять перевод строки». В UI его
	// нет: при активной отправке по умолчанию включаем (иначе строка уходит без
	// Enter), явно сохранённое значение ("0") не перетираем.
	if env.SendStringActive == 1 {
		if env.SendStringIntro == "" {
			env.SendStringIntro = "1"
		}
	} else {
		env.SendStringIntro = ""
	}
	// QuoteCommand/QuotePostCommand — легаси-обёртка команды для sh; Go-порт
	// исполняет argv без shell, поэтому кавычки неприменимы и редактор их больше
	// не пишет (существующие значения остаются нетронутыми — совместимость).
	env.SendStringEvery = config.FlexInt(f.SendStringEvery)
	env.SendStringOnlyWhenIdle = config.FlexInt(boolToInt(f.SendStringOnlyWhenIdle))
	env.UseSudo = config.FlexInt(boolToInt(f.UseSudo))
	env.AutoReconnect = config.FlexInt(boolToInt(f.AutoReconnect))
	env.Autossh = config.FlexInt(boolToInt(f.Autossh))
	env.EnableRandomSocks = config.FlexInt(boolToInt(f.EnableRandomSocks))
	env.UseProxy = config.FlexInt(f.UseProxy)
	env.ProxyIP, env.ProxyPort, env.ProxyUser, env.ProxyPass = f.ProxyIP, f.ProxyPort, f.ProxyUser, f.ProxyPass
	env.JumpIP = f.JumpIP
	env.JumpPort = config.FlexInt(f.JumpPort)
	env.JumpUser, env.JumpPass, env.JumpKey = f.JumpUser, f.JumpPass, f.JumpKey
	env.Expect = formToExpect(f.ExpectRows)
	env.Variables = f.Variables
	env.Description, env.Title, env.KPXUUID = f.Description, f.Title, f.KPXUUID
	env.StartUpLaunch = config.FlexInt(boolToInt(f.StartUpLaunch))
	env.StartUpScript, env.StartUpScriptName = f.StartUpScript, f.StartUpScriptName
	env.VNCEncryption = f.VNCEncryption
	env.UseSecretService = config.FlexInt(boolToInt(f.UseSecretService))
	env.Macros = f.Macros
}

// expectCells — индексы колонок таблицы Expect.
const (
	colActive  = 0
	colExpect  = 1
	colSend    = 2
	colHidden  = 3
	colReturn  = 4
	colOnMatch = 5
	colOnFail  = 6
	colTimeOut = 7
)

// expectRowFromCells — строка из ячеек: пустые/нечисловые значения НЕ
// затирают легаси-дефолты (ревью M13 H2: Atoi("") давал 0; time_out=0
// закрывал сессию на первом тике).
func expectRowFromCells(cells map[int]string, prev expectRow) expectRow {
	r := prev
	if v, err := strconv.Atoi(cells[colOnMatch]); err == nil {
		r.OnMatch = v
	}
	if v, err := strconv.Atoi(cells[colOnFail]); err == nil {
		r.OnFail = v
	}
	if v, err := strconv.Atoi(cells[colTimeOut]); err == nil {
		r.TimeOut = v
	}
	switch cells[colReturn] {
	case "0":
		r.Return = false
	case "1":
		r.Return = true
	}
	if v, ok := cells[colExpect]; ok {
		r.Expect = v
	}
	if v, ok := cells[colSend]; ok {
		r.Send = v
	}
	return r
}

// variableFromCells — сохранение переменной: исходная мапа дополняется
// name/txt, остальные ключи (hide/variable/type…) не теряются (M13-ревью).
func variableFromCells(orig map[string]any, name, txt string) (map[string]any, bool) {
	if name == "" && txt == "" {
		return nil, false // пустая строка не пишется
	}
	m := map[string]any{}
	for k, v := range orig {
		m[k] = v
	}
	m["name"] = name
	m["txt"] = txt
	return m, true
}

// macroFromCells — сборка макроса: исходная мапа сохраняет незатронутые
// ключи (menu/expand/check…), name→description, cmd→txt/command.
func macroFromCells(orig map[string]any, name, cmd string) (map[string]any, bool) {
	if name == "" && cmd == "" {
		return nil, false
	}
	m := map[string]any{}
	for k, v := range orig {
		m[k] = v
	}
	m["description"] = name
	m["txt"] = cmd
	m["command"] = cmd
	return m, true
}

// mergeMacros — сборка таблицы макросов в слайс (аналог mergeVariables)
// для Save в редакторе: исходные мапы по индексу сохраняют ключи.
func mergeMacros(old []map[string]any, cells [][2]string) []map[string]any {
	out := make([]map[string]any, 0, len(cells))
	for i, p := range cells {
		var o map[string]any
		if i < len(old) {
			o = old[i]
		}
		if m, ok := macroFromCells(o, p[0], p[1]); ok {
			out = append(out, m)
		}
	}
	return out
}

// atoiOr — число из строки; мусор/пустая строка → prev (M13-ревью L1).
func atoiOr(s string, prev int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return prev
}

// mergeVariables — сборка таблицы переменных в слайс: исходные мапы по
// индексу сохраняют незатронутые ключи (hide/variable/type), пустые
// строки отбрасываются. Используется редактором на Save (M13-ре-ревью:
// обнуление до цикла потеряло orig и все ключи).
func mergeVariables(old []map[string]any, cells [][2]string) []map[string]any {
	out := make([]map[string]any, 0, len(cells))
	for i, p := range cells {
		var o map[string]any
		if i < len(old) {
			o = old[i]
		}
		if m, ok := variableFromCells(o, p[0], p[1]); ok {
			out = append(out, m)
		}
	}
	return out
}
