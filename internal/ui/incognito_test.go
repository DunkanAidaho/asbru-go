package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// бэклог Incognito: аллокатор случайного свободного порта (эфемерный, 1..65535).
func TestAllocRandomPort(t *testing.T) {
	for i := 0; i < 5; i++ {
		p := allocRandomPort()
		if p <= 0 || p > 65535 {
			t.Fatalf("allocRandomPort = %d, хотим 1..65535", p)
		}
	}
}

// бэклог Incognito: флаг 'create random SOCKS tunnel' → BuildEnv.RandomSocks.
func TestNewBuildEnvRandomSocks(t *testing.T) {
	old := allocSocksPort
	allocSocksPort = func() int { return 43210 }
	defer func() { allocSocksPort = old }()

	// флаг вкл (SSH) → RandomSocks = "43210".
	be, err := newBuildEnv("u1", &config.Environment{EnableRandomSocks: 1, Method: "SSH"}, &config.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if be.RandomSocks != "43210" {
		t.Fatalf("RandomSocks = %q, хотим 43210", be.RandomSocks)
	}
	// флаг выкл → пусто.
	be, err = newBuildEnv("u1", &config.Environment{Method: "SSH"}, &config.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if be.RandomSocks != "" {
		t.Fatalf("RandomSocks без флага = %q, хотим пусто", be.RandomSocks)
	}
}

// бэклог Incognito: при ошибке аллокатора (0) флаг НЕ должен дать "-D localhost:0".
func TestNewBuildEnvRandomSocksAllocFail(t *testing.T) {
	old := allocSocksPort
	allocSocksPort = func() int { return 0 }
	defer func() { allocSocksPort = old }()

	be, err := newBuildEnv("u1", &config.Environment{EnableRandomSocks: 1, Method: "SSH"}, &config.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if be.RandomSocks != "" {
		t.Fatalf("RandomSocks при ошибке аллокатора = %q, хотим пусто", be.RandomSocks)
	}
}

// бэклог Incognito: флаг только для SSH (легаси /ssh/i) — не-SSH без порта.
func TestNewBuildEnvRandomSocksNonSSH(t *testing.T) {
	old := allocSocksPort
	allocSocksPort = func() int { return 43210 }
	defer func() { allocSocksPort = old }()

	be, err := newBuildEnv("u1", &config.Environment{EnableRandomSocks: 1, Method: "VNC"}, &config.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if be.RandomSocks != "" {
		t.Fatalf("RandomSocks для не-SSH = %q, хотим пусто", be.RandomSocks)
	}
}
