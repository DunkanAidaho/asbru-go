package embed

// M39: звук встроенного RDP-клиента. Настройка берётся из существующего поля
// options (xfreerdp `/sound:…` / rdesktop `-r sound:…`) и доезжает до C-моста
// как аргумент канала rdpsnd ("sys:alsa"). Без токена звук не включается
// (легаси-паритет), а нераспознанный токен не попадает в Unknown.

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

func TestRDPParamsSound(t *testing.T) {
	tests := []struct {
		name string
		opts string
		mode method.RdpSoundMode
		arg  string
	}{
		{"нет токена — звук выключен", "", method.RdpSoundUnset, ""},
		{"другие опции", "-k en-us +clipboard /size:1280x1024", method.RdpSoundUnset, ""},
		{"xfreerdp канон", "/sound:sys:alsa", method.RdpSoundLocal, "sys:alsa"},
		{"xfreerdp pulse", "/sound:sys:pulse", method.RdpSoundLocal, "sys:pulse"},
		{"rdesktop local (канон alsa)", "-r sound:local", method.RdpSoundLocal, "sys:alsa"},
		{"rdesktop pulseaudio", "-r sound:local:pulseaudio", method.RdpSoundLocal, "sys:pulse"},
		{"rdesktop off", "-r sound:off", method.RdpSoundOff, ""},
		{"rdesktop remote", "-r sound:remote", method.RdpSoundRemote, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := RDPParamsFrom(&config.Environment{IP: "h"}, tt.opts)
			if p.Sound.Mode != tt.mode {
				t.Errorf("Sound.Mode = %v, want %v", p.Sound.Mode, tt.mode)
			}
			if got := p.Sound.ChannelArg(); got != tt.arg {
				t.Errorf("Sound.ChannelArg() = %q, want %q", got, tt.arg)
			}
			if len(p.Unknown) != 0 {
				t.Errorf("Unknown = %v, звуковой токен не должен считаться неизвестным", p.Unknown)
			}
		})
	}
}
