package cli

import (
	"bytes"
	ui "github.com/DunkanAidaho/asbru-go/internal/ui"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/lock"
)

// TestVersionCommand — спецификация M0: `asbru version` печатает имя и версию.
func TestVersionCommand(t *testing.T) {
	var buf bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "asbru") || !strings.Contains(out, Version) {
		t.Fatalf("version output %q does not contain asbru %s", out, Version)
	}
}

// Голый `asbru` (без подкоманды) запускает GUI (ревью-находка H1).
func TestRootDefaultRunsGUI(t *testing.T) {
	called := false
	defer func(prev func(string, ui.Startup) error) { runGUI = prev }(runGUI)
	defer func(prev func() (func(), error)) { acquireLock = prev }(acquireLock)
	runGUI = func(string, ui.Startup) error { called = true; return nil }
	acquireLock = func() (func(), error) { return func() {}, nil }
	cmd := NewRootCmd()
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !called {
		t.Fatal("корневая команда должна запускать GUI")
	}
}

// Повторный запуск: блокировка занята и IPC-инстанс недоступен → ошибка
// (M26: иначе вместо ошибки шлём флаги живому инстансу; здесь сокета нет).
func TestLaunchGUIRejectedWhenHeld(t *testing.T) {
	setEnv(t, "ASBRU_CFG", t.TempDir()) // не трогать реальный ~/.config (M26-ревью)
	defer func(prev func() (func(), error)) { acquireLock = prev }(acquireLock)
	acquireLock = func() (func(), error) { return nil, lock.ErrHeld }
	if err := launchGUI(); err == nil {
		t.Fatal("ожидали ошибку при занятой блокировке и недоступном IPC")
	}
}

// Каталог конфигурации: $ASBRU_CFG или ~/.config/asbru (как в Perl entry script).
func TestDefaultCfgDir(t *testing.T) {
	prev, had := os.LookupEnv("ASBRU_CFG")
	_ = os.Setenv("ASBRU_CFG", "/tmp/custom-cfg")
	defer func() {
		if had {
			_ = os.Setenv("ASBRU_CFG", prev)
		} else {
			_ = os.Unsetenv("ASBRU_CFG")
		}
	}()
	if got := cfgDir(); got != "/tmp/custom-cfg" {
		t.Fatalf("cfgDir = %q, хотим override из ASBRU_CFG", got)
	}
	_ = os.Unsetenv("ASBRU_CFG")
	const suffix = "/.config/asbru"
	if got := cfgDir(); got == "" || len(got) < len(suffix) || got[len(got)-len(suffix):] != suffix {
		t.Fatalf("cfgDir дефолт = %q", got)
	}
}

// --debug: файл лога лежит в каталоге конфига (основан на cfgDir).
func TestDebugLogPath(t *testing.T) {
	prev, had := os.LookupEnv("ASBRU_CFG")
	_ = os.Setenv("ASBRU_CFG", "/tmp/dbg-cfg")
	defer func() {
		if had {
			_ = os.Setenv("ASBRU_CFG", prev)
		} else {
			_ = os.Unsetenv("ASBRU_CFG")
		}
	}()
	if got := debugLogPath(); got != filepath.Join("/tmp/dbg-cfg", "debug.log") {
		t.Fatalf("debugLogPath = %q", got)
	}
}

// M17-ревью: флаги доходят до Startup (инжект runGUI).
func TestStartFlagsReachStartup(t *testing.T) {
	var got ui.Startup
	defer func(prev func(string, ui.Startup) error) { runGUI = prev }(runGUI)
	defer func(prev func() (func(), error)) { acquireLock = prev }(acquireLock)
	runGUI = func(_ string, s ui.Startup) error { got = s; return nil }
	acquireLock = func() (func(), error) { return func() {}, nil }
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--start-uuid", "u1", "--edit-uuid", "u2", "--quick-conn", "--start-shell"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.StartUUID != "u1" || got.EditUUID != "u2" || !got.QuickConn || !got.StartShell {
		t.Fatalf("Startup = %+v", got)
	}
}
