package ui

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/session"
)

// sharedTmp — один каталог на время жизни приложения (аналог $ASBRU_TMP):
// во избежание утечек каталогов/файлов паролей на каждый клик (ревью F3).
var sharedTmp struct {
	sync.Once
	dir string
	err error
}

func tmpDir() (string, error) {
	sharedTmp.Do(func() {
		// M25: --temp-dir/ASBRU_TMP — база для временных файлов (легаси
		// asbru-cm:72-73); по умолчанию системный временный каталог.
		// prepareTmpDir создаёт базу, если задана (иначе MkdirTemp падает).
		sharedTmp.dir, sharedTmp.err = prepareTmpDir(os.Getenv("ASBRU_TMP"))
	})
	return sharedTmp.dir, sharedTmp.err
}

// prepareTmpDir — временный подкаталог под базой ASBRU_TMP; база создаётся,
// если задана (ревью M25: --temp-dir=/несуществующий не должен ломать
// VNC-сессии, которым нужен tmp). Пустая база — системный временный каталог.
func prepareTmpDir(base string) (string, error) {
	if base != "" {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(base, "asbru-tmp-*")
}

// newBuildEnv — контекст запуска сессии из окружения соединения и defaults.
// Shell binary/options берутся из defaults (M29-ревью I2: иначе Local Shell
// игнорировал настроенный shell — BuildEnv.ShellBinary/ShellOptions оставались
// пустыми, cmdline.go:102-110 падал на $SHELL//bin/bash без опций).
func newBuildEnv(uuid string, env *config.Environment, d *config.Defaults) (session.BuildEnv, error) {
	if env == nil {
		return session.BuildEnv{}, fmt.Errorf("пустое окружение соединения")
	}
	tmp, err := tmpDir()
	if err != nil {
		return session.BuildEnv{}, err
	}
	sudoPrompt := ""
	if d != nil {
		sudoPrompt = d.SudoPrompt
	}
	if sudoPrompt == "" {
		sudoPrompt = config.DefaultSudoPrompt // M30: дефолт, если defaults пуст
	}
	// бэклог Incognito: флаг 'create random socks tunnel' → случайный порт только для
	// SSH (легаси PACTerminal.pm:411 gated по /ssh/i), чтобы BuildEnv.RandomSocks
	// собрал -D localhost:PORT и оживил <SOCKS5_PORT>. При ошибке аллокатора (0)
	// НЕ ставим (иначе -D localhost:0). Не-SSH → порт не аллоцируем (источника нет).
	randomSocks := ""
	if env.EnableRandomSocks == 1 && strings.Contains(strings.ToLower(env.Method), "ssh") {
		if p := allocSocksPort(); p > 0 {
			randomSocks = strconv.Itoa(p)
		}
	}
	return session.BuildEnv{
		UUID:         uuid,
		Name:         env.Name,
		TmpDir:       tmp,
		ShellBinary:  rawStr(d, "shell binary"),
		ShellOptions: rawStr(d, "shell options"),
		SudoPrompt:   sudoPrompt,
		RandomSocks:  randomSocks,
		Defaults:     d, // M35 P0: defaults-proxy (use proxy=0 → defaults.proxy)
	}, nil
}

// allocRandomPort — эфемерный свободный локальный TCP-порт (бэклог Incognito):
// bind 127.0.0.1:0 → забрать назначенный номер → закрыть. 0 при ошибке.
func allocRandomPort() int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer l.Close()
	if a, ok := l.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// allocSocksPort — инжектируемый аллокатор порта SOCKS (для юнит-тестов newBuildEnv).
var allocSocksPort = allocRandomPort
