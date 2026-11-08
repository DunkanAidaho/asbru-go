package ui

import "testing"

// M36-ревью MAJOR: 'hide' хранится как int(0/1), string("1") или bool — varText
// должен маскировать по Perl-истинности (иначе секрет утекал в меню).
func TestVarTextMasksHidden(t *testing.T) {
	cases := []struct {
		name string
		v    map[string]any
		want string
	}{
		{"hide int 1", map[string]any{"hide": 1, "txt": "secret"}, "<hidden>"},
		{"hide string 1", map[string]any{"hide": "1", "txt": "secret"}, "<hidden>"},
		{"hide bool true", map[string]any{"hide": true, "txt": "secret"}, "<hidden>"},
		{"hide int 0", map[string]any{"hide": 0, "txt": "visible"}, "visible"},
		{"hide string 0", map[string]any{"hide": "0", "txt": "visible"}, "visible"},
		{"hide absent", map[string]any{"txt": "visible"}, "visible"},
		{"hide empty str", map[string]any{"hide": "", "txt": "visible"}, "visible"},
		{"no txt", map[string]any{"hide": 1}, "<hidden>"},
		{"nil map", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := varText(c.v); got != c.want {
				t.Errorf("varText(%v) = %q, want %q", c.v, got, c.want)
			}
		})
	}
}

// varHidden — прямая проверка истинности 'hide'.
func TestVarHidden(t *testing.T) {
	for _, tt := range []struct {
		v any
		w bool
	}{
		{1, true}, {"1", true}, {true, true}, {1.0, true},
		{0, false}, {"0", false}, {false, false}, {"", false}, {nil, false}, {0.0, false},
	} {
		m := map[string]any{"hide": tt.v}
		if got := varHidden(m); got != tt.w {
			t.Errorf("varHidden(hide=%v) = %v, want %v", tt.v, got, tt.w)
		}
	}
}

// rawVarText — предзаполнение диалога правки скрытой переменной реальным
// значением (иначе '<hidden>' затирал бы секрет — foot-gun легаси-порта).
func TestRawVarTextNoMask(t *testing.T) {
	m := map[string]any{"hide": 1, "txt": "secret"}
	if got := rawVarText(m); got != "secret" {
		t.Fatalf("rawVarText(hidden) = %q, want 'secret' (не маску)", got)
	}
	// а varText для меню — маска.
	if got := varText(m); got != "<hidden>" {
		t.Fatalf("varText(hidden) = %q, want '<hidden>'", got)
	}
}
