package session

// M31: startup launch/script — команда, запускаемая при подключении.
// Современная реализация: чистая функция + инъектируемый раннер (тесты);
// дефолт — exec.CommandContext(/bin/sh -c), без Perl _execScript.

import (
	"os/exec"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// startupCommand — команда для запуска при подключении, если 'startup launch'
// включён и задана строка 'startup script'; ok=false — ничего не запускать.
// M31-остаток defaults-fallback: если соединение не задаёт, читаем глобальные
// defaults 'startup launch'/'startup script' (как resolveLogConfig).
func startupCommand(env *config.Environment, d *config.Defaults) (string, bool) {
	launch := 0
	script := ""
	if env != nil {
		launch = int(env.StartUpLaunch)
		script = env.StartUpScript
	}
	// M31-остаток defaults-fallback: если соединение не включает startup, берём
	// ВСЮ пару из глобальных defaults (атомарно, как resolveLogConfig) — иначе
	// смешали бы launch из defaults со скриптом из env (ревью MAJOR-1).
	if env == nil || launch != 1 {
		launch = rawIntD(d, "startup launch")
		script = rawStrD(d, "startup script")
	}
	if launch == 1 && script != "" {
		return script, true
	}
	return "", false
}

// runStartup — запуск startup-команды при подключении (инъектируемый раннер).
func (s *Session) runStartup() {
	cmd, ok := startupCommand(s.envConfig(), s.opts.defaults)
	if !ok {
		return
	}
	if s.opts.startupRunner != nil {
		s.opts.startupRunner(s.ctx, cmd)
		return
	}
	// M31-ревью #3: асинхронно (.Start + go .Wait) — иначе блокирующий
	// startup-скрипт стопорит run-цикл сессии (чтение/события). Ошибки не роняют.
	c := exec.CommandContext(s.ctx, "/bin/sh", "-c", cmd)
	if err := c.Start(); err != nil {
		return
	}
	go func() { _ = c.Wait() }()
}
