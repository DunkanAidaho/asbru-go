package ui

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M21: легаси-запись AltCtrl+x → Qt Ctrl+Alt+X (ввод-формат легаси:
// порядок модификаторов как в конфиге, не как в stringifyKeybind).
func TestLegacyToQt(t *testing.T) {
	cases := map[string]string{
		"Alt+n":     "Alt+N",
		"Alt+N":     "Alt+N",
		"AltCtrl+x": "Ctrl+Alt+X",
		"AltCtrl+X": "Ctrl+Alt+X",
		"Ctrl+i":    "Ctrl+I",
		"Alt+F5":    "Alt+F5",
		"F5":        "F5",
		"":          "",
	}
	for in, want := range cases {
		if got := legacyToQt(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// M21: flatten defaults.keybindings → seq+action; неподдерживаемые действия
// отсеиваются (reset/reset-clear/sftp — задел, не регистрируются).
func TestBindingActions(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"pacmain": {"Alt+n": {"Main Window", "showconnections", "Show/Hide connections list"}},
		"terminal": {
			"Alt+e":     {"Terminal", "edit_node", "Edit connection"},
			"AltCtrl+x": {"Terminal", "reset", "Reset Terminal"},
			"AltCtrl+s": {"Terminal", "sftp", "Open SFTP session"},
		},
		"treeConnections": {"Alt+c": {"Connections Tree", "connect_node", "Connect selected node"}},
	}}}
	got := bindingActions(cfg)
	// Batch B: sftp теперь поддержан (не отфильтрован); reset — всё ещё задел.
	want := []hotkey{
		{Seq: "Alt+c", Action: "connect_node"},
		{Seq: "Alt+e", Action: "edit_node"},
		{Seq: "Alt+n", Action: "showconnections"},
		{Seq: "AltCtrl+s", Action: "sftp"},
	}
	if len(got) != len(want) {
		t.Fatalf("bindings: %+v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d]: %+v, want %+v", i, got[i], w)
		}
	}
}

// M21: Alt+1..9 — встроенные переключения вкладок терминалов.
func TestAltDigitTabs(t *testing.T) {
	if got := altDigitTabSeq(3); got != "Alt+3" {
		t.Fatalf("seq: %q", got)
	}
	if altDigitTabSeq(0) != "" {
		t.Fatal("0 не в диапазоне")
	}
	if altDigitTabSeq(10) != "" {
		t.Fatal("10 не в диапазоне")
	}
}

// Batch A hotkeys: new tree actions are now supported and survive the filter.
func TestSupportedActionsTreeBatchA(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"treeConnections": {
			"Ctrl+r": {"Connections Tree", "expand_all", "Expand"},
			"Ctrl+t": {"Connections Tree", "collaps_all", "Collapse"},
			"Ctrl+d": {"Connections Tree", "clone", "Clone"},
			"Alt+r":  {"Connections Tree", "protection", "Protect"},
			"F2":     {"Connections Tree", "rename", "Rename"},
			"Ctrl+q": {"Terminal", "reset", "Reset"}, // задел; не в support → отфильтрован
		},
	}}}
	acts := bindingActions(cfg)
	if len(acts) != 5 {
		t.Fatalf("bindingActions = %d (хотим 5 дерево-действий; reset отфильтрован)", len(acts))
	}
	seen := map[string]bool{}
	for _, a := range acts {
		seen[a.Action] = true
	}
	for _, want := range []string{"expand_all", "collaps_all", "clone", "protection", "rename"} {
		if !seen[want] {
			t.Fatalf("не нашли %q: %#v", want, seen)
		}
	}
	if seen["reset"] {
		t.Fatal("reset не должен быть в support (задел)")
	}
}

// Batch C: keybindingsList — активные привязки (фильтр поддержки) в порядке
// (Window, Action, Key); показывает окно/действие/клавишу/описание.
func TestKeybindingsList(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"pacmain": {
			"Ctrl+q": {"Main Window", "quit", "Exit Ásbrú"},
			"Ctrl+T": {"Main Window", "localshell", "Open a local shell"},
		},
		"terminal": {
			"F11":    {"Terminal", "fullscreen", "Go full screen"},
			"Ctrl+q": {"Terminal", "quit", "Exit Ásbrú"},
		},
		"tabs": {
			"Ctrl+Page_Down": {"Tabs", "next", "Next Tab"},
			"Ctrl+Page_Up":   {"Tabs", "previous", "Previous Tab"},
			"Ctrl+Tab":       {"Tabs", "last", "Last focused Tab"},
		},
		"treeConnections": {
			"Alt+n":  {"Connections Tree", "showconnections", "Show/Hide"},
			"Ctrl+e": {"Connections Tree", "bogus_action", "filtered"}, // не поддержан
		},
	}}}
	got := keybindingsList(cfg)
	// ожидаем все поддержанные, по (Window, Action, Key); bogus отфильтрован.
	want := []kbEntry{
		{Window: "Connections Tree", Action: "showconnections", Key: "Alt+n", Desc: "Show/Hide", App: "treeConnections", Orig: "Alt+n"},
		{Window: "Main Window", Action: "localshell", Key: "Ctrl+T", Desc: "Open a local shell", App: "pacmain", Orig: "Ctrl+T"},
		{Window: "Main Window", Action: "quit", Key: "Ctrl+q", Desc: "Exit Ásbrú", App: "pacmain", Orig: "Ctrl+q"},
		{Window: "Tabs", Action: "last", Key: "Ctrl+Tab", Desc: "Last focused Tab", App: "tabs", Orig: "Ctrl+Tab"},
		{Window: "Tabs", Action: "next", Key: "Ctrl+Page_Down", Desc: "Next Tab", App: "tabs", Orig: "Ctrl+Page_Down"},
		{Window: "Tabs", Action: "previous", Key: "Ctrl+Page_Up", Desc: "Previous Tab", App: "tabs", Orig: "Ctrl+Page_Up"},
		{Window: "Terminal", Action: "fullscreen", Key: "F11", Desc: "Go full screen", App: "terminal", Orig: "F11"},
		{Window: "Terminal", Action: "quit", Key: "Ctrl+q", Desc: "Exit Ásbrú", App: "terminal", Orig: "Ctrl+q"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keybindingsList:\n got %#v\nwant %#v", got, want)
	}
}

// KeyBindings-редактор: applyKeyBindings — перенос на новый Key, пустой → удаление,
// неподдержанные/неизменённые сохраняются.
func TestApplyKeyBindings(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"terminal": {
			"Ctrl+q": {"Terminal", "quit", "Exit"},
			"F5":     {"Terminal", "reset", "Reset"}, // unsupported — сохранить
		},
	}}}
	// переносим quit на Ctrl+Q, удаляем localshell (нет в cfg — не трогаем),
	// оставляем reset как есть.
	applyKeyBindings(cfg, []kbEntry{
		{App: "terminal", Orig: "Ctrl+q", Action: "quit", Key: "Ctrl+Q"},
	})
	kb := cfg.Defaults.KeyBindings["terminal"]
	if _, ok := kb["Ctrl+q"]; ok {
		t.Fatal("старый Ctrl+q остался")
	}
	if got := kb["Ctrl+Q"]; len(got) != 3 || got[1] != "quit" {
		t.Fatalf("новый Ctrl+Q: %v", got)
	}
	if _, ok := kb["F5"]; !ok {
		t.Fatal("неподдержанная reset потеряна")
	}
	// пустой Key → удалить binding.
	applyKeyBindings(cfg, []kbEntry{{App: "terminal", Orig: "Ctrl+Q", Action: "quit", Key: ""}})
	if _, ok := kb["Ctrl+Q"]; ok {
		t.Fatal("пустой Key не удалил binding")
	}
	if _, ok := kb["F5"]; !ok {
		t.Fatal("reset потеряна после удаления")
	}
}

// KeyBindings-ревью (MAJOR): смена Key на ЗАНЯТЫЙ другим (не-вакуируемым)
// биндингом не должна перезаписывать его (иначе теряется fullscreen).
func TestApplyKeyBindingsCollision(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"terminal": {
			"Ctrl+q": {"Terminal", "quit", "Exit"},
			"F11":    {"Terminal", "fullscreen", "Full screen"},
		},
	}}}
	// quit → F11 (занят fullscreen, не редактируется) → откат на Ctrl+q.
	applyKeyBindings(cfg, []kbEntry{{App: "terminal", Orig: "Ctrl+q", Action: "quit", Key: "F11"}})
	kb := cfg.Defaults.KeyBindings["terminal"]
	if _, ok := kb["F11"]; !ok {
		t.Fatal("fullscreen потерян (перезаписан)")
	}
	if got := kb["F11"]; len(got) != 3 || got[1] != "fullscreen" {
		t.Fatalf("fullscreen перезаписан: %v", got)
	}
	if _, ok := kb["Ctrl+q"]; !ok {
		t.Fatal("quit не вернулся на Ctrl+q (откат не сработал)")
	}
	// своп: quit→F11, fullscreen→Ctrl+q (оба вакуируются) → переезжают оба.
	applyKeyBindings(cfg, []kbEntry{
		{App: "terminal", Orig: "Ctrl+q", Action: "quit", Key: "F11"},
		{App: "terminal", Orig: "F11", Action: "fullscreen", Key: "Ctrl+q"},
	})
	if got := kb["F11"]; len(got) < 2 || got[1] != "quit" {
		t.Fatalf("своп quit: %v", got)
	}
	if got := kb["Ctrl+q"]; len(got) < 2 || got[1] != "fullscreen" {
		t.Fatalf("своп fullscreen: %v", got)
	}
}

// KeyBindings-ревью (MAJOR): каскад a:A→X, x:X→Y (Y занят non-vacated) — ни один
// биндинг не теряется (консервативный fixpoint; цепочечные переезды не
// исполняются, но БЕЗ перезаписи).
func TestApplyKeyBindingsCascade(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"t": {
			"A": {"T", "acta", "a"},
			"X": {"T", "actx", "x"},
			"Y": {"T", "acty", "y"},
		},
	}}}
	applyKeyBindings(cfg, []kbEntry{
		{App: "t", Orig: "A", Action: "acta", Key: "X"},
		{App: "t", Orig: "X", Action: "actx", Key: "Y"},
	})
	kb := cfg.Defaults.KeyBindings["t"]
	// все три сохраняются (acta остаётся на A, так как X занят цепочкой).
	for k, act := range map[string]string{"A": "acta", "X": "actx", "Y": "acty"} {
		if got := kb[k]; len(got) < 2 || got[1] != act {
			t.Fatalf("каскад: %s = %v, want %s", k, got, act)
		}
	}
}

// Batch C: все глобальные действия объявлены поддержанными (не фильтруются).
func TestBatchCActionsSupported(t *testing.T) {
	for _, a := range []string{"quit", "fullscreen", "next", "previous", "last",
		"infotab", "localshell", "showkeybindings"} {
		if !supportedActions[a] {
			t.Errorf("Batch C действие %q не в supportedActions", a)
		}
	}
}

// Batch C-ревью: undef-заглушки (undef-infotab) не биндятся и не показываются.
func TestUndefPlaceholderSkipped(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"tabs": {
			"undef-infotab":  {"Tabs", "infotab", "Go to Info Tab"},
			"Ctrl+Page_Down": {"Tabs", "next", "Next Tab"},
		},
	}}}
	got := bindingActions(cfg)
	if len(got) != 1 || got[0].Seq != "Ctrl+Page_Down" {
		t.Fatalf("bindingActions (undef пропущен): %+v", got)
	}
	kl := keybindingsList(cfg)
	if len(kl) != 1 || kl[0].Key != "Ctrl+Page_Down" {
		t.Fatalf("keybindingsList (undef пропущен): %+v", kl)
	}
}

// KeyBindings-ревью (MAJOR): 3-mover F→E, A→B, B→E — ни один не теряется
// (F забирает свободный E; B не может (E занят) → остаётся; A не может (B занят)
// → остаётся).
func TestApplyKeyBindings3Mover(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"t": {
			"A": {"T", "a", "a"},
			"B": {"T", "b", "b"},
			"F": {"T", "f", "f"},
		},
	}}}
	applyKeyBindings(cfg, []kbEntry{
		{App: "t", Orig: "F", Action: "f", Key: "E"},
		{App: "t", Orig: "A", Action: "a", Key: "B"},
		{App: "t", Orig: "B", Action: "b", Key: "E"},
	})
	kb := cfg.Defaults.KeyBindings["t"]
	for k, act := range map[string]string{"A": "a", "B": "b", "E": "f"} {
		got := kb[k]
		if len(got) == 0 || got[1] != act {
			t.Fatalf("3-mover: %s = %v, want %s", k, got, act)
		}
	}
}

// KeyBindings-ревью (MAJOR): цепочка-в-своп E→B, B→C, C→B — батч НЕ разрешим
// без потерь (E-лист на ключ цикла) → ОТБРАСЫВАЕТСЯ целиком (no-op), ничего не
// теряется.
func TestApplyKeyBindingsChainIntoSwap(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"t": {
			"B": {"T", "b", "b"},
			"C": {"T", "c", "c"},
			"E": {"T", "e", "e"},
			"F": {"T", "f", "f"},
		},
	}}}
	before := map[string][]string{}
	for k, v := range cfg.Defaults.KeyBindings["t"] {
		before[k] = v
	}
	applyKeyBindings(cfg, []kbEntry{
		{App: "t", Orig: "E", Action: "e", Key: "B"},
		{App: "t", Orig: "B", Action: "b", Key: "C"},
		{App: "t", Orig: "C", Action: "c", Key: "B"},
	})
	got := cfg.Defaults.KeyBindings["t"]
	for k, act := range map[string]string{"B": "b", "C": "c", "E": "e", "F": "f"} {
		if v := got[k]; len(v) == 0 || v[1] != act {
			t.Fatalf("chain-into-swap: %s = %v, want %s", k, v, act)
		}
	}
	if len(got) != len(before) {
		t.Fatalf("число биндингов изменилось: %d → %d", len(before), len(got))
	}
}

// Живой баг 2026-10-14 (ревью B): legacyToQt не знает токен «CtrlShift» —
// засеянный closealltabs (CtrlShift+F4) превращался в невалидный
// «CTRLSHIFT+F4» и никогда не срабатывал. Плюс мультибуквенные клавиши
// (Page_Down/Page_Up) ToUpper ломал (Qt ждёт PgDown/PgUp).
func TestLegacyToQtCtrlShiftAndNamedKeys(t *testing.T) {
	cases := map[string]string{
		"CtrlShift+F4":   "Ctrl+Shift+F4",
		"Ctrl+Page_Down": "Ctrl+PgDown",
		"Ctrl+Page_Up":   "Ctrl+PgUp",
		"Ctrl+Insert":    "Ctrl+Insert",
		"Ctrl+Return":    "Ctrl+Return",
	}
	for in, want := range cases {
		if got := legacyToQt(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// Живой баг (ревью B): дубли seq между секциями ставили два одинаковых
// QShortcut — Qt объявляет их ambiguous и НЕ СРАБАТЫВАЕТ НИ ОДИН (на
// Perl-конфигах так умирали Alt+N и Alt+E). bindingActions обязан
// дедуплицировать по seq (оставлять первый, по (Seq,Action) сортировка
// детерминирует какой).
func TestBindingActionsDedupesSeq(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: map[string]map[string][]string{
		"pacmain":   {"Alt+n": {"Main Window", "showconnections", "d"}},
		"terminal":  {"Alt+n": {"Terminal", "showconnections", "d"}},
		"treeConn2": {"Alt+n": {"X", "showconnections", "d"}},
	}}}
	got := bindingActions(cfg)
	n := 0
	for _, hk := range got {
		if hk.Seq == "Alt+n" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("seq Alt+n встретился %d раз, ожидался 1 (Qt-ambiguous)", n)
	}
}

// Живой баг (ревью B, Critical): миграция переносила только часть Perl-таблицы
// — на реальных конфигах оставались supported-записи на голом Ctrl+буква
// (pacmain Ctrl+T localshell, terminal Ctrl+I infotab), и они съедали
// transpose-chars/Tab в bash. Инвариант: ПОЛНАЯ Perl-таблица после Sanitize
// не даёт ни одной bindingActions-записи на голом Ctrl+<буква>.
// NB: в Qt 6.12 «Ctrl+C» матчит ГОЛЫЙ Ctrl+C (заглавная буква Shift не
// добавляет — проверено equality-пробником), поэтому регистр не важен.
func TestMigratedPerlTableHasNoBareCtrlLetter(t *testing.T) {
	perl := map[string]map[string][]string{
		"pacmain": {
			"Alt+n":  {"Main Window", "showconnections", "d"},
			"Ctrl+f": {"Main Window", "find", "d"},
			"Ctrl+q": {"Main Window", "quit", "d"},
			"Ctrl+T": {"Main Window", "localshell", "d"},
		},
		"pactabs": {
			"Ctrl+F4":        {"Tabs", "close", "d"},
			"Ctrl+Tab":       {"Tabs", "last", "d"},
			"Ctrl+Page_Down": {"Tabs", "next", "d"},
			"Ctrl+Page_Up":   {"Tabs", "previous", "d"},
		},
		"terminal": {
			"Ctrl+Return": {"Terminal", "start", "d"},
			"AltCtrl+x":   {"Terminal", "reset", "d"},
			"AltCtrl+X":   {"Terminal", "reset-clear", "d"},
			"Ctrl+C":      {"Terminal", "copy", "d"},
			"Ctrl+V":      {"Terminal", "paste", "d"},
			"Ctrl+Insert": {"Terminal", "copy", "d"},
			"Ctrl+p":      {"Terminal", "paste-passwd", "d"},
			"Ctrl+b":      {"Terminal", "paste-delete", "d"},
			"Ctrl+g":      {"Terminal", "hostname", "d"},
			"Ctrl+w":      {"Terminal", "close", "d"},
			"Ctrl+W":      {"Terminal", "disconnect", "d"},
			"Ctrl+q":      {"Terminal", "quit", "d"},
			"Ctrl+f":      {"Terminal", "find", "d"},
			"Ctrl+N":      {"Terminal", "close-disconected", "d"},
			"Ctrl+D":      {"Terminal", "duplicate", "d"},
			"Ctrl+R":      {"Terminal", "restart", "d"},
			"Ctrl+I":      {"Terminal", "infotab", "d"},
			"Ctrl+F3":     {"Terminal", "find-terminal", "d"},
			"AltCtrl+s":   {"Terminal", "sftp", "d"},
		},
		"treeConnections": {
			"Alt+c":  {"Connections Tree", "connect_node", "d"},
			"Ctrl+f": {"Connections Tree", "find", "d"},
			"Ctrl+r": {"Connections Tree", "expand_all", "d"},
			"Ctrl+t": {"Connections Tree", "collaps_all", "d"},
			"Ctrl+d": {"Connections Tree", "clone", "d"},
			"Ctrl+c": {"Connections Tree", "copy", "d"},
			"Ctrl+x": {"Connections Tree", "cut", "d"},
			"Ctrl+v": {"Connections Tree", "paste", "d"},
			"Alt+r":  {"Connections Tree", "protection", "d"},
			"F2":     {"Connections Tree", "rename", "d"},
		},
	}
	cfg := &config.Config{Defaults: config.Defaults{KeyBindings: perl}, Environments: map[string]config.Environment{}}
	config.Sanitize(cfg)
	bare := map[string]bool{"Ctrl+C": true, "Ctrl+V": true, "Ctrl+Q": true, "Ctrl+F": true,
		"Ctrl+T": true, "Ctrl+I": true, "Ctrl+N": true, "Ctrl+D": true, "Ctrl+W": true,
		"Ctrl+R": true, "Ctrl+G": true, "Ctrl+P": true, "Ctrl+B": true}
	for _, hk := range bindingActions(cfg) {
		if bare[hk.Seq] {
			t.Errorf("после миграции остался supported-биндинг на голом шелловом сочетании: %s → %s", hk.Seq, hk.Action)
		}
	}
}

// Правка привязок должна пересобирать QShortcut без перезапуска приложения.
func TestReinstallHotkeysHook(t *testing.T) {
	called := 0
	old := hotkeyReinstall
	hotkeyReinstall = func() { called++ }
	defer func() { hotkeyReinstall = old }()
	reinstallHotkeys()
	if called != 1 {
		t.Fatalf("хук переустановки вызван %d раз, хотим 1", called)
	}
	// нет зарегистрированного хука (тесты без Qt) — no-op, без паники.
	hotkeyReinstall = nil
	reinstallHotkeys()
}

// showKeybindingsEditor обязан переустанавливать шорткаты после applyKeyBindings.
func TestShowKeybindingsEditorReinstalls(t *testing.T) {
	src, err := os.ReadFile("hotkeys.go")
	if err != nil {
		t.Fatalf("не читается hotkeys.go: %v", err)
	}
	body := uiFuncBody(t, string(src), "func showKeybindingsEditor(")
	i := strings.Index(body, "applyKeyBindings(cfg, edits)")
	if i < 0 {
		t.Fatal("в редакторе нет applyKeyBindings")
	}
	if !strings.Contains(body[i:], "reinstallHotkeys()") {
		t.Error("после сохранения привязок шорткаты не переустанавливаются")
	}
}
