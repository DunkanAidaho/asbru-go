package session

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// writeVNCPassFile создаёт 0600-файл через инжектируемый vncpasswd (ревью H3).
func TestWriteVNCPassFile(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-vncpasswd")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat >/dev/null\nprintf 'ENC'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(prev string) { vncpasswdPath = prev }(vncpasswdPath)
	vncpasswdPath = script

	sub := filepath.Join(dir, "tmp")
	if err := writeVNCPassFile(sub, "u1", "secret"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sub, "asbru_conn_u1.pass")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
	}
	b, _ := os.ReadFile(p)
	if string(b) != "ENC" {
		t.Fatalf("content = %q", b)
	}
}

// Конфигурационные блоки сохраняют пользовательские on_match (ревью H2).
func TestBlocksPreserveUserOnMatch(t *testing.T) {
	cfg := &config.Environment{Name: "x", Method: "SSH", User: "u", Pass: "p",
		Expect: []map[string]any{{"expect": "a", "send": "b", "on_match": 3}}}
	s := &Session{cfg: cfg}
	blocks := s.blocks()
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v, want [login, password, configure]", blocks)
	}
	if blocks[2].OnMatch != 3 {
		t.Fatalf("on_match конфиг-блока = %d, want 3", blocks[2].OnMatch)
	}
}

// M11: детект VNC-бинарника по --help подключается, когда явного
// WithVNCBackend нет (TigerVNC должен получать -PasswordFile).
func TestSessionVNCBackendDetected(t *testing.T) {
	old, oldD := vncHelp, vncD
	vncHelp = func() (string, error) { return "TigerVNC Viewer 1.13.0", nil }
	vncD = &vncDetector{once: new(sync.Once)}
	t.Cleanup(func() { vncHelp, vncD = old, oldD })

	s := &Session{opts: sessionOptions{}}
	if got := s.vncBackend(); got != method.VNCTigerVNC {
		t.Fatalf("детект: %v", got)
	}
}

// M11: если vncviewer недоступен — безопасный VNCGeneric.
func TestSessionVNCBackendUnknown(t *testing.T) {
	old, oldD := vncHelp, vncD
	vncHelp = func() (string, error) { return "", errors.New("vncviewer: not found") }
	vncD = &vncDetector{once: new(sync.Once)}
	t.Cleanup(func() { vncHelp, vncD = old, oldD })

	s := &Session{opts: sessionOptions{}}
	if got := s.vncBackend(); got != method.VNCGeneric {
		t.Fatalf("без vncviewer: %v", got)
	}
}

// BUG (blocker): PasswordFile для TigerVNC нужен только VNC-методам.
// На машине с vncviewer (TigerVNC), но без vncpasswd, любая SSH-сессия
// падала в spawnPTY ещё до запуска ssh («vncpasswd: executable file not
// found»). spawnPTY не должен трогать vncpasswd для не-VNC методов.
func TestSpawnPTYNonVNCDoesNotRequireVNCPasswd(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "vncpasswd-called")
	script := filepath.Join(dir, "vncpasswd")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	defer func(prev string) { vncpasswdPath = prev }(vncpasswdPath)
	vncpasswdPath = script

	s := &Session{
		env:  BuildEnv{TmpDir: filepath.Join(dir, "tmp"), UUID: "u1"},
		opts: sessionOptions{vnc: method.VNCTigerVNC},
	}
	pe := &config.Environment{Name: "ssh", Method: "SSH", IP: "127.0.0.1", Port: "1",
		User: "u", Pass: "p"}
	st, err := s.spawnPTY(pe)
	if err == nil {
		_ = st.Close()
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("spawnPTY запустил vncpasswd для SSH-сессии")
	}
}
