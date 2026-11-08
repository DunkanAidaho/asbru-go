package ui

// M30-остаток: структурные виджеты -L/-R/-D (SSH forwarding) в редакторе сверх
// сырого поля options. Чистая пара forwardRows/applyForwardRows (TDD); qt-таблица
// в "Опции" — build-only.

import (
	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// startsLikeOption — токен похож на отдельную опцию (после голого -L/-R/-D её
// НЕ следует съедать как spec; MINOR-8).
func startsLikeOption(tok string) bool {
	return len(tok) > 0 && (tok[0] == '-' || tok[0] == '/' || tok[0] == '+')
}

// forwardRow — один SSH-forward: Kind L/R/D, Spec (bind:point).
type forwardRow struct {
	Kind string // "L" | "R" | "D"
	Spec string // e.g. "8080:localhost:80"
}

// forwardRows — разбор -L/-R/-D из options (формы "-L spec" и "-Lspec").
func forwardRows(options string) []forwardRow {
	toks := method.SplitArgs(options)
	var out []forwardRow
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		kind := ""
		var spec string
		if len(t) >= 2 && t[0] == '-' && (t[1] == 'L' || t[1] == 'R' || t[1] == 'D') {
			kind = string(t[1])
			if len(t) > 2 {
				spec = t[2:]
			} else if i+1 < len(toks) {
				spec = toks[i+1]
				i++
			}
		}
		if kind != "" {
			out = append(out, forwardRow{Kind: kind, Spec: spec})
		}
	}
	return out
}

// applyForwardRows — убрать все -L/-R/-D из options, затем добавить строки.
// Прочие токены сохраняются; метод.JoinArgs пере-кавычивает токены с пробелами
// (как SetToggle) — иначе -o "A=B C" развалилось бы на куски.
func applyForwardRows(options string, rows []forwardRow) string {
	toks := method.SplitArgs(options)
	kept := make([]string, 0, len(toks))
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if len(t) >= 2 && t[0] == '-' && (t[1] == 'L' || t[1] == 'R' || t[1] == 'D') {
			if len(t) == 2 && i+1 < len(toks) && !startsLikeOption(toks[i+1]) {
				i++ // пропустить отдельный spec-токен (не съедая опцию после голого -L)
			}
			continue
		}
		kept = append(kept, t)
	}
	for _, r := range rows {
		if r.Kind != "L" && r.Kind != "R" && r.Kind != "D" {
			continue
		}
		if r.Spec != "" {
			kept = append(kept, "-"+r.Kind, r.Spec)
		} else {
			kept = append(kept, "-"+r.Kind)
		}
	}
	return method.JoinArgs(kept)
}
