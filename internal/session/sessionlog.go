package session

// M31: 'save session logs' — путь и запись лога сессии (легаси PACTerminal.pm:160-166,
// PACUtils.pm:2127). Каталог и паттерн берутся из окружения соединения; макросы
// в паттерне (UUID/NAME/DATE_*/TIME_*) резолвятся через Substitute.

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// rawStrD — Raw-значение defaults как строка (для логирования/startup).
func rawStrD(d *config.Defaults, key string) string {
	if d == nil {
		return ""
	}
	if v, ok := d.Raw[key].(string); ok {
		return v
	}
	return ""
}

// rawIntD — Raw-значение defaults как int.
func rawIntD(d *config.Defaults, key string) int {
	if d == nil {
		return 0
	}
	switch v := d.Raw[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 0
}

// resolveLogConfig — эффективные настройки 'save session logs': окружение
// соединения, иначе глобальные defaults (revью M31 #4; легаси PACTerminal.pm:163-166).
func resolveLogConfig(env *config.Environment, d *config.Defaults) (folder, pattern string, amount int, ok bool) {
	if env != nil && env.SaveSessionLogs == 1 {
		return env.SessionLogsFolder, env.SessionLogPattern, int(env.SessionLogsAmount), true
	}
	if rawIntD(d, "save session logs") != 1 {
		return "", "", 0, false
	}
	folder = rawStrD(d, "session logs folder")
	pattern = rawStrD(d, "session log pattern")
	if folder == "" || pattern == "" {
		return "", "", 0, false
	}
	return folder, pattern, rawIntD(d, "session logs amount"), true
}

// sessionLogPath — путь лога сессии при включённом SaveSessionLogs; ok=false —
// логи выключены либо нет каталога/паттерна (нет корня для записи).
func sessionLogPath(env *config.Environment, r Resolver) (string, bool) {
	if env == nil || env.SaveSessionLogs != 1 {
		return "", false
	}
	folder, pattern := env.SessionLogsFolder, env.SessionLogPattern
	if folder == "" || pattern == "" {
		return "", false
	}
	return folder + "/" + Substitute(pattern, r), true
}

// sessionLogWriter — открывает файл лога сессии (создаёт каталог, append), если
// 'save session logs' включён (соединение ИЛИ defaults — M31 #4);
// ok=false — выключено/нет каталога/не открыть.
func (s *Session) sessionLogWriter(pe *config.Environment) (io.WriteCloser, bool) {
	folder, pattern, amount, ok := resolveLogConfig(pe, s.opts.defaults)
	if !ok {
		return nil, false
	}
	r := ResolverFromConfig(s.env, nil, pe)
	path := folder + "/" + Substitute(pattern, r)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, false
	}
	// M31-ревью #1: ротация — только файлы-логи сессии (по маске паттерна),
	// не всё подряд из каталога; держим newer N + новый файл.
	rotateSessionLogs(filepath.Dir(path), amount, sessionLogMask(pattern))
	return f, true
}

// reLogMacro — <MACRO>-токены в паттерне для маски (UUID/NAME/DATE_*/TIME_*).
var reLogMacro = regexp.MustCompile(`<[A-Z][A-Z0-9_:]*>`)

// sessionLogMask — glob-маска файлов логов из паттерна: <MACRO> → '*'.
// Паттерн '<UUID>_<NAME>_<DATE_Y>...<TIME_S>.txt' → '*_*_***_***.txt'.
func sessionLogMask(pattern string) string {
	return reLogMacro.ReplaceAllString(pattern, "*")
}

// rotateSessionLogs — удаляет самые старые ЛОГИ СЕССИИ сверх amount (легаси
// _deleteOldestSessionLog, PACUtils.pm:3301 фильтрует по маске). Современный
// вариант: листинг каталога + glob-маска + сортировка по mtime.
func rotateSessionLogs(folder string, amount int, mask string) {
	if amount <= 0 || mask == "" {
		return
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	type logFile struct {
		name string
		mod  time.Time
	}
	var files []logFile
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// M31-ревью #1: не трогаем посторонние файлы/чужие логи в каталоге.
		if ok, _ := filepath.Match(mask, e.Name()); !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, logFile{e.Name(), info.ModTime()})
	}
	if len(files) <= amount {
		return
	}
	// newest → oldest; удаляем всё после первых amount.
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, x := range files[amount:] {
		_ = os.Remove(filepath.Join(folder, x.name))
	}
}
