// Package cli — точка входа командной строки Ásbrú (Go-порт).
package cli

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/ipc"
	"github.com/DunkanAidaho/asbru-go/internal/lock"
	"github.com/DunkanAidaho/asbru-go/internal/ui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Version — версия Go-порта (не совпадает с версией Perl проекта).
const Version = "0.1.0"

// runGUI — инжектируемый запуск GUI (тесты подменяют).
var runGUI = func(cfgPath string, start ui.Startup) error { return ui.RunWithStartup(cfgPath, start) }

// acquireLock — инжектируемая single-instance блокировка (тесты подменяют).
var acquireLock = func() (func(), error) {
	return lock.Acquire(filepath.Join(cfgDir(), "asbru.lock"))
}

// debugLogPath — файл debug-лога (живёт в каталоге конфига; GUI-лаунчер теряет
// stderr, поэтому при --debug всё пишем в файл — и лог, и crash-бэктрейс).
func debugLogPath() string {
	return filepath.Join(cfgDir(), "debug.log")
}

// setDebugLogging — включение debug-логирования (инжектируемо в тестах).
// При --debug: stderr переключается на <config>/debug.log, slog — Debug,
// а runtime/debug.SetTraceback("crash") заставляет Go-рантайм печатать ПОЛНЫЙ
// бэктрейс (все горутины + сигнал) при панике/SEGV — сюда же в файл. Это
// закрывает очный-дебаг «двойной клик — вылет без ошибки» (сегфолт, который
// recover не ловит): запуск `asbru --debug` → в debug.log и лог, и стек падения.
var setDebugLogging = func() {
	if f, err := os.OpenFile(debugLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		os.Stderr = f
	} else {
		slog.Warn("debug: не открыть файл лога", "err", err)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	debug.SetTraceback("crash")
}

// cfgDir — каталог конфигурации: $ASBRU_CFG или ~/.config/asbru.
func cfgDir() string {
	if d := os.Getenv("ASBRU_CFG"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "asbru")
}

// cfgPath — путь к asbru.yml текущего каталога конфигурации.
func cfgPath() string {
	return filepath.Join(cfgDir(), "asbru.yml")
}

// resolveConfigDir — M25: --config-dir (легаси asbru-cm:61-71): абсолютный
// путь — как есть, относительный — к ~/.config/.
func resolveConfigDir(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", p)
}

// applyDirFlags — M25: --config-dir/--temp-dir → ASBRU_CFG/ASBRU_TMP
// (легаси asbru-cm:61-73). Вызывается до любого использования cfgDir().
func applyDirFlags(cfgDirFlag, tmpDirFlag string) error {
	if cfgDirFlag != "" {
		if err := os.Setenv("ASBRU_CFG", resolveConfigDir(cfgDirFlag)); err != nil {
			return fmt.Errorf("asbru: --config-dir: %w", err)
		}
	}
	if tmpDirFlag != "" {
		if err := os.Setenv("ASBRU_TMP", tmpDirFlag); err != nil {
			return fmt.Errorf("asbru: --temp-dir: %w", err)
		}
	}
	return nil
}

// loadCliCfg — конфиг для не-GUI команд (--list-uuids/--dump-uuid):
// загрузка + Sanitize (как GUI); отсутствующий файл — пустой конфиг.
func loadCliCfg(path string) (*config.Config, error) {
	cfg, err := config.LoadConfig(path, config.NewCBC())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &config.Config{Environments: map[string]config.Environment{}}, nil
		}
		return nil, err
	}
	config.Sanitize(cfg)
	return cfg, nil
}

// listUUIDs — M25: --list-uuids (легаси PACMain.pm:218-234): таблица
// UUID|PARENT_UUID|TYPE|METHOD|NAME, без GUI; __PAC_SHELL__ и безымянные
// пропускаются.
func listUUIDs(w io.Writer, path string) error {
	cfg, err := loadCliCfg(path)
	if err != nil {
		return err
	}
	dash := strings.Repeat("-", 36) + "|" + strings.Repeat("-", 36) + "|" +
		strings.Repeat("-", 5) + "|" + strings.Repeat("-", 15) + "|" + strings.Repeat("-", 21)
	fmt.Fprintln(w, dash)
	fmt.Fprintf(w, "%36s|%36s|%5s|%15s|%s\n", "UUID", "PARENT_UUID", "TYPE", "METHOD", "NAME")
	fmt.Fprintln(w, dash)
	for uuid, env := range cfg.Environments {
		if env.Name == "" || uuid == "__PAC_SHELL__" {
			continue
		}
		typ := ""
		if env.IsGroup == 1 {
			typ = "GROUP"
		}
		fmt.Fprintf(w, "%s|%36s|%5s|%15s|%s\n", uuid, env.Parent, typ, env.Method, env.Name)
	}
	fmt.Fprintln(w, dash)
	return nil
}

// dumpUUID — M25: --dump-uuid=<uuid> (легаси PACMain.pm:209-214): данные
// соединения (YAML, только непустые поля + неизвестные Raw-ключи).
func dumpUUID(w io.Writer, path, uuid string) error {
	cfg, err := loadCliCfg(path)
	if err != nil {
		return err
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return fmt.Errorf("dump-uuid: соединение %q не найдено", uuid)
	}
	data, err := dumpEnvYAML(env)
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}

// dumpEnvYAML — окружение в YAML без пустых полей (легаси-дамп Data::Dumper
// показывал только определённые ключи).
func dumpEnvYAML(e config.Environment) ([]byte, error) {
	raw, err := yaml.Marshal(e)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	for k, v := range e.Raw {
		m[k] = v
	}
	clean := make(map[string]any, len(m))
	for k, v := range m {
		if !isEmptyYAML(v) {
			clean[k] = v
		}
	}
	return yaml.Marshal(clean)
}

// isEmptyYAML — «пустое» значение для дампа (легаси-ключи присутствуют
// только когда заданы).
func isEmptyYAML(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case bool:
		return !t
	case int:
		return t == 0
	case int64:
		return t == 0
	case uint64:
		return t == 0
	case float64:
		return t == 0
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	}
	return false
}

func launchGUI() error { return launchGUIWith(ui.Startup{}) }

// ipcMessage — сообщение живому инстансу (легаси PACMain.pm:269-292):
// приоритет start-shell > quick-conn > start-uuid > edit-uuid; иначе show-conn.
func ipcMessage(start ui.Startup) string {
	switch {
	case start.StartShell:
		return "start-shell"
	case start.QuickConn:
		return "quick-conn"
	case start.StartUUID != "":
		return "start-uuid|" + start.StartUUID
	case start.EditUUID != "":
		return "edit-uuid|" + start.EditUUID
	default:
		return "show-conn"
	}
}

// forwardToRunning — аудит #8: передать стартовое действие живому инстансу
// через D-Bus (org.asbru_cm.Forward) и тихо выйти. Bounded retry на гонку
// старта: имя ещё не зарегистрировано сервером, а lock уже взят. Окно до
// 8s — холодный старт Qt + загрузка конфига + трей/IPC могут занять >2.5s
// (MINOR-12: 2.5s давало ложный «форвардинг не удался» на медленном старте).
func forwardToRunning(start ui.Startup) error {
	msg := ipcMessage(start)
	deadline := time.Now().Add(8 * time.Second)
	var err error
	for {
		err = ipc.Send(ipc.Name(cfgDir()), msg)
		if !errors.Is(err, ipc.ErrNotRunning) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("asbru: уже запущен, но IPC-форвардинг не удался: %w", err)
	}
	return nil
}

// launchGUIWith — общий запуск GUI с single-instance блокировкой.
func launchGUIWith(start ui.Startup) error {
	// первый запуск: каталог конфигурации может отсутствовать (ревью N2)
	if err := os.MkdirAll(cfgDir(), 0o700); err != nil {
		return fmt.Errorf("asbru: создать каталог конфигурации: %w", err)
	}
	release, err := acquireLock()
	if err != nil {
		if err == lock.ErrHeld {
			// M26: вместо отказа — форвардинг флагов живому инстансу
			// (легаси: если уже запущен, _sendAppMessage + exit 0).
			return forwardToRunning(start)
		}
		return err
	}
	defer release()
	return runGUI(filepath.Join(cfgDir(), "asbru.yml"), start)
}

// NewRootCmd собирает корневую команду asbru (без подкоманды — GUI).
func NewRootCmd() *cobra.Command {
	var debug, verbose bool
	var cfgDirFlag, tmpDirFlag string
	var listUUIDsFlag bool
	var dumpUUIDFlag string
	var start ui.Startup
	cmd := &cobra.Command{
		Use:   "asbru",
		Short: "Ásbrú Connection Manager (Go)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// M25: информационные команды без GUI (легаси PACMain.pm:209-234)
			if listUUIDsFlag {
				return listUUIDs(cmd.OutOrStdout(), cfgPath())
			}
			if dumpUUIDFlag != "" {
				return dumpUUID(cmd.OutOrStdout(), cfgPath(), dumpUUIDFlag)
			}
			err := launchGUIWith(start)
			if errors.Is(err, ui.ErrGUIExit) {
				return nil // легаси: тихий exit 0 (M19-ревью)
			}
			return err
		},
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// M25: --config-dir/--temp-dir применяются до любого чтения
			// каталога конфигурации (легаси BEGIN-блок asbru-cm:61-77).
			if err := applyDirFlags(cfgDirFlag, tmpDirFlag); err != nil {
				return err
			}
			if debug || verbose { // M25: --verbose — алиас --debug
				setDebugLogging()
			}
			return nil
		},
	}
	cmd.PersistentFlags().BoolVar(&debug, "debug", false, "debug logging")
	// M25: флаги легаси (asbru-cm:32-58)
	cmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "display more debugging information")
	cmd.PersistentFlags().StringVar(&cfgDirFlag, "config-dir", "", "каталог конфигурации: абсолютный путь или относительный к ~/.config")
	cmd.PersistentFlags().StringVar(&tmpDirFlag, "temp-dir", "", "каталог временных файлов (ASBRU_TMP)")
	cmd.PersistentFlags().BoolVar(&listUUIDsFlag, "list-uuids", false, "list existing connections/groups and their UUIDs (без GUI)")
	cmd.PersistentFlags().StringVar(&dumpUUIDFlag, "dump-uuid", "", "dump data for given connection (без GUI)")
	cmd.PersistentFlags().StringVar(&start.GUIPassword, "password", "", "automatically logon with given password without prompting user")
	cmd.PersistentFlags().BoolVar(&start.Prefs, "preferences", false, "open global preferences dialog on startup")
	cmd.PersistentFlags().BoolVar(&start.Iconified, "iconified", false, "go to tray once started")
	cmd.PersistentFlags().BoolVar(&start.ReadOnly, "readonly", false, "start in read only mode (no config changes allowed)")
	cmd.PersistentFlags().BoolVar(&start.NoSplash, "no-splash", false, "no splash screen on startup (в порте сплэша нет — no-op)")
	cmd.PersistentFlags().BoolVar(&start.NoBackup, "no-backup", false, "do not create backup config files (порт пишет атомарно — no-op)")
	cmd.PersistentFlags().BoolVar(&start.Scripts, "scripts", false, "open scripts window (реализовано — ui.showScriptsWindow)")
	cmd.PersistentFlags().StringVar(&start.StartScript, "start-script", "", "start given script (реализовано — стартовый скрипт в конфиге)")
	// M17: стартовые флаги; persistent — `asbru gui --start-shell` тоже
	// работает (L3-7-ревью). --start-uuid поддерживает кластер-суффикс
	// :cluster (легаси asbru-cm:42).
	cmd.PersistentFlags().StringVar(&start.StartUUID, "start-uuid", "", "подключить соединение по UUID при старте (кластер-суффикс :cluster)")
	cmd.PersistentFlags().StringVar(&start.EditUUID, "edit-uuid", "", "открыть редактор соединения по UUID")
	cmd.PersistentFlags().BoolVar(&start.QuickConn, "quick-conn", false, "показать Quick Connect при старте")
	cmd.PersistentFlags().BoolVar(&start.StartShell, "start-shell", false, "запустить локальную оболочку при старте")

	cmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "print version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "asbru %s\n", Version)
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "gui",
		Short: "start the Ásbrú GUI (Qt6)",
		RunE: func(cmd *cobra.Command, args []string) error {
			// M25-ревью: информацияционные флаги работают и с подкомандой
			// gui (иначе 'asbru gui --list-uuids' молча запускал бы GUI).
			if listUUIDsFlag {
				return listUUIDs(cmd.OutOrStdout(), cfgPath())
			}
			if dumpUUIDFlag != "" {
				return dumpUUID(cmd.OutOrStdout(), cfgPath(), dumpUUIDFlag)
			}
			if err := launchGUIWith(start); errors.Is(err, ui.ErrGUIExit) {
				return nil // M19-ревью: тихий exit 0 (легаси)
			} else {
				return err
			}
		},
	})
	return cmd
}

// Execute запускает CLI и завершает процесс с кодом ошибки.
func Execute() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
