package session

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Resolver — источники подстановок (<V:>, <GV:>, <ENV:>, <ASK:>, <CMD:>).
// Маски вида <field|uuid> (KeePass) оставляются как есть — резолвит keepass-слой.
type Resolver struct {
	Vars  map[string]string
	GVars map[string]string
	Env   func(string) (string, bool) // M35 P0: ok=false → переменная не задана (литерал)
	Ask   func(string) (string, error)
	Local map[string]string // <NAME>/<IP>/<TIMESTAMP>-маски (LOCAL_VARS)
}

var (
	reV   = regexp.MustCompile(`<V:([^>]+)>`)
	reGV  = regexp.MustCompile(`<GV:([^>]+)>`)
	reEnv = regexp.MustCompile(`<ENV:([^>]+)>`)
	reAsk = regexp.MustCompile(`<ASK:([^>]+)>`)
	reCmd = regexp.MustCompile(`<CMD:([^>]+)>`)
)

// Substitute — порт движка _subst (asbru_conn), без exec-обёрток.
// Нулевой резолвер (ни Local, ни V, ни GV, ни ENV, ни ASK) значит «источников
// нет»: маски остаются как есть, включая <CMD:> (безопасность по умолчанию).
func Substitute(input string, r Resolver) string {
	return substitute(input, r, true)
}

// SubstituteValues — подстановка ТОЛЬКО значений (local/<V:>/<GV:>/<ENV:>),
// БЕЗ исполнения <CMD:> и без <ASK:> (вставка значения в терминал не должна
// локально исполнять команду — M36-ревью MINOR: иначе user-ввод при <CMD:>
// шеллил бы на клиенте). <CMD:>/<ASK:> остаются литералом.
func SubstituteValues(input string, r Resolver) string {
	return substitute(input, r, false)
}

// substitute — общий движок; execCMD=false отключает <CMD:> и <ASK:>.
func substitute(input string, r Resolver, execCMD bool) string {
	active := r.Vars != nil || r.GVars != nil || r.Env != nil || r.Ask != nil || r.Local != nil
	if !active {
		return input
	}
	if r.Local != nil {
		input = reLocal.ReplaceAllStringFunc(input, func(m string) string {
			if v, ok := r.Local[m[1:len(m)-1]]; ok {
				return v
			}
			return m
		})
	}
	input = replaceNamed(r.Vars, reV, input, len("<V:"))
	input = replaceNamed(r.GVars, reGV, input, len("<GV:"))
	if r.Env != nil {
		input = reEnv.ReplaceAllStringFunc(input, func(m string) string {
			name := m[len("<ENV:") : len(m)-1]
			// M35 P0: переменная не задана → оставляем литерал (Perl defined $ENV).
			if v, ok := r.Env(name); ok {
				return v
			}
			return m
		})
	}
	if execCMD {
		if r.Ask != nil {
			input = reAsk.ReplaceAllStringFunc(input, func(m string) string {
				q := m[len("<ASK:") : len(m)-1]
				if v, err := r.Ask(q); err == nil {
					return v
				}
				return ""
			})
		}
		// <CMD:> — исполнение с таймаутом 10с (L3: без него зависшая команда
		// вешает старт сессии; легаси-инь параллельн ie — оставляем контракт).
		input = reCmd.ReplaceAllStringFunc(input, func(m string) string {
			cmd := m[len("<CMD:") : len(m)-1]
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/bin/sh", "-c", cmd).CombinedOutput() // легаси: 2>&1
			if err != nil {
				return ""
			}
			// легаси chomp'ит вывод внешней команды (asbru_conn:467-468)
			return strings.TrimSuffix(string(out), "\n")
		})
	}
	return input
}

func replaceNamed(m map[string]string, re *regexp.Regexp, input string, prefixLen int) string {
	if m == nil {
		return input
	}
	return re.ReplaceAllStringFunc(input, func(matched string) string {
		name := matched[prefixLen : len(matched)-1]
		if v, ok := m[name]; ok {
			return v
		}
		// M35 P0: переменная не определена → оставляем литерал (Perl _subst):
		// замена только если ключ есть в карте, иначе маска остаётся как есть.
		return matched
	})
}

// Local — локальные маски легаси LOCAL_VARS (PACUtils:2877-2893):
// <UUID>, <NAME>, <TITLE>, <IP>, <PORT>, <USER>, <PASS>, <TIMESTAMP>,
// <DATE_Y|M|D>, <TIME_H|M|S>, <SOCKS5_PORT>. Индексы подставляются только
// при непустом Local (нулевой резолвер инертен — см. Substitute).
var reLocal = regexp.MustCompile(`<(UUID|SOCKS5_PORT|TIMESTAMP|DATE_Y|DATE_M|DATE_D|TIME_H|TIME_M|TIME_S|NAME|TITLE|IP|PORT|USER|PASS)>`)

// active — есть ли хоть один источник подстановок (нулевой резолвер инертен).
func (r Resolver) active() bool {
	return r.Vars != nil || r.GVars != nil || r.Env != nil || r.Ask != nil || r.Local != nil
}

// fillFromEnv — env-зависимая часть резолвера: LOCAL_VARS и variables[N].
// Вынесено из ResolverFromConfig, чтобы RefreshConfig могла пересобрать эти
// значения после правки соединения (Finding #9), сохранив GVars/Env/Ask.
//
// ag-vl1: карты Local/Vars строятся заново и публикуются заменой полей, а НЕ
// мутируются на месте. Читатели (send-string тикер, expect, preparedEnv, UI)
// держат копию структуры Resolver и читают карты уже после отпускания optsMu,
// поэтому запись в общую карту — fatal concurrent map read/write.
func (r *Resolver) fillFromEnv(env BuildEnv, e *config.Environment) {
	local := make(map[string]string, 16)
	vars := map[string]string{}
	local["UUID"] = env.UUID
	// дата-макросы (легаси strftime + time; SOCKS5_PORT — из случайного туннеля)
	now := time.Now()
	local["TIMESTAMP"] = strconv.FormatInt(now.Unix(), 10)
	local["DATE_Y"] = now.Format("2006")
	local["DATE_M"] = now.Format("01")
	local["DATE_D"] = now.Format("02")
	local["TIME_H"] = now.Format("15")
	local["TIME_M"] = now.Format("04")
	local["TIME_S"] = now.Format("05")
	local["SOCKS5_PORT"] = env.RandomSocks
	if e != nil {
		local["NAME"] = e.Name
		local["TITLE"] = e.TitleOrName()
		local["IP"] = e.IP
		local["PORT"] = e.Port
		// M35 P0: <USER>/<PASS> для publickey — passphrase-user/passphrase (легаси
		// берёт эти же поля), для userpass — User/Pass.
		if e.AuthType == "publickey" {
			local["USER"] = e.PassphraseUser
			local["PASS"] = e.Passphrase
		} else {
			local["USER"] = e.User
			local["PASS"] = e.Pass
		}
		for i, m := range e.Variables {
			if txt, ok := m["txt"].(string); ok {
				vars[strconv.Itoa(i)] = txt
			}
		}
	}
	r.Local = local
	r.Vars = vars
}

// ResolverFromConfig — связка подстановок из конфига + переменных соединения
// (легаси _subst lookup: GVars, variables[N].txt, ENV).
func ResolverFromConfig(env BuildEnv, cfg *config.Config, e *config.Environment) Resolver {
	r := Resolver{
		Env:   os.LookupEnv, // M35 P0: ok=false для незаданных → литерал
		Vars:  map[string]string{},
		GVars: map[string]string{},
		Local: map[string]string{},
	}
	r.fillFromEnv(env, e)
	if cfg != nil {
		for name, v := range cfg.Defaults.GlobalVariables {
			r.GVars[name] = v.Value
		}
	}
	return r
}
