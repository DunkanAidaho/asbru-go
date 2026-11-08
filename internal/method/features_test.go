package method

import "testing"

// M34: auth type — РОВНО 3 значения; допустимость зависит от диалекта.
func TestFeaturesAuthTypes(t *testing.T) {
	if !ValidAuthType(SSH, "publickey") {
		t.Error("SSH должен допускать publickey")
	}
	if !ValidAuthType(SSH, "userpass") || !ValidAuthType(SSH, "manual") {
		t.Error("SSH: userpass/manual")
	}
	if ValidAuthType(RDesktop, "publickey") {
		t.Error("RDP не должен допускать publickey (лев. PACUtils.pm:429)")
	}
	if !ValidAuthType(RDesktop, "userpass") || !ValidAuthType(RDesktop, "manual") {
		t.Error("RDP: userpass/manual")
	}
	if !ValidAuthType(VNC, "userpass") || ValidAuthType(VNC, "publickey") {
		t.Error("VNC: userpass, не publickey")
	}
	if !ValidAuthType(Generic, "manual") || ValidAuthType(Generic, "userpass") {
		t.Error("Generic: только manual (auth-область disabled)")
	}
	if !ValidAuthType(IBM3270, "manual") || ValidAuthType(IBM3270, "publickey") {
		t.Error("3270: только manual")
	}
	for _, d := range []Dialect{Telnet, FTP, WebDAV, SerialRemoteTTY} {
		if !ValidAuthType(d, "userpass") || ValidAuthType(d, "publickey") {
			t.Errorf("%s: userpass, не publickey", d)
		}
	}
}

func TestFeaturesPubKeyAndFallback(t *testing.T) {
	for _, d := range []Dialect{SSH, SFTP, MOSH} {
		f := FeaturesFor(d)
		if !f.HasPubKey || !f.HasAuthFallback {
			t.Errorf("%s: HasPubKey=%v HasAuthFallback=%v", d, f.HasPubKey, f.HasAuthFallback)
		}
	}
	if FeaturesFor(RDesktop).HasPubKey {
		t.Error("RDP не имеет publickey-полей")
	}
	if FeaturesFor(XFreeRDP).HasPubKey {
		t.Error("xfreerdp не имеет publickey-полей")
	}
}

// M34: OptionsFor — галки по диалекту.
func TestOptionsFor(t *testing.T) {
	ssh := OptionsFor(SSH)
	if len(ssh) < 5 {
		t.Fatalf("SSH галок = %d (мин 5)", len(ssh))
	}
	if ssh[0].Setting != "forwardX" {
		t.Errorf("SSH[0].Setting = %q", ssh[0].Setting)
	}
	xf := OptionsFor(XFreeRDP)
	if len(xf) == 0 {
		t.Fatal("xfreerdp без галок")
	}
	if !hasSetting(xf, "fullscreen") || !hasSetting(xf, "clipboard") {
		t.Errorf("xfreerdp галки = %#v", xf)
	}
	rd := OptionsFor(RDesktop)
	if !hasSetting(rd, "fullscreen") || !hasSetting(rd, "admin") {
		t.Errorf("rdesktop галки = %#v", rd)
	}
	if len(OptionsFor(Generic)) != 0 {
		t.Error("Generic без галок")
	}
}

func hasSetting(ts []OptionToggle, setting string) bool {
	for _, x := range ts {
		if x.Setting == setting {
			return true
		}
	}
	return false
}

// TogglePresent — есть ли токен галки в строке.
func TestTogglePresent(t *testing.T) {
	options := "-C -X -o \"ServerAliveInterval=5\""
	if !TogglePresent(options, OptionToggle{Tokens: []string{"-X"}}) {
		t.Error("-X должен быть найден")
	}
	if !TogglePresent(options, OptionToggle{Tokens: []string{"-C"}}) {
		t.Error("-C должен быть найден")
	}
	if TogglePresent(options, OptionToggle{Tokens: []string{"-A"}}) {
		t.Error("-A не должен быть найден")
	}
}

// Очный дебаг issue 1: командные опции (sudo/autoreconnect/проброс/…) — только
// для терминальных методов; RDP/VNC их не используют (иначе на RDP показывались
// SSH-галки).
func TestCommandCapable(t *testing.T) {
	noCmd := []Dialect{RDesktop, XFreeRDP, VNC}
	for _, d := range noCmd {
		if CommandCapable(d) {
			t.Errorf("CommandCapable(%s)=true, want false (графический)", d)
		}
	}
	yesCmd := []Dialect{SSH, SFTP, MOSH, Telnet, FTP, WebDAV, SerialRemoteTTY, Generic, LocalShell, SerialCu, IBM3270}
	for _, d := range yesCmd {
		if !CommandCapable(d) {
			t.Errorf("CommandCapable(%s)=false, want true (терминальный)", d)
		}
	}
}

// SetToggle — вкл/выкл токена без порчи остальной строки.
func TestSetToggle(t *testing.T) {
	base := "-o ServerAliveInterval=5"
	on, err := SetToggle(base, OptionToggle{Tokens: []string{"-X"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !TogglePresent(on, OptionToggle{Tokens: []string{"-X"}}) {
		t.Errorf("after on: %q (нет -X)", on)
	}
	if !TogglePresent(on, OptionToggle{Tokens: []string{"-o"}}) {
		t.Errorf("после on потерян -o: %q", on)
	}
	off, err := SetToggle(on, OptionToggle{Tokens: []string{"-X"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if TogglePresent(off, OptionToggle{Tokens: []string{"-X"}}) {
		t.Errorf("after off: %q (остался -X)", off)
	}
	if !TogglePresent(off, OptionToggle{Tokens: []string{"ServerAliveInterval=5"}}) {
		t.Errorf("после off потеряно значение: %q", off)
	}
}

// JoinArgs — обратный к SplitArgs: значение с пробелом переживает round-trip.
func TestJoinArgs(t *testing.T) {
	src := `-o "ServerAliveInterval=5" -X`
	toks := SplitArgs(src)
	joined := JoinArgs(toks)
	toks2 := SplitArgs(joined)
	if len(toks) != len(toks2) {
		t.Fatalf("roundtrip: %d != %d (%q)", len(toks), len(toks2), joined)
	}
	for i := range toks {
		if toks[i] != toks2[i] {
			t.Errorf("roundtrip %d: %q != %q", i, toks[i], toks2[i])
		}
	}
}

// M34: галки согласованы с конвертацией диалектов (rdToXf/xfToRd). Токены
// галки = то, что эмитит конвертация; переключение диалекта сохраняет значение.
func TestTogglesConsistentWithConvert(t *testing.T) {
	// xfreerdp +clipboard ↔ rdesktop -r clipboard:PRIMARYCLIPBOARD
	rd, unknown := ConvertOptions(XFreeRDP, RDesktop, "+clipboard")
	if len(unknown) != 0 {
		t.Errorf("unknown on convert: %v", unknown)
	}
	if rd != "-r clipboard:PRIMARYCLIPBOARD" {
		t.Errorf("rdesktop clipboard = %q", rd)
	}
	if !TogglePresent(rd, OptionToggle{Tokens: []string{"-r", "clipboard:PRIMARYCLIPBOARD"}}) {
		t.Errorf("TogglePresent rdesktop clipboard fail: %q", rd)
	}
	// обратно
	xf, _ := ConvertOptions(RDesktop, XFreeRDP, rd)
	if xf != "+clipboard" {
		t.Errorf("xfreerdp clipboard = %q", xf)
	}
	if !TogglePresent(xf, OptionToggle{Tokens: []string{"+clipboard"}}) {
		t.Errorf("TogglePresent xfreerdp clipboard fail: %q", xf)
	}
	// /f (эмитит rdToXf из -f) узнаётся галкой и MapRdpOptions -> fullscreen
	if !TogglePresent("/f", OptionToggle{Tokens: []string{"/f"}}) {
		t.Error("TogglePresent /f fail")
	}
	if m, _ := MapRdpOptions("/f"); func() bool { _, ok := m["fullscreen"]; return !ok }() {
		t.Errorf("MapRdpOptions(/f) нет fullscreen: %#v", m)
	}
	// галка xfreerdp fullscreen = /f согласована с галкой rdesktop fullscreen = -f
	xfFull := OptionsFor(XFreeRDP)
	if !hasTokenToggle(xfFull, "fullscreen", "/f") {
		t.Errorf("xfreerdp fullscreen токен не /f: %#v", xfFull)
	}
}

func hasTokenToggle(ts []OptionToggle, setting, token string) bool {
	for _, x := range ts {
		if x.Setting == setting {
			for _, t := range x.Tokens {
				if t == token {
					return true
				}
			}
		}
	}
	return false
}

// BLOCKER: ConvertOptions на НЕ-RDP паре диалектов (SSH→Telnet и т.п.) не должен
// паниковать (rule==nil → passthrough), а возвращать строку как есть.
func TestConvertOptionsNonRDPNoPanic(t *testing.T) {
	src := "-o ServerAliveInterval=5 -X"
	out, unknown := ConvertOptions(SSH, Telnet, src)
	if out != src {
		t.Errorf("non-RDP passthrough: %q != %q", out, src)
	}
	if len(unknown) != 0 {
		t.Errorf("unknown on passthrough: %v", unknown)
	}
	// RDP→SSH тоже passthrough (не паника).
	out, _ = ConvertOptions(RDesktop, SSH, "-f -z")
	if out != "-f -z" {
		t.Errorf("rdp→ssh passthrough: %q", out)
	}
}

// MAJOR: SetToggle на многотокенной rdesktop-буфере добавляет ВСЕ токены и не
// стирает общий префикс -r у других redirect'ов (sound/disk).
func TestSetToggleMultiTokenClipboard(t *testing.T) {
	cb := OptionToggle{Tokens: []string{"-r", "clipboard:PRIMARYCLIPBOARD"}}
	base := "-r sound:sys:0"
	on, err := SetToggle(base, cb, true)
	if err != nil {
		t.Fatal(err)
	}
	if !TogglePresent(on, cb) {
		t.Errorf("clipboard on: %q (нет группы)", on)
	}
	if !TogglePresent(on, OptionToggle{Tokens: []string{"-r", "sound:sys:0"}}) {
		t.Errorf("clipboard on стёр sound: %q", on)
	}
	// обратно: клипборд выкл — sound остаётся, буфер-группа удаляется.
	off, err := SetToggle(on, cb, false)
	if err != nil {
		t.Fatal(err)
	}
	if TogglePresent(off, cb) {
		t.Errorf("clipboard off: %q (остался буфер)", off)
	}
	if !TogglePresent(off, OptionToggle{Tokens: []string{"-r", "sound:sys:0"}}) {
		t.Errorf("clipboard off стёр sound: %q", off)
	}
}

// MINOR: JoinArgs/joinTokens — единый ре-квотер; круговая совместимость.
func TestJoinArgsUnified(t *testing.T) {
	src := `-r sound:sys:0 --x="a b"`
	if JoinArgs(SplitArgs(src)) != src {
		// допускаем нормализацию кавычек, но не потерю токенов
		toks := SplitArgs(src)
		round := SplitArgs(JoinArgs(toks))
		if len(toks) != len(round) {
			t.Fatalf("roundtrip: %d != %d", len(toks), len(round))
		}
	}
}

// M34/A: rdesktop -5/-4 (версия) и -r (редиректы) узнаются MapRdpOptions —
// не уходят в unknown (раньше шум «нераспознанные опции» на реальном конфиге).
func TestRdpMapVersionAndRedirectNotUnknown(t *testing.T) {
	s, unknown := MapRdpOptions("-5")
	if len(unknown) != 0 || s["rdpversion"] == "" {
		t.Errorf("-5: unknown=%v settings=%#v", unknown, s)
	}
	s2, u2 := MapRdpOptions("-4")
	if len(u2) != 0 || s2["rdpversion"] == "" {
		t.Errorf("-4: unknown=%v settings=%#v", u2, s2)
	}
	s3, u3 := MapRdpOptions("-r clipboard:PRIMARYCLIPBOARD")
	if len(u3) != 0 || s3["redirect"] != "clipboard:PRIMARYCLIPBOARD" {
		t.Errorf("-r: unknown=%v settings=%#v", u3, s3)
	}
	// два редиректа подряд не должны ронять парсинг/unknown-спам
	_, u4 := MapRdpOptions("-5 -r sound:sys:0 -r clipboard:PRIMARYCLIPBOARD")
	if len(u4) != 0 {
		t.Errorf("составные: unknown=%v", u4)
	}
}
