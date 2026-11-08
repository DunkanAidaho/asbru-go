package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M39-fullscreen: встроенный RDP/VNC разворачивается на весь экран по
// существующим легаси-токенам строки options — без новых галок/токенов.
func TestEmbedWantsFullscreen(t *testing.T) {
	cases := []struct {
		name string
		kind EmbedKind
		env  *config.Environment
		want bool
	}{
		{"RDP xfreerdp /f", EmbedRDP, &config.Environment{Options: "/f +clipboard"}, true},
		{"RDP xfreerdp /fullscreen", EmbedRDP, &config.Environment{Options: "/fullscreen"}, true},
		{"RDP rdesktop -f", EmbedRDP, &config.Environment{Options: "-f -a16"}, true},
		{"RDP без токена", EmbedRDP, &config.Environment{Options: "/bpp:16"}, false},
		// «-grab-keyboard» не должен приниматься за «-g» (size) — проверка M35;
		// главное здесь: /f отсутствует → не полный экран.
		{"RDP прочие флаги", EmbedRDP, &config.Environment{Options: "-grab-keyboard /rfx"}, false},
		{"VNC RealVNC/vncviewer -fullscreen", EmbedVNC, &config.Environment{Options: "-fullscreen -autopass"}, true},
		{"VNC TigerVNC -FullScreen=1", EmbedVNC, &config.Environment{Options: "-FullScreen=1"}, true},
		{"VNC TigerVNC -FullScreen=0", EmbedVNC, &config.Environment{Options: "-FullScreen=0"}, false},
		{"VNC без опций", EmbedVNC, &config.Environment{}, false},
		{"PVE без cgo-канваса", EmbedPVE, &config.Environment{Options: "/f"}, false},
		{"nil env", EmbedRDP, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := embedWantsFullscreen(c.kind, c.env); got != c.want {
				t.Errorf("embedWantsFullscreen(%v) = %v, хотим %v", c.kind, got, c.want)
			}
		})
	}
}
