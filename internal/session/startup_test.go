package session

// M31: startup launch/script — команда, запускаемая при подключении.
// Современная реализация: чистая функция + инъектируемый раннер (тесты),
// дефолт — exec.CommandContext(/bin/sh -c) без Perl _execScript.

import (
	"context"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestStartupCommand(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *config.Environment
		defaults *config.Defaults
		cmd      string
		ok       bool
	}{
		{"off", &config.Environment{StartUpLaunch: 0, StartUpScript: "echo hi"}, nil, "", false},
		{"on-with-script", &config.Environment{StartUpLaunch: 1, StartUpScript: "echo hi"}, nil, "echo hi", true},
		{"on-empty-script", &config.Environment{StartUpLaunch: 1}, nil, "", false},
		{"nil", nil, nil, "", false},
		// M31-остаток defaults-fallback: env не задаёт → глобальные defaults.
		{"defaults-fallback", &config.Environment{},
			&config.Defaults{Raw: map[string]any{"startup launch": 1, "startup script": "echo default"}},
			"echo default", true},
		{"defaults-off", &config.Environment{},
			&config.Defaults{Raw: map[string]any{"startup launch": 0, "startup script": "echo default"}},
			"", false},
		{"env-wins", &config.Environment{StartUpLaunch: 1, StartUpScript: "echo env"},
			&config.Defaults{Raw: map[string]any{"startup launch": 1, "startup script": "echo default"}},
			"echo env", true},
		// MAJOR-1 регрессия: env выкл, но остался левый скрипт + defaults вкл →
		// атомарный whole-pair fallback берёт defaults-скрипт, не гибрид.
		{"env-off-leftover-defaults-on", &config.Environment{StartUpLaunch: 0, StartUpScript: "echo left"},
			&config.Defaults{Raw: map[string]any{"startup launch": 1, "startup script": "echo default"}},
			"echo default", true},
		{"nil-env-defaults-on", nil,
			&config.Defaults{Raw: map[string]any{"startup launch": 1, "startup script": "echo default"}},
			"echo default", true},
		{"env-on-empty-script-defaults-on", &config.Environment{StartUpLaunch: 1, StartUpScript: ""},
			&config.Defaults{Raw: map[string]any{"startup launch": 1, "startup script": "echo default"}},
			"", false},
	}
	for _, c := range cases {
		cmd, ok := startupCommand(c.cfg, c.defaults)
		if ok != c.ok || cmd != c.cmd {
			t.Errorf("%s: got (%q,%v) want (%q,%v)", c.name, cmd, ok, c.cmd, c.ok)
		}
	}
}

// M31: раннер вызывается с командой при подключении; не вызывается без startup.
func TestRunStartupRunner(t *testing.T) {
	var got string
	cfg := &config.Environment{StartUpLaunch: 1, StartUpScript: "echo hi"}
	s := &Session{cfg: cfg}
	s.opts.defaults = &config.Defaults{}
	s.opts.startupRunner = func(_ context.Context, cmd string) { got = cmd }

	s.runStartup()
	if got != "echo hi" {
		t.Fatalf("раннер: %q, want 'echo hi'", got)
	}

	got = ""
	s.RefreshConfig(&config.Environment{StartUpLaunch: 0, StartUpScript: "x"})
	s.runStartup()
	if got != "" {
		t.Fatalf("раннер вызван при выключенном startup: %q", got)
	}
}
