package embed

// M39: drive redirection для встроенного RDP — конфигурационный маппинг.
// Источник — существующее поле соединения `options` (диалект xfreerdp или
// rdesktop); новые поля конфига не вводятся. Неразобранные/битые токены
// безопасно игнорируются (диск не подключается, connect не падает).

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

func TestRDPParamsDrivesFromOptions(t *testing.T) {
	e := &config.Environment{Name: "rdp", IP: "h", Options: "/drive:docs,/home/u/docs +clipboard"}
	p := RDPParamsFrom(e, e.Options)
	want := []method.RdpDrive{{Share: "docs", Path: "/home/u/docs"}}
	if !reflect.DeepEqual(p.Drives, want) {
		t.Fatalf("Drives=%+v, хотим %+v", p.Drives, want)
	}
	if len(p.Unknown) != 0 {
		t.Fatalf("Unknown=%v, /drive: не должен считаться неизвестным", p.Unknown)
	}
}

// rdesktop-диалект (наследие Perl-конфигов): -r disk:share=path.
func TestRDPParamsDrivesFromRdesktopOptions(t *testing.T) {
	p := RDPParamsFrom(&config.Environment{IP: "h"}, `-r disk:share="/home/u"`)
	want := []method.RdpDrive{{Share: "share", Path: "/home/u"}}
	if !reflect.DeepEqual(p.Drives, want) {
		t.Fatalf("Drives=%+v, хотим %+v", p.Drives, want)
	}
}

// Без /drive: дисков нет — поведение прежнее (никаких неявных перенаправлений,
// в т.ч. домашнего каталога).
func TestRDPParamsDrivesDefaultEmpty(t *testing.T) {
	p := RDPParamsFrom(&config.Environment{IP: "h"}, "+clipboard /size:1280x1024")
	if len(p.Drives) != 0 {
		t.Fatalf("Drives=%+v без /drive:, хотим пусто", p.Drives)
	}
}
