package keepass

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tobischo/gokeepasslib/v3"
)

// buildSearchDB — вложенная структура групп для тестов SearchPassword (рекурсия),
// пустой пароль у одной записи (должна пропускаться).
func buildSearchDB(t *testing.T) string {
	t.Helper()
	db := gokeepasslib.NewDatabase()
	db.Content.Root = gokeepasslib.NewRootData()
	db.Content.Root.Groups = []gokeepasslib.Group{
		{Name: "Servers", Entries: []gokeepasslib.Entry{
			{Values: []gokeepasslib.ValueData{
				{Key: "Title", Value: gokeepasslib.V{Content: "Web Server"}},
				{Key: "Password", Value: gokeepasslib.V{Content: "pw-web"}},
			}},
			{Values: []gokeepasslib.ValueData{
				{Key: "Title", Value: gokeepasslib.V{Content: "Empty"}},
				{Key: "Password", Value: gokeepasslib.V{Content: ""}},
			}},
		}},
		{Name: "Databases", Entries: []gokeepasslib.Entry{{
			Values: []gokeepasslib.ValueData{
				{Key: "Title", Value: gokeepasslib.V{Content: "DB Prod"}},
				{Key: "Password", Value: gokeepasslib.V{Content: "pw-db"}},
			},
		}}, Groups: []gokeepasslib.Group{{Name: "Staging", Entries: []gokeepasslib.Entry{{
			Values: []gokeepasslib.ValueData{
				{Key: "Title", Value: gokeepasslib.V{Content: "Staging API"}},
				{Key: "Password", Value: gokeepasslib.V{Content: "pw-staging"}},
			},
		}}}}},
	}
	db.Credentials = gokeepasslib.NewPasswordCredentials("master")
	path := filepath.Join(t.TempDir(), "vault.kdbx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := encodeDB(db, f); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

// бэклог: SearchPassOnKPX — пароль первой записи, чей title соответствует regexp;
// рекурсия в подгруппы; пустой пароль пропускается.
func TestVaultSearchPassword(t *testing.T) {
	path := buildSearchDB(t)
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	cases := []struct {
		pattern string
		want    string
	}{
		{"Web Server", "pw-web"},      // точный title
		{"^DB", "pw-db"},              // regexp-префикс
		{".*Server.*", "pw-web"},      // вида ".*title.*"
		{"(?i)^db", "pw-db"},          // регистронезависимый флаг
		{"Staging API", "pw-staging"}, // рекурсия в подгруппу Staging
	}
	for _, c := range cases {
		got, err := v.SearchPassword(c.pattern)
		if err != nil {
			t.Errorf("SearchPassword(%q): %v", c.pattern, err)
			continue
		}
		if got != c.want {
			t.Errorf("SearchPassword(%q) = %q, хотим %q", c.pattern, got, c.want)
		}
	}
	// запись с пустым паролем «Empty» → пропускается (нет совпадения с паролем).
	if _, err := v.SearchPassword("Empty"); err != ErrNotFound {
		t.Errorf("SearchPassword(Empty) err = %v, хотим ErrNotFound (пустой пароль)", err)
	}
	// нет совпадения → ErrNotFound.
	if _, err := v.SearchPassword("Nomatch"); err != ErrNotFound {
		t.Errorf("SearchPassword(Nomatch) err = %v, хотим ErrNotFound", err)
	}
	// невалидный regexp → ошибка, не паника.
	if _, err := v.SearchPassword("("); err == nil {
		t.Errorf("SearchPassword('(') err = nil, хотим ошибку")
	}
}

// Регресс: при нескольких совпадениях SearchPassword обязан вернуть ПЕРВУЮ
// запись в порядке DFS. Раньше walk не пробрасывал «нашли» наружу: return
// выходил только из вложенного вызова, внешние циклы продолжались и found
// перезаписывался последним совпадением.
func TestVaultSearchPasswordFirstMatch(t *testing.T) {
	db := gokeepasslib.NewDatabase()
	db.Content.Root = gokeepasslib.NewRootData()
	db.Content.Root.Groups = []gokeepasslib.Group{
		// Первое совпадение DFS — во вложенной подгруппе A/A1.
		{Name: "A", Groups: []gokeepasslib.Group{{Name: "A1", Entries: []gokeepasslib.Entry{{
			Values: []gokeepasslib.ValueData{
				{Key: "Title", Value: gokeepasslib.V{Content: "First"}},
				{Key: "Password", Value: gokeepasslib.V{Content: "pw-first"}},
			},
		}}}}},
		// Второе совпадение — в следующей группе верхнего уровня.
		{Name: "B", Entries: []gokeepasslib.Entry{{
			Values: []gokeepasslib.ValueData{
				{Key: "Title", Value: gokeepasslib.V{Content: "Second"}},
				{Key: "Password", Value: gokeepasslib.V{Content: "pw-second"}},
			},
		}}},
	}
	db.Credentials = gokeepasslib.NewPasswordCredentials("master")
	path := filepath.Join(t.TempDir(), "first.kdbx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := encodeDB(db, f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()

	// ".*" совпадает с обеими записями: первая DFS — First (pw-first).
	got, err := v.SearchPassword(".*")
	if err != nil {
		t.Fatalf("SearchPassword(.*): %v", err)
	}
	if got != "pw-first" {
		t.Fatalf("SearchPassword(.*) = %q, хотим первую запись DFS %q", got, "pw-first")
	}
}

// бэклог: SearchPassword после Close → ErrClosed.
func TestVaultSearchPasswordClosed(t *testing.T) {
	path := buildSearchDB(t)
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := v.SearchPassword("Web"); err != ErrClosed {
		t.Errorf("SearchPassword после Close err = %v, хотим ErrClosed", err)
	}
}
