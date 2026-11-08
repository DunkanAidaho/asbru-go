package ui

// Бэклог M30/M32: SOCKS/jump-туннель для встроенных RDP/VNC — maybeTunnel.
// Инжектируемые границы (tunnelAllocPort/tunnelStart) — юнит без реального
// ssh/nc/сокета. Проверяем выбор ветки, переадресацию и обработку ошибок.

import (
	"errors"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/session"
)

func TestMaybeTunnelNoProxy(t *testing.T) {
	// use proxy=0 → туннель не нужен (nil, без аллокации).
	allocCalled := false
	restore := patchTunnel(func() (int, error) { allocCalled = true; return 100, nil },
		func(session.StartTunnelParams) (*session.Tunnel, error) { return nil, errors.New("no") })
	defer restore()

	host, port, tn, err := maybeTunnel(&config.Environment{UseProxy: 0}, "u1", "/tmp", "10.0.0.1", 5900)
	if err != nil || tn != nil {
		t.Fatalf("proxy=0: tn=%v err=%v", tn, err)
	}
	if host != "10.0.0.1" || port != 5900 {
		t.Fatalf("proxy=0 host/port = %q/%d", host, port)
	}
	if allocCalled {
		t.Fatal("proxy=0 не должен аллоцировать порт")
	}
}

func TestMaybeTunnelJump(t *testing.T) {
	// jump(3): надо вернуть 127.0.0.1:LocalPort и *Tunnel.
	var gotParams session.StartTunnelParams
	restore := patchTunnel(func() (int, error) { return 5555, nil },
		func(p session.StartTunnelParams) (*session.Tunnel, error) {
			gotParams = p
			return &session.Tunnel{LocalPort: p.LocalPort}, nil
		})
	defer restore()

	host, port, tn, err := maybeTunnel(&config.Environment{UseProxy: 3, JumpIP: "jmp", JumpUser: "ju"}, "u1", "/tmp", "10.0.0.1", 5900)
	if err != nil {
		t.Fatalf("jump: %v", err)
	}
	if host != "127.0.0.1" || port != 5555 {
		t.Fatalf("jump host/port = %q/%d", host, port)
	}
	if tn == nil || tn.LocalPort != 5555 {
		t.Fatalf("jump tunnel = %+v", tn)
	}
	if gotParams.UseProxy != 3 || gotParams.TargetIP != "10.0.0.1" || gotParams.TargetPort != "5900" ||
		gotParams.JumpIP != "jmp" || gotParams.JumpUser != "ju" {
		t.Fatalf("jump params = %+v", gotParams)
	}
}

func TestMaybeTunnelSocks(t *testing.T) {
	// socks(1): переадресация + proxy-поля в params.
	var gotParams session.StartTunnelParams
	restore := patchTunnel(func() (int, error) { return 7777, nil },
		func(p session.StartTunnelParams) (*session.Tunnel, error) {
			gotParams = p
			return &session.Tunnel{LocalPort: p.LocalPort}, nil
		})
	defer restore()

	host, port, tn, err := maybeTunnel(&config.Environment{UseProxy: 1, ProxyIP: "10.0.0.9", ProxyPort: "1080", ProxyUser: "pu"},
		"u1", "/tmp", "10.0.0.1", 5900)
	if err != nil {
		t.Fatalf("socks: %v", err)
	}
	if host != "127.0.0.1" || port != 7777 || tn == nil {
		t.Fatalf("socks host/port/tn = %q/%d/%v", host, port, tn)
	}
	if gotParams.UseProxy != 1 || gotParams.ProxyIP != "10.0.0.9" || gotParams.ProxyPort != "1080" || gotParams.ProxyUser != "pu" {
		t.Fatalf("socks params = %+v", gotParams)
	}
}

func TestMaybeTunnelEmptyTarget(t *testing.T) {
	// пустой host/port → nil (без аллокации, без ошибки).
	allocCalled := false
	restore := patchTunnel(func() (int, error) { allocCalled = true; return 100, nil },
		func(session.StartTunnelParams) (*session.Tunnel, error) { return nil, errors.New("no") })
	defer restore()

	_, _, tn, err := maybeTunnel(&config.Environment{UseProxy: 3}, "u1", "/tmp", "", 0)
	if err != nil || tn != nil {
		t.Fatalf("empty target: tn=%v err=%v", tn, err)
	}
	if allocCalled {
		t.Fatal("пустой target не должен аллоцировать порт")
	}
}

func TestMaybeTunnelStartError(t *testing.T) {
	// ошибка StartTunnel пробрасывается (клиент не запускаем).
	restore := patchTunnel(func() (int, error) { return 100, nil },
		func(session.StartTunnelParams) (*session.Tunnel, error) { return nil, errors.New("boom") })
	defer restore()

	_, _, _, err := maybeTunnel(&config.Environment{UseProxy: 3, JumpIP: "jmp"}, "u1", "/tmp", "10.0.0.1", 5900)
	if err == nil {
		t.Fatalf("ожидали ошибку от StartTunnel")
	}
}

// patchTunnel — временная замена инжектируемых границ туннеля.
func patchTunnel(alloc func() (int, error), start func(session.StartTunnelParams) (*session.Tunnel, error)) func() {
	oa, os := tunnelAllocPort, tunnelStart
	tunnelAllocPort, tunnelStart = alloc, start
	return func() { tunnelAllocPort, tunnelStart = oa, os }
}
