// Пакет config: YAML-конфиг Ásbrú и совместимое с легаси шифрование.
//
// Структуры проецируют hyphenated-ключи asbru.yml; все ключи, которых нет
// в схеме, попадают в Raw и сохраняются при записи без изменений.
package config

import (
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// RootUUID — корень дерева соединений (__PAC__ROOT__).
const RootUUID = "__PAC__ROOT__"

// Config — верхний уровень asbru.yml.
type Config struct {
	Defaults            Defaults               `yaml:"defaults"`
	Environments        map[string]Environment `yaml:"environments"`
	Tmp                 map[string]any         `yaml:"tmp,omitempty"`
	ConfigVersion       FlexInt                `yaml:"config version,omitempty"`
	ConfigVersionChange string                 `yaml:"config version change,omitempty"`
	// маркеры экспорта (легаси PACConfig.pm:635-648): known-ключи уже в
	// captureRaw-списке, без полей при загрузке молча терялись бы
	Exported        map[string]any `yaml:"__PAC__EXPORTED__,omitempty"` // легаси — map узлов (PACMain.pm:4344)
	ExportedFull    FlexInt        `yaml:"__PAC__EXPORTED__FULL__,omitempty"`
	ExportedPartial map[string]any `yaml:"__PAC__EXPORTED__PARTIAL_CONF,omitempty"`
	Raw             map[string]any `yaml:"-"`
}

// Defaults — секция defaults.
type Defaults struct {
	// seen — присутствие ключей в исходном YAML (внутреннее): явная 0/пустая
	// строка НЕ перетирается сидом (легаси //=: defined ≠ undef).
	seen                    map[string]bool                `yaml:"-"`
	Version                 string                         `yaml:"version"`
	ConfigVersion           FlexInt                        `yaml:"config version"`
	GUIPassword             string                         `yaml:"gui password"`
	UseGUIPassword          FlexInt                        `yaml:"use gui password"`
	UseGUIPasswordTray      FlexInt                        `yaml:"use gui password tray"`
	SudoPassword            string                         `yaml:"sudo password"`
	SudoShowPassword        FlexInt                        `yaml:"sudo show password"`
	AutoAcceptKey           FlexInt                        `yaml:"auto accept key"`
	TimeOutConnect          FlexInt                        `yaml:"timeout connect"`
	TimeOutCommand          FlexInt                        `yaml:"timeout command"`
	CommandPrompt           string                         `yaml:"command prompt"`
	UsernamePrompt          string                         `yaml:"username prompt"`
	PasswordPrompt          string                         `yaml:"password prompt"`
	HostkeyChangedPrompt    string                         `yaml:"hostkey changed prompt"`
	PressAnyKeyPrompt       string                         `yaml:"press any key prompt"`
	RemoteHostChangedPrompt string                         `yaml:"remote host changed prompt"`
	SudoPrompt              string                         `yaml:"sudo prompt"`
	Proxy                   string                         `yaml:"proxy"` // M35 P0: селектор 'Jump'/'Proxy' (case 0)
	ProxyIP                 string                         `yaml:"proxy ip"`
	ProxyPort               string                         `yaml:"proxy port"`
	ProxyUser               string                         `yaml:"proxy user"`
	ProxyPass               string                         `yaml:"proxy pass"`
	JumpIP                  string                         `yaml:"jump ip"`
	JumpPort                FlexInt                        `yaml:"jump port"`
	JumpUser                string                         `yaml:"jump user"`
	JumpPass                string                         `yaml:"jump pass"`
	JumpKey                 string                         `yaml:"jump key"`
	Keepass                 Keepass                        `yaml:"keepass"`
	KeyBindings             map[string]map[string][]string `yaml:"keybindings,omitempty"`
	GlobalVariables         map[string]VarEntry            `yaml:"global variables"`
	Raw                     map[string]any                 `yaml:"-"`
}

// Keepass — настройки KeePassXC.
type Keepass struct {
	UseKeepass FlexInt `yaml:"use_keepass"`
	Database   string  `yaml:"database"`
	PathCLI    string  `yaml:"pathcli"`
	KeyFile    string  `yaml:"keyfile"`
	Password   string  `yaml:"password"`
}

// ClusterNames — список имён кластеров соединения (легаси: cluster — массив
// имён, PACUtils.pm:2393). Группы при запуске кластера не участвуют.
// Толерантный разбор: скаляр (старые конфиги/0) не роняет загрузку.
type ClusterNames []string

// UnmarshalYAML — последовательность → список имён; скаляр → [имя], прочее
// (0, null, пустое) → nil.
func (c *ClusterNames) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		val := node.Value
		if node.Tag == "!!null" || val == "" || val == "0" {
			*c = nil
			return nil
		}
		*c = ClusterNames{val}
		return nil
	case yaml.SequenceNode:
		*c = nil // ре-вью M20: переиспользование Environment не накапливает
		for _, n := range node.Content {
			if n.Kind == yaml.ScalarNode && n.Value != "" && n.Value != "0" {
				*c = append(*c, n.Value)
			}
		}
		return nil
	}
	*c = nil
	return nil
}

// VarEntry — одна глобальная переменная.
type VarEntry struct {
	Type   string  `yaml:"type"`
	Value  string  `yaml:"value"`
	Hidden FlexInt `yaml:"hidden"`
}

// Environment — соединение или группа в environments{uuid}.
type Environment struct {
	Name                   string             `yaml:"name"`
	Description            string             `yaml:"description"`
	Parent                 string             `yaml:"parent"`
	Title                  string             `yaml:"title"`
	IP                     string             `yaml:"ip"`
	Port                   string             `yaml:"port"`
	User                   string             `yaml:"user"`
	Pass                   string             `yaml:"pass"`
	SearchPassOnKPX        FlexInt            `yaml:"search pass on KPX"`
	KPXTitleRegexp         string             `yaml:"KPX title regexp"`
	KPXUUID                string             `yaml:"KPX uuid"` // KeePass-ссылка на запись по Uuid (точечный доступ через SecretService)
	Method                 string             `yaml:"method"`
	AuthType               string             `yaml:"auth type"`
	AuthFallback           string             `yaml:"auth fallback"`
	PublicKey              string             `yaml:"public key"`
	PassphraseUser         string             `yaml:"passphrase user"`
	Passphrase             string             `yaml:"passphrase"`
	Options                string             `yaml:"options"`
	EnableRandomSocks      FlexInt            `yaml:"create random socks tunnel"`
	VNCEncryption          string             `yaml:"vnc security"`       // встроенный VNC: auto/vencrypt/none (VeNCrypt)
	UseSecretService       FlexInt            `yaml:"use secret service"` // бэклог #7: пароль берём из SecretService (org.freedesktop.secrets)
	UseProxy               FlexInt            `yaml:"use proxy"`
	ProxyIP                string             `yaml:"proxy ip"`
	ProxyPort              string             `yaml:"proxy port"`
	ProxyUser              string             `yaml:"proxy user"`
	ProxyPass              string             `yaml:"proxy pass"`
	JumpIP                 string             `yaml:"jump ip"`
	JumpPort               FlexInt            `yaml:"jump port"`
	JumpUser               string             `yaml:"jump user"`
	JumpPass               string             `yaml:"jump pass"`
	JumpKey                string             `yaml:"jump key"`
	SaveSessionLogs        FlexInt            `yaml:"save session logs"`
	SessionLogPattern      string             `yaml:"session log pattern"`
	SessionLogsFolder      string             `yaml:"session logs folder"`
	SessionLogsAmount      FlexInt            `yaml:"session logs amount"`
	UsePrependCommand      FlexInt            `yaml:"use prepend command"`
	PrependCommand         string             `yaml:"prepend command"`
	UsePostpendCommand     FlexInt            `yaml:"use postpend command"`
	PostpendCommand        string             `yaml:"postpend command"`
	QuoteCommand           FlexInt            `yaml:"quote command"`
	QuotePostCommand       FlexInt            `yaml:"quotepost command"`
	SendStringActive       FlexInt            `yaml:"send string active"`
	SendStringTxt          string             `yaml:"send string txt"`
	SendStringIntro        string             `yaml:"send string intro"`
	SendStringEvery        FlexInt            `yaml:"send string every"`
	SendStringOnlyWhenIdle FlexInt            `yaml:"send string only when idle"`
	Embed                  FlexInt            `yaml:"embed"`
	Mac                    string             `yaml:"mac"`
	AutoReconnect          FlexInt            `yaml:"autoreconnect"`
	RemoveControlChars     FlexInt            `yaml:"remove control chars"`
	StartUpLaunch          FlexInt            `yaml:"startup launch"`
	StartUpScript          string             `yaml:"startup script"`
	StartUpScriptName      string             `yaml:"startup script name"`
	Favourite              FlexInt            `yaml:"favourite"`
	Autossh                FlexInt            `yaml:"autossh"`
	Cluster                ClusterNames       `yaml:"cluster"`
	UseSudo                FlexInt            `yaml:"use sudo"`
	IsGroup                FlexInt            `yaml:"_is_group,omitempty"`
	Protected              FlexInt            `yaml:"_protected,omitempty"` // M36: защита от Edit/Delete/Rename (легаси _protected)
	Children               map[string]FlexInt `yaml:"children,omitempty"`
	Variables              []map[string]any   `yaml:"variables"`
	Screenshots            []any              `yaml:"screenshots"`
	LocalBefore            []map[string]any   `yaml:"local before"`
	Expect                 []map[string]any   `yaml:"expect"`
	LocalConnected         []map[string]any   `yaml:"local connected"`
	Macros                 []map[string]any   `yaml:"macros"`
	LocalAfter             []map[string]any   `yaml:"local after"`
	TerminalOptions        map[string]any     `yaml:"terminal options,omitempty"`
	Raw                    map[string]any     `yaml:"-"`
	NameSet                bool               `yaml:"-"` // присутствие ключа name в исходном YAML
}

// UnknownDefaultKey отдаёт значение неизвестного ключа defaults (для тестов).
func (d *Defaults) UnknownDefaultKey() any {
	return d.Raw["unknown-defaults-key"]
}

// ShowStatusBar — defaults 'terminal show status bar' (легаси PACTerminal.pm:814,
// PACUtils.pm:2113 //= 1 — по умолчанию ВКЛЮЧЕНО): строка состояния внизу терминала
// (бэклог). Хранится в Raw; отсутствие ключа → true (легаси-дефолт).
func (d *Defaults) ShowStatusBar() bool {
	if d == nil {
		return true
	}
	v, ok := d.Raw["terminal show status bar"]
	if !ok {
		return true
	}
	return numAsBool(v)
}

// ShowNotifications — defaults 'show notifications' (аудит #8; по умолчанию
// ВЫКЛ): отправлять D-Bus-уведомления (org.freedesktop.Notifications) об
// ошибках подключения. Хранится в Raw; отсутствие ключа → false.
func (d *Defaults) ShowNotifications() bool {
	if d == nil {
		return false
	}
	return numAsBool(d.Raw["show notifications"])
}

// CloseTerminalOnDisconnect — defaults 'close terminal on disconnect' (легаси
// PACUtils.pm:2052 //= ”, по умолчанию ВЫКЛ): закрывать ли терминал при отключении
// (бэклог). Хранится в Raw.
func (d *Defaults) CloseTerminalOnDisconnect() bool {
	if d == nil {
		return false
	}
	return numAsBool(d.Raw["close terminal on disconnect"])
}

// numAsBool — легаси-конверсия сырого значения defaults в bool: любое ненулевое
// число / true / "1" / "true" — истина (для "terminal show status bar").
func numAsBool(v any) bool {
	switch n := v.(type) {
	case bool:
		return n
	case int:
		return n != 0
	case int64:
		return n != 0
	case float64:
		return n != 0
	case string:
		return n == "1" || strings.EqualFold(n, "true")
	}
	return false
}

// MaxRetryOnDisconnect — defaults 'max retry on disconnect' (легаси
// PACUtils.pm:2053, дефолт 50): лимит автопереподключений (M24). Ключ не
// типизирован в Defaults — хранится в Raw как неизвестный.
func (d *Defaults) MaxRetryOnDisconnect() int {
	const def = 50
	if d == nil {
		return def
	}
	v, ok := d.Raw["max retry on disconnect"]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n
		}
	case int64:
		if n > 0 {
			return int(n)
		}
	case float64:
		if n > 0 {
			return int(n)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && i > 0 {
			return i
		}
	}
	return def
}

// TitleOrName — заголовок вкладки: title с fallback на name (легаси: title //= name).
func (e *Environment) TitleOrName() string {
	if e.Title != "" {
		return e.Title
	}
	return e.Name
}

// ---- Сохранение/восстановление неизвестных ключей ----

func captureRaw(node *yaml.Node, known []string) (map[string]any, error) {
	var all map[string]any
	if err := node.Decode(&all); err != nil {
		return nil, err
	}
	for _, k := range known {
		delete(all, k)
	}
	return all, nil
}

func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	type plain Config
	if err := node.Decode((*plain)(c)); err != nil {
		return err
	}
	raw, err := captureRaw(node, []string{"defaults", "environments", "tmp", "config version", "config version change",
		"__PAC__EXPORTED__", "__PAC__EXPORTED__FULL__", "__PAC__EXPORTED__PARTIAL_CONF"})
	if err != nil {
		return err
	}
	c.Raw = raw
	return nil
}

// GroupConnections — рекурсивный сбор идентификаторов подключений-потомков группы
// root (промежуточные группы пропускаются; результат отсортирован для
// детерминированного порядка запуска). Бэклог: «двойной клик по группе = запустить
// все её подключения» + «Start Group» (легаси PACMain.pm:2917-2934).
func (c Config) GroupConnections(root string) []string {
	seen := map[string]bool{}
	ids := []string{}
	var collect func(string)
	collect = func(u string) {
		if seen[u] {
			return
		}
		seen[u] = true
		e, ok := c.Environments[u]
		if !ok {
			return
		}
		if e.IsGroup == 1 {
			for cu := range e.Children {
				collect(cu)
			}
			return
		}
		ids = append(ids, u)
	}
	collect(root)
	sort.Strings(ids)
	return ids
}

// marshalMerge — общая сборка YAML-мапы: типизированные поля (plain) поверх Raw
// (дедуп близнецов Config/Defaults/Environment.MarshalYAML — все три идентичны).
func marshalMerge(raw map[string]any, plain any) (any, error) {
	m := map[string]any{}
	for k, v := range raw {
		m[k] = v
	}
	val, err := yaml.Marshal(plain)
	if err != nil {
		return nil, err
	}
	var typed map[string]any
	if err := yaml.Unmarshal(val, &typed); err != nil {
		return nil, err
	}
	for k, v := range typed {
		m[k] = v
	}
	return m, nil
}

func (c Config) MarshalYAML() (any, error) {
	type plain Config
	return marshalMerge(c.Raw, plain(c))
}

func (d *Defaults) UnmarshalYAML(node *yaml.Node) error {
	d.seen = map[string]bool{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		d.seen[node.Content[i].Value] = true
	}
	type plain Defaults
	if err := node.Decode((*plain)(d)); err != nil {
		return err
	}
	raw, err := captureRaw(node, []string{"version", "config version", "gui password", "use gui password",
		"use gui password tray",
		"sudo password", "sudo show password", "auto accept key", "timeout connect", "timeout command",
		"command prompt", "username prompt", "password prompt", "hostkey changed prompt",
		"press any key prompt", "remote host changed prompt", "sudo prompt",
		"proxy", "proxy ip", "proxy port", "proxy user", "proxy pass",
		"jump ip", "jump port", "jump user", "jump pass", "jump key", "keepass",
		"keybindings", "global variables"})
	if err != nil {
		return err
	}
	d.Raw = raw
	return nil
}

func (d Defaults) MarshalYAML() (any, error) {
	type plain Defaults
	return marshalMerge(d.Raw, plain(d))
}

// legacyScalarDefaults — легаси-нормализация скалярных элементов коллекций
// (PACUtils.pm:2401,2436,2455,2484,2501,2520): yaml.v3 отвергал bare-строку
// внутри []map[string]any и конфиг целиком не загружался. valueField — поле,
// куда попадает скаляр (expect его не сохраняет — так в легаси).
var legacyScalarDefaults = map[string]struct {
	valueField string
	defaults   []struct{ key, val, tag string }
}{
	"variables": {"txt", []struct{ key, val, tag string }{
		{"hide", "0", "!!int"}, {"txt", "", "!!str"},
	}},
	"expect": {"", []struct{ key, val, tag string }{
		{"active", "1", "!!int"}, {"expect", "", "!!str"}, {"send", "", "!!str"},
		{"hidden", "0", "!!int"}, {"return", "1", "!!int"},
		{"on_match", "-1", "!!int"}, {"on_fail", "-1", "!!int"}, {"time_out", "-1", "!!int"},
	}},
	"local before": {"command", []struct{ key, val, tag string }{
		{"default", "1", "!!int"}, {"ask", "1", "!!int"}, {"command", "", "!!str"},
	}},
	"local after": {"command", []struct{ key, val, tag string }{
		{"default", "1", "!!int"}, {"ask", "1", "!!int"}, {"command", "", "!!str"},
	}},
	"local connected": {"txt", []struct{ key, val, tag string }{
		{"confirm", "0", "!!int"}, {"txt", "", "!!str"},
	}},
	"macros": {"txt", []struct{ key, val, tag string }{
		{"confirm", "0", "!!int"}, {"intro", "1", "!!int"}, {"txt", "", "!!str"},
	}},
}

// normalizeLegacyScalarCollections переписывает скалярные элементы коллекций
// окружения в map до декодирования в типизированную структуру.
func normalizeLegacyScalarCollections(node *yaml.Node) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		def, ok := legacyScalarDefaults[node.Content[i].Value]
		if !ok {
			continue
		}
		seq := node.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			continue
		}
		for j, el := range seq.Content {
			// AliasNode (*a) на якорный скаляр (&a 42): легаси YAML.pm
			// раскрывает alias до нормализации, и Perl видит скаляр
			// (PACUtils.pm:2401,2501) — нормализуем его так же (ag-j1r).
			// Alias на map не трогаем: он декодируется как map (легаси: ref).
			if el.Kind == yaml.AliasNode {
				if el.Alias != nil && el.Alias.Kind == yaml.ScalarNode {
					seq.Content[j] = legacyScalarMap(el.Alias, def.valueField, def.defaults)
				}
				continue
			}
			if el.Kind != yaml.ScalarNode {
				continue // map/более сложный узел — не скаляр (легаси: ref)
			}
			seq.Content[j] = legacyScalarMap(el, def.valueField, def.defaults)
		}
	}
}

// legacyScalarMap собирает map-узел с легаси-дефолтами и скаляром в valueField.
func legacyScalarMap(scalar *yaml.Node, valueField string, defaults []struct{ key, val, tag string }) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Line: scalar.Line, Column: scalar.Column}
	for _, d := range defaults {
		v := &yaml.Node{Kind: yaml.ScalarNode, Tag: d.tag, Value: d.val}
		if valueField != "" && d.key == valueField {
			// Значение скаляра ВСЕГДА строка: легаси Perl стрингифицирует
			// !!int/!!bool/!!float (YAML.pm отдаёт raw-текст, PACUtils.pm:2401
			// кладёт его в txt как есть), а потребители Go ждут string
			// (session/variables.go: m["txt"].(string)) — иначе значение
			// молча терялось (ag-9tj). null → '' (легаси $hash // '').
			v.Tag, v.Value = "!!str", scalar.Value
			if scalar.Tag == "!!null" {
				v.Value = ""
			}
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: d.key}, v)
	}
	return m
}

func (e *Environment) UnmarshalYAML(node *yaml.Node) error {
	normalizeLegacyScalarCollections(node)
	type plain Environment
	if err := node.Decode((*plain)(e)); err != nil {
		return err
	}
	var all map[string]any
	if err := node.Decode(&all); err != nil {
		return err
	}
	// присутствие ключа важно: Perl удаляет запись только при undef-имене,
	// `name: ''` остаётся (ревью major #2). COSMETIC-3: null (`name: ~`) —
	// это undef → NameSet=false (запись станет джанком), в отличие от пустой
	// строки `name: ''` (определённой, остаётся).
	if v, ok := all["name"]; ok && v != nil {
		e.NameSet = true
	}
	// бэклог Incognito: мост легаси 'connection options.randomSocksTunnel' →
	// EnableRandomSocks (PACEdit.pm:975 / PACMethod_ssh.pm:195). ВАЖНО: плоский
	// Go-ключ ('create random socks tunnel') приоритетнее (иначе «снять галку»
	// не закреплялось бы — легаси-вложенность тянула бы EnableRandomSocks=1
	// обратно при перезагрузке). Мост — только fallback для легаси-конфигов.
	if _, flatPresent := all["create random socks tunnel"]; !flatPresent {
		if co, ok := all["connection options"].(map[string]any); ok {
			if numAsBool(co["randomSocksTunnel"]) {
				e.EnableRandomSocks = 1
			}
			all["connection options"] = co // сохранить для Raw (round-trip легаси)
		}
	}
	for _, k := range environmentKnownKeys {
		delete(all, k)
	}
	e.Raw = all
	return nil
}

func (e Environment) MarshalYAML() (any, error) {
	type plain Environment
	return marshalMerge(e.Raw, plain(e))
}

var environmentKnownKeys = []string{
	"name", "description", "parent", "title", "ip", "port", "user", "pass",
	"search pass on KPX", "KPX title regexp", "KPX uuid", "method", "auth type", "auth fallback",
	"public key", "passphrase user", "passphrase", "options",
	"create random socks tunnel", "vnc security", "use secret service",
	"use proxy", "proxy ip", "proxy port", "proxy user", "proxy pass",
	"jump ip", "jump port", "jump user", "jump pass", "jump key",
	"save session logs", "session log pattern", "session logs folder", "session logs amount",
	"use prepend command", "prepend command", "use postpend command", "postpend command",
	"quote command", "quotepost command",
	"send string active", "send string txt", "send string intro", "send string every", "send string only when idle",
	"embed", "mac", "autoreconnect", "remove control chars", "startup launch",
	"startup script", "startup script name", "favourite", "autossh", "cluster", "use sudo",
	"_is_group", "_protected", "children",
	"variables", "screenshots", "local before", "expect", "local connected", "macros",
	"local after", "terminal options",
}
