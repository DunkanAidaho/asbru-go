package ui

// M25: --readonly (легаси PACMain.pm:301-304) — запрет записи конфига;
// --password (PACMain.pm:243-260) — пароль GUI без диалога.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M25: saveCfg в readonly-режиме не пишет файл и возвращает ErrReadOnly.
func TestSaveCfgReadOnlyBlocks(t *testing.T) {
	readOnly.Store(true)
	defer readOnly.Store(false)

	path := filepath.Join(t.TempDir(), "asbru.yml")
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	if err := saveCfg(cfg, path); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("ошибка = %v, хотим ErrReadOnly", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("readonly-режим записал файл (stat err=%v)", err)
	}
}

// M25: в обычном режиме saveCfg сохраняет конфиг.
func TestSaveCfgNormal(t *testing.T) {
	readOnly.Store(false)

	path := filepath.Join(t.TempDir(), "asbru.yml")
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	if err := saveCfg(cfg, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("конфиг не сохранён: %v", err)
	}
}

// M25: --password подставляется в guiPassGate вместо диалога.
func TestGuiPassGateWithCLIPassword(t *testing.T) {
	cipher := config.NewCBC()
	cfg := &config.Config{Defaults: config.Defaults{
		UseGUIPassword: 1,
		GUIPassword:    cipher.Encrypt("right"),
	}}
	if !guiPassGate(cfg, nil, "right") {
		t.Fatal("правильный CLI-пароль отклонён")
	}
	if guiPassGate(cfg, nil, "wrong") {
		t.Fatal("неверный CLI-пароль принят")
	}
	// без защиты гейт не спрашивает ни при каком пароле
	open := &config.Config{Defaults: config.Defaults{}}
	if !guiPassGate(open, nil, "") {
		t.Fatal("без защиты — отказ")
	}
}

// M25: --start-uuid=uuid[:cluster] — суффикс кластера (легаси asbru-cm:42).
func TestSplitClusterSuffix(t *testing.T) {
	cases := []struct {
		in, uuid, cluster string
	}{
		{"", "", ""},
		{"u1", "u1", ""},
		{"u1:prod", "u1", "prod"},
		{"u1:gruppa:prod", "u1:gruppa", "prod"}, // суффикс после последнего ':'
	}
	for _, c := range cases {
		u, cl := splitClusterSuffix(c.in)
		if u != c.uuid || cl != c.cluster {
			t.Fatalf("splitClusterSuffix(%q) = (%q,%q), want (%q,%q)", c.in, u, cl, c.uuid, c.cluster)
		}
	}
}

// M25: prepareTmpDir создаёт базу ASBRU_TMP и подкаталог (ревью Important).
func TestPrepareTmpDirCreatesBase(t *testing.T) {
	base := filepath.Join(t.TempDir(), "sub")
	dir, err := prepareTmpDir(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("база ASBRU_TMP не создана: %v", err)
	}
	if !strings.HasPrefix(dir, base+string(os.PathSeparator)) {
		t.Fatalf("dir=%q вне base %q", dir, base)
	}
}

func TestPrepareTmpDirNoBase(t *testing.T) {
	dir, err := prepareTmpDir("")
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" {
		t.Fatal("пустой временный каталог")
	}
}
