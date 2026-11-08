package tree

// Bulk edit — массовое изменение полей выбранных соединений (M36 tree_menu).
// Логика отделена от Qt-диалога: чистая функция над картой environments.

import (
	"regexp"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// FieldEdit — одно желаемое изменение поля: литеральная установка Value
// (IsRegex=false) или regex-подстановка s/Match/Replace/ (IsRegex=true).
type FieldEdit struct {
	Field   string
	Value   string // литеральное значение (при IsRegex=false)
	IsRegex bool
	Match   string // regex-паттерн (при IsRegex=true)
	Replace string // замена (при IsRegex=true)
}

// ExpandSelection — раскрытие выбранных uuid: группы → дети (рекурсивно или
// только 1-й уровень). Возвращает множество задействованных uuid (включая
// выбранные группы). Если Recurse=false — дети только 1-го уровня.
func ExpandSelection(envs map[string]config.Environment, sel []string, recurse bool) map[string]bool {
	out := map[string]bool{}
	if recurse {
		seen := map[string]bool{}
		var walk func(string)
		walk = func(u string) {
			if seen[u] {
				return
			}
			seen[u] = true
			out[u] = true
			if e, ok := envs[u]; ok && e.IsGroup == 1 {
				for c := range e.Children {
					walk(c)
				}
			}
		}
		for _, u := range sel {
			walk(u)
		}
		return out
	}
	for _, u := range sel {
		out[u] = true
		if e, ok := envs[u]; ok && e.IsGroup == 1 {
			for c := range e.Children {
				out[c] = true
			}
		}
	}
	return out
}

// ApplyBulkEdits — применяет FieldEdit-ы ко всем задействованным окружениям.
// Селектор раскрывается через ExpandSelection с рекурсией (группа → дети).
// Группы по умолчанию НЕ меняются (applyToGroups=false): только листья;
// для групп применяется правка к их детям. Возвращает число изменённых env.
func ApplyBulkEdits(envs map[string]config.Environment, sel []string, edits []FieldEdit, recurse bool) int {
	if len(edits) == 0 {
		return 0
	}
	targets := ExpandSelection(envs, sel, recurse)
	changed := 0
	for u := range targets {
		e, ok := envs[u]
		if !ok {
			continue
		}
		if e.IsGroup == 1 {
			continue // группы не редактируем правкой (только дети через раскрытие)
		}
		if applyEditsToEnv(&e, edits) {
			envs[u] = e
			changed++
		}
	}
	return changed
}

// applyEditsToEnv — применяет set-редактирования к одному окружению;
// возвращает true, если что-то изменилось.
func applyEditsToEnv(e *config.Environment, edits []FieldEdit) bool {
	changed := false
	for _, ed := range edits {
		if applyOneEdit(e, ed) {
			changed = true
		}
	}
	return changed
}

// applyOneEdit — одно редактирование (литерал или regex). Возвращает true при
// фактическом изменении поля; некорректный regex — false (не применяется).
func applyOneEdit(e *config.Environment, ed FieldEdit) bool {
	cur := fieldStr(e, ed.Field)
	if ed.IsRegex {
		if ed.Match == "" {
			return false
		}
		re, err := regexp.Compile(ed.Match)
		if err != nil {
			return false
		}
		if !re.MatchString(cur) {
			return false
		}
		repl := re.ReplaceAllString(cur, ed.Replace)
		if repl == cur {
			return false // замена воспроизводит то же значение — не считаем изменением
		}
		setFieldStr(e, ed.Field, repl)
		return true
	}
	// литерал: всегда обновляем (в т.ч. пустым — «очистить»), если отличается.
	if cur == ed.Value {
		return false
	}
	setFieldStr(e, ed.Field, ed.Value)
	return true
}

// fieldStr — строковое значение поля (неизвестное поле → "").
func fieldStr(e *config.Environment, f string) string {
	switch f {
	case "name":
		return e.Name
	case "title":
		return e.Title
	case "ip":
		return e.IP
	case "port":
		return e.Port
	case "user":
		return e.User
	case "pass":
		return e.Pass
	case "public key":
		return e.PublicKey
	case "passphrase user":
		return e.PassphraseUser
	case "passphrase":
		return e.Passphrase
	case "options":
		return e.Options
	}
	return ""
}

// setFieldStr — устанавливает строковое поле (неизвестное поле — no-op).
func setFieldStr(e *config.Environment, f, v string) {
	switch f {
	case "name":
		e.Name = v
	case "title":
		e.Title = v
	case "ip":
		e.IP = v
	case "port":
		e.Port = v
	case "user":
		e.User = v
	case "pass":
		e.Pass = v
	case "public key":
		e.PublicKey = v
	case "passphrase user":
		e.PassphraseUser = v
	case "passphrase":
		e.Passphrase = v
	case "options":
		e.Options = v
	}
}
