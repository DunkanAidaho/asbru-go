package ui

// Опциональная трассировка паник в файл (очный дебаг). Асбру часто запускается
// без терминала (GUI-лаунчер), поэтому stderr паники недоступен; ловим panic
// на границах действий и пишем value + debug.Stack() в crash-файл, чтобы
// диагностировать краш по стеку, а не гаданием. Ошибка записи файла не
// роняет приложение и не глушится молча (slog.Warn).

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

var crashMu sync.Mutex

// crashLogPath — файл трассировки: env ASBRU_CRASH_LOG, иначе <config>/crash.log.
func crashLogPath() string {
	if p := os.Getenv("ASBRU_CRASH_LOG"); p != "" {
		return p
	}
	if u, err := os.UserConfigDir(); err == nil {
		return filepath.Join(u, "asbru", "crash.log")
	}
	return filepath.Join(os.TempDir(), "asbru-crash.log")
}

// logCrash — дописать панику + стек в crash-файл (идемпотентно, без паники).
func logCrash(where string, r any) {
	crashMu.Lock()
	defer crashMu.Unlock()
	f, err := os.OpenFile(crashLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		slog.Warn("crash: не открыть crash-файл", "err", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "\n=== %s %s ===\npanic: %v\n%s\n",
		time.Now().Format(time.RFC3339), where, r, debug.Stack())
}

// crashRecover — defer-recоver: логирует панику места действия. Значение по
// умолчанию — НЕ ре-паниковать (иначе краш убивает приложение и стек не
// доносится до файла при GUI-лаунчере). Где нужен ре-паник — передаётся bool.
func crashRecover(where string, repanic ...bool) {
	r := recover()
	if r == nil {
		return
	}
	logCrash(where, r)
	if len(repanic) > 0 && repanic[0] {
		panic(r)
	}
}
