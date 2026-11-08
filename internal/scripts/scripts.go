// Package scripts — пользовательские скрипты Ásbrú (M32, PACScripts).
// Современная реализация: поиск по имени (без path-traversal) + запуск как
// подпроцесс; API-движок/контракт поверх — последующий шаг.
package scripts

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ScriptsDir — каталог пользовательских скриптов (легаси $ASBRU_CFG/scripts).
func ScriptsDir(cfgDir string) string {
	return filepath.Join(cfgDir, "scripts")
}

// Lookup — путь скрипта по имени в каталоге; отклоняет path-traversal,
// каталоги и отсутствующие файлы. ok=false — не найден.
func Lookup(scriptsDir, name string) (string, bool) {
	if !ValidName(name) {
		return "", false
	}
	p := filepath.Join(scriptsDir, name)
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return "", false
	}
	return p, true
}

// ValidName — безопасно ли имя скрипта (файл в каталоге скриптов): непустое,
// без сепараторов пути/`..`/ведущей точки, не каталог-маркер. Используется и
// Lookup, и редактором скриптов (создание/перезапись).
func ValidName(name string) bool {
	if name == "" || strings.ContainsAny(name, "/\\") ||
		strings.Contains(name, "..") || strings.HasPrefix(name, ".") {
		return false
	}
	return true
}

// Path — путь нового/существующего скрипта с проверкой имени. ok=false, если имя
// невалидно (существование файла не требуется — для создания). Каталог создаётся.
func Path(scriptsDir, name string) (string, bool) {
	if !ValidName(name) {
		return "", false
	}
	p := filepath.Join(scriptsDir, name)
	_ = os.MkdirAll(scriptsDir, 0o755) // каталог скриптов создаётся при записи
	return p, true
}

// Run — выполняет команду (собственно скрипт/программу) как подпроцесс и
// возвращает CombinedOutput. Ошибки ненулевого кода возвращаются вместе с выводом.
func Run(ctx context.Context, path string, args []string) (string, error) {
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}
