package method

import "testing"

// Отображение display-имя ⇄ диалект (ключи конфига — те же строки, что у _METHODS).
func TestDisplayNameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		want Dialect
	}{
		{"SSH", SSH},
		{"SFTP", SFTP},
		{"Telnet", Telnet},
		{"FTP", FTP},
		{"WebDAV", WebDAV},
		{"MOSH", MOSH},
		{"Serial (cu)", SerialCu},
		{"Serial (remote-tty)", SerialRemoteTTY},
		{"IBM 3270/5250", IBM3270},
		{"RDP (rdesktop)", RDesktop},
		{"RDP (xfreerdp)", XFreeRDP},
		{"VNC", VNC},
		{"Generic Command", Generic},
		{"Local Shell", LocalShell},
	}
	for _, c := range cases {
		d, ok := FromDisplay(c.name)
		if !ok || d != c.want {
			t.Errorf("FromDisplay(%q) = %v, %v; want %v, true", c.name, d, ok, c.want)
		}
		if got := DisplayName(c.want); got != c.name {
			t.Errorf("DisplayName(%v) = %q", c.want, got)
		}
	}
	if _, ok := FromDisplay("Bogus"); ok {
		t.Errorf("FromDisplay(Bogus) должен быть false")
	}
}

// VNC-детект по выводу `vncviewer --help` (как в легаси: grep RealVNC / TigerVNC).
func TestDetectVNC(t *testing.T) {
	cases := []struct {
		help string
		want VNCBackend
	}{
		{"TigerVNC 1.13", VNCTigerVNC},
		{"RealVNC 2018", VNCRealVNC},
		{"some viewer", VNCGeneric},
		{"", VNCGeneric},
	}
	for _, c := range cases {
		if got := DetectVNC(c.help); got != c.want {
			t.Errorf("DetectVNC(%q) = %v, want %v", c.help, got, c.want)
		}
	}
}

// Разбор опционной строки с шелл-квотингом (общий для cmdline и конвертера).
func TestSplitArgs(t *testing.T) {
	got := SplitArgs(` -o "ServerAliveInterval=5" -r disk:share="/home/my share" -g 1920x1080`)
	want := []string{"-o", "ServerAliveInterval=5", "-r", "disk:share=/home/my share", "-g", "1920x1080"}
	if len(got) != len(want) {
		t.Fatalf("SplitArgs = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Экранированные кавычки внутри значений (ревью-находка H1: вложенные " ломали round-trip).
func TestSplitArgsEscapedQuotes(t *testing.T) {
	got := SplitArgs(`-r disk:share="C:\Program Files" -k en-us`)
	want := []string{"-r", `disk:share=C:\Program Files`, "-k", "en-us"}
	if len(got) != len(want) || got[1] != want[1] || got[3] != want[3] {
		t.Fatalf("escaped: %#v", got)
	}
	// токен, содержащий кавычки целиком
	got = SplitArgs(`"disk:share=\"C:\Program Files\""`)
	if len(got) != 1 || got[0] != `disk:share="C:\Program Files"` {
		t.Fatalf("embedded quotes: %#v", got)
	}
}
