package config

// user-тест #3: миграция опций RDP — автоконвертация при несоответствии диалекта
// метода (сменил rdesktop↔xfreerdp, а опции остались от старого).

import (
	"strings"
	"testing"
)

func TestMigrateRdpOptions(t *testing.T) {
	// rdesktop-опции на xfreerdp-методе → конвертируются.
	env := &Environment{Method: "RDP (xfreerdp)", Options: "-a 16 -k en-us -d DOMAIN"}
	migrateRdpOptions(env)
	for _, want := range []string{"/bpp:16", "/kbd:en-us", "/d:DOMAIN"} {
		if !strings.Contains(env.Options, want) {
			t.Fatalf("не сконвертировано %q: %q", want, env.Options)
		}
	}
	// совпадающий диалект — не трогаем.
	env2 := &Environment{Method: "RDP (xfreerdp)", Options: "/bpp:16 /d:D"}
	migrateRdpOptions(env2)
	if env2.Options != "/bpp:16 /d:D" {
		t.Fatalf("совпал: %q", env2.Options)
	}
	// не-RDP — не трогаем.
	env3 := &Environment{Method: "SSH", Options: "-a 16"}
	migrateRdpOptions(env3)
	if env3.Options != "-a 16" {
		t.Fatalf("ssh: %q", env3.Options)
	}
	// обратная конверсия: xfreerdp-опции на rdesktop-методе.
	env4 := &Environment{Method: "RDP (rdesktop)", Options: "/bpp:16 /d:D"}
	migrateRdpOptions(env4)
	if !strings.Contains(env4.Options, "-a 16") || !strings.Contains(env4.Options, "-d D") {
		t.Fatalf("обратная: %q", env4.Options)
	}
}
