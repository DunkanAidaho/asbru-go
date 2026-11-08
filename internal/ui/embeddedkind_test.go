package ui

import "testing"

// M37 (п.4): «встроенный ли клиент» — решение ДЕСКРИПТОРА (method.SpecFor),
// а не свитча по именам в UI. Виджет выбирается уже по диалекту.
func TestEmbeddedKindFromSpec(t *testing.T) {
	want := map[string]EmbedKind{
		"RDP (rdesktop)": EmbedRDP,
		"RDP (xfreerdp)": EmbedRDP,
		"VNC":            EmbedVNC,
	}
	for name, kind := range want {
		got, ok := embeddedKind(name)
		if !ok || got != kind {
			t.Errorf("%s: got %v (ok=%v), хотим %v", name, got, ok, kind)
		}
	}
	for _, name := range []string{"SSH", "SFTP", "Telnet", "MOSH", "Generic Command", "Local Shell", "нет-такого"} {
		if got, ok := embeddedKind(name); ok {
			t.Errorf("%s: встроенный клиент не нужен (got %v)", name, got)
		}
	}
}
