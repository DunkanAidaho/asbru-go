package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M19: GUI-пароль. Легаси хранит encrypt_hex(пароль) в defaults
// (PACUtils.pm:2035), проверка: decrypt == введённый (PACMain.pm:256).
func TestGuiPassSetCheck(t *testing.T) {
	d := &config.Defaults{UseGUIPassword: 1}
	if err := guiPassSet(d, "", "s3cret"); err != nil {
		t.Fatalf("первичная установка: %v", err)
	}
	if !guiPassCheck(d, "s3cret") {
		t.Fatal("верный пароль отклонён")
	}
	if guiPassCheck(d, "wrong") {
		t.Fatal("неверный пароль принят")
	}
	if err := guiPassSet(d, "wrong", "next"); err == nil {
		t.Fatal("смена с неверным текущим прошла")
	}
	if err := guiPassSet(d, "s3cret", "next"); err != nil {
		t.Fatalf("смена: %v", err)
	}
	if !guiPassCheck(d, "next") {
		t.Fatal("сменённый пароль не работает")
	}
}

// Пароль отключён → проверка не требуется, смена не требует текущего.
func TestGuiPassDisabled(t *testing.T) {
	d := &config.Defaults{}
	if !guiPassCheck(d, "anything") {
		t.Fatal("без защиты пароль не требуется")
	}
	if err := guiPassSet(d, "", "new"); err != nil {
		t.Fatal(err)
	}
}

// Хранится шифртекст, не плейн.
func TestGuiPassStoredEncrypted(t *testing.T) {
	d := &config.Defaults{UseGUIPassword: 1}
	_ = guiPassSet(d, "", "hunter2")
	if d.GUIPassword == "hunter2" || d.GUIPassword == "" {
		t.Fatalf("GUIPassword = %q", d.GUIPassword)
	}
}

// Реестр предпочтений: каждое поле проходит Get/Set-round-trip.
func TestPrefFieldsRoundTrip(t *testing.T) {
	for _, f := range prefFields() {
		d := &config.Defaults{}
		in := "значение"
		switch f.Kind {
		case prefCheck:
			in = "1"
		case prefNum:
			in = "101"
			if f.Key == "terminal transparency" { // проценты, кламп [0,100]
				in = "50"
			}
		case prefCombo:
			// комбо: валидный пункт списка (label==value); произвольная строка
			// мапилась бы к первому пункту и ломала round-trip.
			if len(f.Combo) > 0 {
				in = f.Combo[0]
			}
		}
		f.Set(d, in)
		if got := f.Get(d); got != in {
			t.Errorf("%s: round-trip = %q, want %q", f.Key, got, in)
		}
	}
}

// Покрываемость: реестр предпочтений обязан покрывать все скалярные поля
// defaults (gui password — отдельным потоком смены; version/config version —
// машинные; global variables — отдельный редактор).
func TestPrefFieldsCoverage(t *testing.T) {
	want := []string{
		"use gui password", "use gui password tray",
		"auto accept key", "timeout connect", "timeout command",
		"sudo password", "sudo show password",
		"command prompt", "username prompt", "password prompt",
		"hostkey changed prompt", "press any key prompt",
		"remote host changed prompt", "sudo prompt",
		"proxy", "proxy ip", "proxy port", "proxy user", "proxy pass",
		"jump ip", "jump port", "jump user", "jump pass", "jump key",
		"use_keepass", "database", "pathcli", "keyfile", "password",
	}
	got := map[string]bool{}
	for _, f := range prefFields() {
		got[f.Key] = true
	}
	for _, k := range want {
		if !got[k] {
			t.Errorf("нет поля %s", k)
		}
	}
}

// M29-ревью I3: в реестре prefFields не должно быть дубликатов ключей
// (две записи на один ключ = два источника правды для значения).
func TestPrefFieldsNoDuplicateKeys(t *testing.T) {
	seen := map[string]string{}
	for _, f := range prefFields() {
		if prev, ok := seen[f.Key]; ok {
			t.Errorf("дубликат ключа %q (вкладки %q и %q)", f.Key, prev, f.Tab)
		}
		seen[f.Key] = f.Tab
	}
}

// Применение значений: изменения ложатся в defaults, чистое поле не трогается.
func TestApplyPrefs(t *testing.T) {
	d := &config.Defaults{}
	vals := map[string]string{
		"timeout connect": "45",
		"proxy ip":        "192.0.2.1",
		"auto accept key": "0",
	}
	if err := applyPrefs(d, vals); err != nil {
		t.Fatal(err)
	}
	if int(d.TimeOutConnect) != 45 {
		t.Fatalf("timeout connect = %d", d.TimeOutConnect)
	}
	if d.ProxyIP != "192.0.2.1" {
		t.Fatalf("proxy ip = %q", d.ProxyIP)
	}
	if int(d.AutoAcceptKey) != 0 {
		t.Fatalf("auto accept key = %d", d.AutoAcceptKey)
	}
	if d.SudoPassword != "" {
		t.Fatalf("не тронутое поле изменилось: %q", d.SudoPassword)
	}
}

// M35: 'proxy' селектор (No/Proxy/Jump) пишется в defaults.proxy и читается
// обратно; empty → первый пункт (No).
func TestPrefFieldsProxySelector(t *testing.T) {
	var pf prefField
	for _, f := range prefFields() {
		if f.Key == "proxy" {
			pf = f
		}
	}
	if pf.Key == "" {
		t.Fatal("нет поля 'proxy' в prefFields")
	}
	if pf.Kind != prefCombo {
		t.Fatalf("поле 'proxy' не prefCombo: %v", pf.Kind)
	}
	if len(pf.Combo) != 3 || pf.Combo[0] != "No" || pf.Combo[1] != "Proxy" || pf.Combo[2] != "Jump" {
		t.Fatalf("пункты 'proxy': %v", pf.Combo)
	}
	d := &config.Defaults{}
	if got := pf.Get(d); got != "" {
		t.Fatalf("дефолт proxy = %q, хотим \"\"", got)
	}
	pf.Set(d, "Jump")
	if d.Proxy != "Jump" || pf.Get(d) != "Jump" {
		t.Fatalf("set proxy: def=%q get=%q, хотим Jump", d.Proxy, pf.Get(d))
	}
	// comboIndex: значение → индекс, "" / неизвестное → 0 (No).
	if comboIndex(pf.Combo, "Jump") != 2 || comboIndex(pf.Combo, "Proxy") != 1 ||
		comboIndex(pf.Combo, "") != 0 || comboIndex(pf.Combo, "bogus") != 0 {
		t.Fatalf("comboIndex: %d %d %d %d",
			comboIndex(pf.Combo, "Jump"), comboIndex(pf.Combo, "Proxy"),
			comboIndex(pf.Combo, ""), comboIndex(pf.Combo, "bogus"))
	}
}

// M19-ревью (средне): трей-гейт — восстановление требет пароль только при
// use gui password && use gui password tray (легаси PACMain.pm:2214/2347).
func TestGuiPassTrayRequired(t *testing.T) {
	cases := []struct {
		use, tray config.FlexInt
		want      bool
	}{
		{0, 0, false}, {1, 0, false}, {0, 1, false}, {1, 1, true},
	}
	for _, c := range cases {
		d := &config.Defaults{UseGUIPassword: c.use, UseGUIPasswordTray: c.tray}
		if got := guiPassTrayRequired(d); got != c.want {
			t.Errorf("use=%d tray=%d: yield %v, want %v", c.use, c.tray, got, c.want)
		}
	}
}
