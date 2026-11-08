package embed

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M37 (п.2, «надо чтобы работали»): галки безопасности обязаны доезжать до
// встроенного клиента — RDPParamsFrom их извлекает, Flags() пакует в битмаску
// для libfreerdp3 (раньше опции игнорировались полностью).
func TestRDPSecurityFlagsFromOptions(t *testing.T) {
	e := &config.Environment{Name: "x", IP: "h", Port: "3389", User: "u"}
	// ВНИМАНИЕ: НЕ все три sec-галки разом — такой набор сбрасывается
	// fail-closed (см. TestRDPSecurityAllDisabledFailsClosed).
	p := RDPParamsFrom(e,
		"-sec-nla -sec-tls /cert-ignore -authentication -fast-path "+
			"/rfx /nsc /dynamic-resolution +fonts /tls-seclevel:3 /admin")
	if !p.NoNLA || !p.NoTLS {
		t.Errorf("sec-* не извлечены: noNLA=%v noTLS=%v", p.NoNLA, p.NoTLS)
	}
	if r := RDPParamsFrom(e, "-sec-rdp"); !r.NoRDP || r.NoNLA || r.NoTLS {
		t.Errorf("-sec-rdp отдельно: %+v", r)
	}
	if !p.CertIgnore || !p.NoAuth || !p.NoFastPath {
		t.Errorf("cert/authentication/fast-path: %v %v %v", p.CertIgnore, p.NoAuth, p.NoFastPath)
	}
	if !p.RFX || !p.NSC || !p.DynamicResolution || !p.FontSmooth {
		t.Errorf("rfx/nsc/dynamic/fonts: %v %v %v %v", p.RFX, p.NSC, p.DynamicResolution, p.FontSmooth)
	}
	if p.TLSSecLevel != 3 {
		t.Errorf("tls-seclevel = %d, хотим 3", p.TLSSecLevel)
	}
	if !p.Admin {
		t.Error("/admin не извлечён")
	}
	f := p.Flags()
	for _, bit := range []struct {
		name string
		ok   bool
	}{{"NoNLA", f&FlagNoNLA != 0}, {"NoTLS", f&FlagNoTLS != 0},
		{"CertIgnore", f&FlagCertIgnore != 0}, {"NoAuth", f&FlagNoAuth != 0},
		{"NoFastPath", f&FlagNoFastPath != 0}, {"RFX", f&FlagRFX != 0}, {"NSC", f&FlagNSC != 0},
		{"Dynamic", f&FlagDynamicResolution != 0}, {"Fonts", f&FlagFontSmooth != 0},
		{"Admin", f&FlagAdmin != 0}} {
		if !bit.ok {
			t.Errorf("бит %s не выставлен (flags=%#x)", bit.name, f)
		}
	}
}

// Без токенов — битмаска пуста: поведение прежнее (NLA/TLS/RDP включены),
// регресса для работающих подключений нет.
func TestRDPSecurityDefaultsUnset(t *testing.T) {
	p := RDPParamsFrom(&config.Environment{Name: "x", IP: "h"}, "")
	if p.NoNLA || p.NoTLS || p.NoRDP || p.CertIgnore || p.NoAuth ||
		p.NoFastPath || p.RFX || p.NSC || p.DynamicResolution || p.FontSmooth {
		t.Fatalf("флаги без токенов: %+v", p)
	}
	if p.TLSSecLevel != 0 {
		t.Fatalf("tls-seclevel = %d, хотим 0 (не задано)", p.TLSSecLevel)
	}
	if p.Flags() != 0 {
		t.Fatalf("битмаска = %#x, хотим 0", p.Flags())
	}
}

// Уровень TLS вне диапазона 0..5 игнорируется (не мусор в настройки).
func TestRDPSecurityTLSLevelBounds(t *testing.T) {
	for _, in := range []string{"/tls-seclevel:9", "/tls-seclevel:x", "/tls-seclevel:"} {
		p := RDPParamsFrom(&config.Environment{IP: "h"}, in)
		if p.TLSSecLevel != 0 {
			t.Errorf("%q → %d, хотим 0", in, p.TLSSecLevel)
		}
	}
	for n := 1; n <= 5; n++ {
		p := RDPParamsFrom(&config.Environment{IP: "h"}, "/tls-seclevel:"+string(rune('0'+n)))
		if p.TLSSecLevel != n {
			t.Errorf("уровень %d не извлечён (%d)", n, p.TLSSecLevel)
		}
	}
}

// M37-ревью MINOR-4: если сняты ВСЕ три типа защиты — это не «no security»,
// а сломанный конфиг: возвращаемся к умолчанию (не понижаем защиту молча).
func TestRDPSecurityAllDisabledFailsClosed(t *testing.T) {
	p := RDPParamsFrom(&config.Environment{IP: "h"}, "-sec-nla -sec-tls -sec-rdp")
	if p.NoNLA && p.NoTLS && p.NoRDP {
		t.Fatalf("все три типа сняты: клиент предложил бы серверу «без защиты» (%+v)", p)
	}
	if p.Flags()&(FlagNoNLA|FlagNoTLS|FlagNoRDP) != 0 {
		t.Fatalf("битмаска сохранила снятие всех типов: %#x", p.Flags())
	}
	// Частичное снятие не трогаем: одна-две галки — осознанный выбор.
	q := RDPParamsFrom(&config.Environment{IP: "h"}, "-sec-nla -sec-tls")
	if !q.NoNLA || !q.NoTLS || q.NoRDP {
		t.Fatalf("частичное снятие испорчено: %+v", q)
	}
}

// M38: предусловие автоподбора размера. Без явного /size размеры ДОЛЖНЫ
// остаться нулевыми — тогда встроенный клиент возьмёт размер виджета (1:1, без
// масштабирования); иначе он зафиксировал бы чужой размер и кадр «квадратило».
// Тест характеризующий (поведение уже было) — но именно на него опирается
// beginWhenSized в rdpview.cpp, поэтому фиксируем контракт явно.
func TestRDPSizeUnsetLetsWidgetDecide(t *testing.T) {
	p := RDPParamsFrom(&config.Environment{IP: "h"}, "/bpp:24 /admin")
	if p.Width != 0 || p.Height != 0 {
		t.Fatalf("без /size размеры должны быть нулевыми, получено %dx%d", p.Width, p.Height)
	}
	q := RDPParamsFrom(&config.Environment{IP: "h"}, "/size:1280x1024")
	if q.Width != 1280 || q.Height != 1024 {
		t.Fatalf("явный /size не разобран: %dx%d", q.Width, q.Height)
	}
}
