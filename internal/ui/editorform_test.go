package ui

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M13: form↔env round-trip (все значимые поля схемы).
func TestFormRoundTrip(t *testing.T) {
	env := &config.Environment{
		Name: "web", IP: "10.0.0.5", Port: "22", User: "admin", Pass: "p",
		AuthType: "publickey", AuthFallback: "1", PublicKey: "~/.ssh/id_ed25519",
		Passphrase: "pph", Options: "-p 22",
		PrependCommand: "pre", PostpendCommand: "post",
		// Включающие флаги теперь выводятся из непустых полей (раньше
		// редактор их не сохранял, и prepend/postpend/send string не работали).
		UsePrependCommand: 1, UsePostpendCommand: 1, SendStringActive: 1,
		SendStringTxt: "top", SendStringIntro: "1", SendStringEvery: 4, SendStringOnlyWhenIdle: 1,
		UseSudo: 1, AutoReconnect: 1, Autossh: 1, UseProxy: 3,
		ProxyIP: "10.0.0.1", ProxyPort: "1080", ProxyUser: "pu", ProxyPass: "pp",
		JumpIP: "10.0.0.2", JumpPort: 22, JumpUser: "ju", JumpPass: "jp", JumpKey: "jk",
		Expect: []map[string]any{
			{"active": 1, "expect": "sudo:", "send": "s", "hidden": 1, "return": 1, "on_match": -1, "on_fail": -1, "time_out": -1},
		},
		Variables:         []map[string]any{{"name": "v1", "variable": "x"}},
		Description:       "web server",
		Title:             "web-1",
		KPXUUID:           "abc-123",
		StartUpLaunch:     1,
		StartUpScript:     "/tmp/setup.sh",
		StartUpScriptName: "setup",
		VNCEncryption:     "vencrypt",
		UseSecretService:  1,
		Macros: []map[string]any{
			{"description": "restart", "txt": "sudo systemctl restart nginx"},
		},
	}
	f := formFromEnv(env)
	out := &config.Environment{}
	applyForm(out, f)
	if !reflect.DeepEqual(out, env) {
		t.Fatalf("round-trip: want %#v\ngot %#v", env, out)
	}
}

// Редактор писал только тексты команд, но не включающие флаги, которыми
// гейтится runtime (use prepend/postpend command, send string active) — поля
// тихо не работали. Флаги выводятся из непустых полей.
func TestApplyFormDerivesCommandFlags(t *testing.T) {
	out := &config.Environment{}
	applyForm(out, connForm{
		Prepend: "env A=1", Postpend: "   ", SendString: "uptime", SendStringEvery: 5,
	})
	if out.UsePrependCommand != 1 {
		t.Errorf("use prepend command = %d, хотим 1 (поле непустое)", out.UsePrependCommand)
	}
	if out.UsePostpendCommand != 0 {
		t.Errorf("use postpend command = %d, хотим 0 (поле пустое)", out.UsePostpendCommand)
	}
	if out.SendStringActive != 1 {
		t.Errorf("send string active = %d, хотим 1", out.SendStringActive)
	}
	if out.SendStringIntro != "1" {
		t.Errorf("send string intro = %q, хотим \"1\" (иначе нет перевода строки)", out.SendStringIntro)
	}
	if out.SendStringEvery != 5 {
		t.Errorf("send string every = %d", out.SendStringEvery)
	}
}

// Пустые поля выключают режимы; явный легаси-флаг intro не перетирается.
func TestApplyFormCommandFlagsClearedAndIntroPreserved(t *testing.T) {
	out := &config.Environment{
		UsePrependCommand: 1, UsePostpendCommand: 1, SendStringActive: 1, SendStringIntro: "1",
	}
	applyForm(out, connForm{})
	if out.UsePrependCommand != 0 || out.UsePostpendCommand != 0 || out.SendStringActive != 0 {
		t.Errorf("флаги не сброшены: pre=%d post=%d send=%d",
			out.UsePrependCommand, out.UsePostpendCommand, out.SendStringActive)
	}
	if out.SendStringIntro != "" {
		t.Errorf("intro = %q, хотим пусто при выключенной отправке", out.SendStringIntro)
	}

	explicit := &config.Environment{SendStringIntro: "0"}
	applyForm(explicit, connForm{SendString: "who"})
	if explicit.SendStringIntro != "0" {
		t.Errorf("явный intro перетёрт: %q", explicit.SendStringIntro)
	}
}

// Quote-флаги легаси не имеют смысла в argv-пути (нет sh): редактор их больше
// не пишет, но существующие значения не затирает (совместимость конфигов).
func TestApplyFormKeepsLegacyQuoteFlags(t *testing.T) {
	env := &config.Environment{QuoteCommand: 1, QuotePostCommand: 1}
	applyForm(env, connForm{Prepend: "x", Postpend: "y"})
	if env.QuoteCommand != 1 || env.QuotePostCommand != 1 {
		t.Errorf("легаси quote-флаги затёрты: %d/%d", env.QuoteCommand, env.QuotePostCommand)
	}
}

// M13: formToExpect сохраняет легаси-ключи и семантику sentinel'ов.
func TestExpectRowsRoundTrip(t *testing.T) {
	env := &config.Environment{Expect: []map[string]any{
		{"active": 0, "expect": "pw:", "on_match": 2, "on_fail": -2, "time_out": 5},
	}}
	f := formFromEnv(env)
	rows := f.ExpectRows
	if len(rows) != 1 || rows[0].Active || rows[0].OnMatch != 2 || rows[0].OnFail != -2 || rows[0].TimeOut != 5 {
		t.Fatalf("rows: %+v", rows)
	}
	got := formToExpect(rows)
	want := []map[string]any{
		{"active": 0, "expect": "pw:", "send": "", "hidden": 0, "return": 1,
			"on_match": 2, "on_fail": -2, "time_out": 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expect: %v", got)
	}
}

// M13: пустая форма не трогает неизвестные ключи (Raw).
func TestApplyPreservesRaw(t *testing.T) {
	env := &config.Environment{Raw: map[string]any{"custom-key": 1}}
	applyForm(env, connForm{Name: "x"})
	if env.Raw["custom-key"] != 1 {
		t.Fatalf("raw потерян: %v", env.Raw)
	}
}

// M13-ревью H2: пустые/мусорные числовые ячейки НЕ затирают дефолты
// (Atoi("")→0 раньше давал time_out=0 → закрытие сессии на первом тике).
func TestExpectRowFromCellsDefaults(t *testing.T) {
	prev := expectRow{Active: true, Return: true, OnMatch: -1, OnFail: -1, TimeOut: -1}
	cells := map[int]string{
		colExpect: "pw:", colSend: "secret", colReturn: "0",
		colOnMatch: "2", colOnFail: "", colTimeOut: "",
	}
	r := expectRowFromCells(cells, prev)
	if r.OnMatch != 2 || r.OnFail != -1 || r.TimeOut != -1 || r.Return || r.Expect != "pw:" {
		t.Fatalf("cells: %+v", r)
	}
	// явный 0 — сохраняется осознанным вводом
	cells[colTimeOut] = "0"
	r = expectRowFromCells(cells, prev)
	if r.TimeOut != 0 {
		t.Fatalf("явный 0: %+v", r)
	}
}

// M13-ревью M2b: переменные не теряют ключи (hide/variable/type), пустые строки не пишутся.
func TestVariableFromCellsPreservesKeys(t *testing.T) {
	orig := map[string]any{"hide": 1, "variable": "VAR"}
	m, ok := variableFromCells(orig, "v1", "val")
	if !ok || m["hide"] != 1 || m["variable"] != "VAR" || m["name"] != "v1" || m["txt"] != "val" {
		t.Fatalf("var: %+v (%v)", m, ok)
	}
	if _, ok := variableFromCells(orig, "", ""); ok {
		t.Fatal("пустая строка записана")
	}
}

// M13-ревью L1: atoiOr на мусоре держит прежнее значение.
func TestAtoiOr(t *testing.T) {
	if v := atoiOr("zz", 5); v != 5 {
		t.Fatalf("мусор: %d", v)
	}
	if v := atoiOr("9", 5); v != 9 {
		t.Fatalf("число: %d", v)
	}
}

// M13-ре-ревью: mergeVariables — реальный путь Save: исходные ключи
// сохраняются по индексу, пустые строки отбрасываются.
func TestMergeVariablesPreservesKeys(t *testing.T) {
	old := []map[string]any{
		{"hide": 1, "variable": "VAR"},
		{"txt": "keep", "hide": 0},
	}
	cells := [][2]string{{"v1", "val"}, {"", "only-txt"}, {"", ""}}
	out := mergeVariables(old, cells)
	if len(out) != 2 {
		t.Fatalf("merge: %+v", out)
	}
	if out[0]["hide"] != 1 || out[0]["variable"] != "VAR" || out[0]["name"] != "v1" {
		t.Fatalf("первая: %+v", out[0])
	}
	if out[1]["hide"] != 0 || out[1]["txt"] != "only-txt" {
		t.Fatalf("вторая: %+v", out[1])
	}
}

// M29-остаток (#2): mergeMacros — Save макросов сохраняет незатронутые
// ключи (menu/expand/check…), пустые строки отбрасываются.
func TestMergeMacrosPreservesKeys(t *testing.T) {
	old := []map[string]any{
		{"menu": 1, "expand": 1, "check": 0},
	}
	cells := [][2]string{{"restart", "sudo systemctl restart nginx"}}
	out := mergeMacros(old, cells)
	if len(out) != 1 {
		t.Fatalf("merge: %+v", out)
	}
	if out[0]["menu"] != 1 || out[0]["expand"] != 1 || out[0]["check"] != 0 {
		t.Fatalf("потеряны ключи: %+v", out[0])
	}
	if out[0]["description"] != "restart" || out[0]["txt"] != "sudo systemctl restart nginx" ||
		out[0]["command"] != "sudo systemctl restart nginx" {
		t.Fatalf("содержимое: %+v", out[0])
	}
	if _, ok := macroFromCells(nil, "", ""); ok {
		t.Fatal("пустая строка макроса записана")
	}
}

// B1: «Тип» соединения должен сохраняться — formFromEnv→applyForm не теряют Method.
func TestFormMethodRoundTrip(t *testing.T) {
	env := &config.Environment{Name: "web", Method: "Telnet"}
	f := formFromEnv(env)
	out := &config.Environment{}
	applyForm(out, f)
	if out.Method != "Telnet" {
		t.Fatalf("Method = %q, want Telnet (Тип должен сохраняться при редактировании)", out.Method)
	}
	if out.Name != "web" {
		t.Fatalf("Name = %q, want web", out.Name)
	}
}

// Новое соединение создаётся без AuthFallback (""), а combo редактора содержит
// только {"0","1"}: SetCurrentText("") оставлял индекс 0 → doSave писал "0" и
// тихо выключал парольный fallback. Форма обязана отдавать "1" для пустого и
// нераспознанного значения (как Sanitize / легаси //= 1).
func TestFormFromEnvAuthFallbackDefault(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "1"},  // отсутствует/пусто
		{"0", "0"}, // явно выключено — сохраняем
		{"1", "1"}, // явно включено — сохраняем
		{"garbage", "1"},
	}
	for _, c := range cases {
		f := formFromEnv(&config.Environment{Name: "n", AuthFallback: c.in})
		if f.AuthFallback != c.want {
			t.Errorf("formFromEnv(AuthFallback=%q) = %q, want %q", c.in, f.AuthFallback, c.want)
		}
	}
}
