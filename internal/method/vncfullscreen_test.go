package method

import "testing"

// M39-fullscreen: легаси-токены полноэкранного VNC. Строка options исторически
// общая для трёх диалектов клиента, поэтому распознаём РОВНО те формы, которые
// писали легаси-методы (новых токенов не вводим):
//   - «-fullscreen»        — vncviewer / RealVNC (PACMethod_vncviewer.pm:140,
//     PACMethod_realvnc.pm:163);
//   - «-FullScreen=N» N>0  — TigerVNC (PACMethod_tigervnc.pm:143/161).
func TestParseVNCFullscreen(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"пустая строка", "", false},
		{"чужой токен", "-QualityLevel 9 -CompressLevel 8", false},
		{"vncviewer/RealVNC: -fullscreen", "-fullscreen -autopass", true},
		{"-fullscreen единственный", "-fullscreen", true},
		{"-fullscreen хвост", "-autopass -fullscreen", true},
		{"TigerVNC: -FullScreen=1", "-FullScreen=1 -QualityLevel 9", true},
		{"TigerVNC: -FullScreen=0 выключен", "-FullScreen=0 -QualityLevel 9", false},
		{"TigerVNC: -FullScreen=12", "-FullScreen=12", true},
		{"tigervnc без значения — не флаг", "-FullScreen", false},
		{"регистр не легаси: -FULLSCREEN", "-FULLSCREEN", false},
		{"регистр не легаси: -fullscreen=1", "-fullscreen=1", false},
		{"-fullscreen=0 выключен", "-fullscreen=0", false},
		{"префикс не наш", "-fullscreenx", false},
		{"xfreerdp-форма не VNC-токен", "/fullscreen", false},
		{"мусорное значение", "-FullScreen=yes", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseVNCFullscreen(c.in); got != c.want {
				t.Errorf("ParseVNCFullscreen(%q) = %v, хотим %v", c.in, got, c.want)
			}
		})
	}
}
