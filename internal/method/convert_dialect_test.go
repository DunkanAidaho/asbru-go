package method

// user-тест #3: определение диалекта опций RDP (rdesktop "-…" vs xfreerdp "/…").

import "testing"

func TestDialectOf(t *testing.T) {
	// rdesktop: ключи вида -a 16 -k en-us -d DOMAIN
	if d := DialectOf("-a 16 -k en-us -d DOMAIN"); d != RDesktop {
		t.Fatalf("rdesktop опции: dialect=%v", d)
	}
	// xfreerdp: /bpp:16 /kbd:en-us /d:DOMAIN
	if d := DialectOf("/bpp:16 /kbd:en-us /d:DOMAIN"); d != XFreeRDP {
		t.Fatalf("xfreerdp опции: dialect=%v", d)
	}
	// пусто/неопределённо
	if d := DialectOf(""); d != "" {
		t.Fatalf("пусто: dialect=%v", d)
	}
	if d := DialectOf("some-random"); d != "" {
		t.Fatalf("не-ключ: dialect=%v", d)
	}
}

// M4: xfreerdp-булевы флаги вида «+clipboard» должны распознаваться как xfreerdp.
func TestDialectOfPlusFlag(t *testing.T) {
	if d := DialectOf("+clipboard"); d != XFreeRDP {
		t.Fatalf("Диалект +clipboard = %v, want XFreeRDP", d)
	}
	// первый токен '-' → rdesktop (даже если дальше есть +…)
	if d := DialectOf("-grab-keyboard +clipboard"); d != RDesktop {
		t.Fatalf("Диалект -grab-keyboard… = %v, want RDesktop", d)
	}
}
