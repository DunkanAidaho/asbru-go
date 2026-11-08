package cli

// M25: CLI-флаги легаси (asbru-cm:32-58, PACMain.pm:209-304). Все тесты
// написаны ДО реализации (TDD): запускаются против отсутствующих символов.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ui "github.com/DunkanAidaho/asbru-go/internal/ui"
)

// setEnv — временная установка ASBRU_CFG (восстанавливается по завершении).
func setEnv(t *testing.T, key, val string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, val); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// injectGUI — подмена runGUI/acquireLock (тесты не запускают Qt).
func injectGUI(t *testing.T, fn func(string, ui.Startup) error) {
	t.Helper()
	oldGUI := runGUI
	oldLock := acquireLock
	runGUI = fn
	acquireLock = func() (func(), error) { return func() {}, nil }
	t.Cleanup(func() { runGUI, acquireLock = oldGUI, oldLock })
}

// M25: --config-dir — относительный путь резолвится к ~/.config, абсолютный
// остаётся как есть (легаси asbru-cm:61-71).
func TestConfigDirFlag(t *testing.T) {
	// HOME в temp: песочница запрещает писать в реальный ~/.config, а
	// --config-dir создаёт каталог. Резолвинг тестируется независимо.
	home := t.TempDir()
	setEnv(t, "HOME", home)
	// флаги ставят ASBRU_CFG — снимаем после теста (загрязнение, M25-ревью)
	t.Cleanup(func() { os.Unsetenv("ASBRU_CFG") })

	t.Run("relative", func(t *testing.T) {
		var got string
		injectGUI(t, func(p string, _ ui.Startup) error { got = p; return nil })
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config-dir=asbru.test"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".config", "asbru.test", "asbru.yml")
		if got != want {
			t.Fatalf("cfgPath = %q, want %q", got, want)
		}
	})

	t.Run("absolute", func(t *testing.T) {
		var got string
		injectGUI(t, func(p string, _ ui.Startup) error { got = p; return nil })
		cmd := NewRootCmd()
		cmd.SetArgs([]string{"--config-dir=/tmp/abs-cfg"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if got != "/tmp/abs-cfg/asbru.yml" {
			t.Fatalf("cfgPath = %q, want /tmp/abs-cfg/asbru.yml", got)
		}
	})
}

// M25: --temp-dir → ASBRU_TMP (легаси asbru-cm:72-73).
func TestTempDirFlag(t *testing.T) {
	t.Cleanup(func() { os.Unsetenv("ASBRU_TMP") }) // M25-ревью: без загрязнения
	injectGUI(t, func(string, ui.Startup) error { return nil })
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--temp-dir=/tmp/asbru-tmp"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ASBRU_TMP"); got != "/tmp/asbru-tmp" {
		t.Fatalf("ASBRU_TMP = %q, want /tmp/asbru-tmp", got)
	}
}

// M25: --verbose включает debug-логирование (как --debug).
func TestVerboseFlagEnablesDebug(t *testing.T) {
	called := false
	defer func(prev func()) { setDebugLogging = prev }(setDebugLogging)
	setDebugLogging = func() { called = true }
	injectGUI(t, func(string, ui.Startup) error { return nil })
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--verbose"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("--verbose не включил debug-логирование")
	}
}

// M25: --debug по-прежнему включает debug-логирование.
func TestDebugFlagEnablesDebug(t *testing.T) {
	called := false
	defer func(prev func()) { setDebugLogging = prev }(setDebugLogging)
	setDebugLogging = func() { called = true }
	injectGUI(t, func(string, ui.Startup) error { return nil })
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--debug"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("--debug не включил debug-логирование")
	}
}

// M25: новые GUI-флаги доходят до Startup.
func TestStartupCarriesNewFlags(t *testing.T) {
	var got ui.Startup
	injectGUI(t, func(_ string, s ui.Startup) error { got = s; return nil })
	cmd := NewRootCmd()
	cmd.SetArgs([]string{
		"--password", "sekret",
		"--preferences",
		"--iconified",
		"--readonly",
		"--no-splash",
		"--no-backup",
		"--scripts",
		"--start-script", "foo.pl",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got.GUIPassword != "sekret" {
		t.Errorf("GUIPassword = %q, want sekret", got.GUIPassword)
	}
	if !got.Prefs || !got.Iconified || !got.ReadOnly || !got.NoSplash || !got.NoBackup || !got.Scripts {
		t.Errorf("bool-флаги не дошли: %+v", got)
	}
	if got.StartScript != "foo.pl" {
		t.Errorf("StartScript = %q, want foo.pl", got.StartScript)
	}
}

// fixtureConfig — временный каталог конфигурации с двумя соединениями и
// мусорным __PAC_SHELL__.
func fixtureConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	data := "environments:\n" +
		"  u1:\n    name: Server1\n    method: SSH\n    ip: 10.0.0.1\n    parent: ''\n" +
		"  g1:\n    name: Group\n    _is_group: 1\n    parent: ''\n" +
		"  __PAC_SHELL__:\n    name: Shell\n    method: Local Shell\n    parent: ''\n"
	if err := os.WriteFile(filepath.Join(dir, "asbru.yml"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "ASBRU_CFG", dir)
	return dir
}

// M25: --list-uuids печатает таблицу UUID и НЕ запускает GUI
// (легаси PACMain.pm:218-234).
func TestListUUIDsOutput(t *testing.T) {
	fixtureConfig(t)
	gui := false
	injectGUI(t, func(string, ui.Startup) error { gui = true; return nil })

	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--list-uuids"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gui {
		t.Fatal("GUI не должен запускаться при --list-uuids")
	}
	s := out.String()
	for _, want := range []string{"UUID", "PARENT_UUID", "TYPE", "METHOD", "NAME", "u1", "Server1", "GROUP", "SSH"} {
		if !strings.Contains(s, want) {
			t.Fatalf("--list-uuids не содержит %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "__PAC_SHELL__") {
		t.Fatalf("--list-uuids вывел __PAC_SHELL__:\n%s", s)
	}
}

// M25: --dump-uuid печатает данные соединения (легаси PACMain.pm:209-214).
func TestDumpUUIDOutput(t *testing.T) {
	fixtureConfig(t)
	var out bytes.Buffer
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--dump-uuid=u1"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"name: Server1", "method: SSH", "ip: 10.0.0.1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("--dump-uuid не содержит %q:\n%s", want, s)
		}
	}
}

// M25: --dump-uuid для неизвестного uuid — ошибка.
func TestDumpUUIDUnknown(t *testing.T) {
	fixtureConfig(t)
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--dump-uuid=nope"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("ожидали ошибку для неизвестного uuid")
	}
}
