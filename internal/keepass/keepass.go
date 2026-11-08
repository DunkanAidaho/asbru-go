// Пакет keepass: нативное чтение .kdbx (gokeepasslib) вместо keepassxc-cli.
package keepass

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/tobischo/gokeepasslib/v3"
)

// FieldResolver — интерфейс доступа к полю записи (Vault реализует).
type FieldResolver interface {
	Field(field, path string) (string, error)
}

// ErrNotFound — запись/группа не найдены.
var ErrNotFound = errors.New("keepass: entry not found")

// ErrClosed — обращение к базе после Close.
var ErrClosed = errors.New("keepass: vault closed")

// Vault — открытая база KeePass.
type Vault struct {
	db     *gokeepasslib.Database
	mu     sync.Mutex
	closed bool
}

// unlockErr — распознать известные ошибки разблокировки. M6: gokeepasslib не
// поддерживает KDBX 3.1/ARC4 (InnerRandomStreamID=1) → понятное сообщение.
func unlockErr(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if strings.Contains(s, "stream type") || strings.Contains(s, "not implemented") ||
		strings.Contains(s, "unsupported") {
		return fmt.Errorf("keepass: KDBX 3.1/ARC4-поток не поддерживается (пересохраните базу в KDBX 4): %w", err)
	}
	return fmt.Errorf("keepass: unlock: %w", err)
}

// Open открывает kdbx (пароль + опциональный keyfile).
func Open(path, password, keyfile string) (*Vault, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	db := gokeepasslib.NewDatabase()
	var cred *gokeepasslib.DBCredentials
	if keyfile != "" {
		cred, err = gokeepasslib.NewPasswordAndKeyCredentials(password, keyfile)
		if err != nil {
			return nil, fmt.Errorf("keepass: keyfile: %w", err)
		}
	} else {
		cred = gokeepasslib.NewPasswordCredentials(password)
	}
	db.Credentials = cred

	if err := gokeepasslib.NewDecoder(f).Decode(db); err != nil {
		return nil, fmt.Errorf("keepass: decode: %w", err)
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, unlockErr(err)
	}
	return &Vault{db: db}, nil
}

// Close освобождает ресурсы базы; после Close Field возвращает ErrClosed
// (ревью M4: повторная блокировка давала шифр-мусор вместо ошибки).
func (v *Vault) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	v.closed = true
	return v.db.LockProtectedEntries()
}

// Field возвращает значение поля записи (title/username/password/url)
// по пути вида "/Group/SubGroup/EntryTitle" (совместим с масками <field|uuid>).
func (v *Vault) Field(field, path string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return "", ErrClosed
	}
	entry, err := v.resolve(path)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(field) {
	case "title":
		return entry.GetTitle(), nil
	case "username":
		return entry.GetContent("UserName"), nil
	case "url":
		return entry.GetContent("URL"), nil
	case "password":
		return entry.GetPassword(), nil
	}
	return "", fmt.Errorf("keepass: unknown field %q", field)
}

// resolve: 1) канонический путь /Group/.../Entry; 2) fallback на
// рекурсивный поиск по голому title — ТОЛЬКО для пути без / (легаси-маска
// <field|title> без группового пути работала через `show db 'title'`).
// Для путей с "/" неудача группы — ошибка: иначе чужая запись с тем же
// титулом в другой группе подсунула бы чужие креды (ревью N1).
func (v *Vault) resolve(path string) (*gokeepasslib.Entry, error) {
	if e, err := v.resolvePath(path); err == nil {
		return e, nil
	}
	if strings.Contains(path, "/") {
		return nil, ErrNotFound
	}
	return v.searchAnyRecursive(path)
}

func (v *Vault) resolvePath(path string) (*gokeepasslib.Entry, error) {
	if v.db.Content == nil || v.db.Content.Root == nil {
		return nil, ErrNotFound
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	groups := v.db.Content.Root.Groups
	var entries []gokeepasslib.Entry
	for i, seg := range segs {
		last := i == len(segs)-1
		if last {
			if e := findIn(entries, seg); e != nil {
				return e, nil
			}
			return nil, ErrNotFound
		}
		found := false
		for _, g := range groups {
			if strings.EqualFold(g.Name, seg) {
				groups = g.Groups
				entries = g.Entries
				found = true
				break
			}
		}
		if !found {
			return nil, ErrNotFound
		}
	}
	return nil, ErrNotFound
}

// searchAnyRecursive — полный обход дерева групп по title.
func (v *Vault) searchAnyRecursive(title string) (*gokeepasslib.Entry, error) {
	if v.db.Content == nil || v.db.Content.Root == nil {
		return nil, ErrNotFound
	}
	var walk func(groups []gokeepasslib.Group) *gokeepasslib.Entry
	walk = func(groups []gokeepasslib.Group) *gokeepasslib.Entry {
		for _, g := range groups {
			if e := findIn(g.Entries, title); e != nil {
				return e
			}
			if e := walk(g.Groups); e != nil {
				return e
			}
		}
		return nil
	}
	if e := walk(v.db.Content.Root.Groups); e != nil {
		return e, nil
	}
	return nil, ErrNotFound
}

// SearchPassword — пароль первой записи, чей title соответствует regexp pattern
// (полный обход дерева; бэклог SearchPassOnKPX/KPXTitleRegexp). Записи с ПУСТЫМ
// паролем пропускаются (иначе (pw="", nil) дал бы затирание сохранённого пароля).
// Возвращает ErrNotFound, если совпадений с непустым паролем нет; ErrClosed после
// Close; ошибку, если regexp невалиден. Совпадение регистрозависимое.
func (v *Vault) SearchPassword(pattern string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("keepass: regexp: %w", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return "", ErrClosed
	}
	if v.db.Content == nil || v.db.Content.Root == nil {
		return "", ErrNotFound
	}
	var found *gokeepasslib.Entry
	// walk возвращает true, если совпадение уже найдено: иначе return из
	// вложенного вызова не останавливал внешние циклы, и found перезаписывался
	// последним совпадением DFS (нужен ПЕРВЫЙ матч).
	var walk func(groups []gokeepasslib.Group) bool
	walk = func(groups []gokeepasslib.Group) bool {
		for _, g := range groups {
			for j := range g.Entries {
				if re.MatchString(g.Entries[j].GetTitle()) && g.Entries[j].GetPassword() != "" {
					found = &g.Entries[j]
					return true
				}
			}
			if walk(g.Groups) {
				return true
			}
		}
		return false
	}
	walk(v.db.Content.Root.Groups)
	if found == nil {
		return "", ErrNotFound
	}
	return found.GetPassword(), nil
}

func findIn(entries []gokeepasslib.Entry, title string) *gokeepasslib.Entry {
	for i, e := range entries {
		if strings.EqualFold(e.GetTitle(), title) {
			return &entries[i]
		}
	}
	return nil
}
