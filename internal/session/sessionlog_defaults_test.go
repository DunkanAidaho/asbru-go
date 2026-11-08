package session

// M31 #4 backlog: defaults-fallback для 'save session logs' — соединение ИЛИ
// глобальные defaults (легаси PACTerminal.pm:163-166).

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestResolveLogConfig(t *testing.T) {
	env := &config.Environment{SaveSessionLogs: 1, SessionLogsFolder: "/env", SessionLogPattern: "<UUID>.log", SessionLogsAmount: 5}
	// приоритет — окружение соединения.
	folder, pattern, amount, ok := resolveLogConfig(env, &config.Defaults{Raw: map[string]any{"save session logs": 1, "session logs folder": "/def", "session log pattern": "<UUID>_def.log", "session logs amount": 3}})
	if !ok || folder != "/env" || pattern != "<UUID>.log" || amount != 5 {
		t.Fatalf("env: %q %q %d %v", folder, pattern, amount, ok)
	}
	// без env → defaults.
	folder, pattern, amount, ok = resolveLogConfig(nil, &config.Defaults{Raw: map[string]any{"save session logs": 1, "session logs folder": "/def", "session log pattern": "<UUID>_def.log", "session logs amount": 3}})
	if !ok || folder != "/def" || pattern != "<UUID>_def.log" || amount != 3 {
		t.Fatalf("defaults: %q %q %d %v", folder, pattern, amount, ok)
	}
	// нигде не включено → false.
	if _, _, _, ok := resolveLogConfig(&config.Environment{}, &config.Defaults{}); ok {
		t.Fatal("ok=true при выключенных логах")
	}
	// defaults без каталога → false.
	if _, _, _, ok := resolveLogConfig(nil, &config.Defaults{Raw: map[string]any{"save session logs": 1}}); ok {
		t.Fatal("ok=true без каталога defaults")
	}
}
