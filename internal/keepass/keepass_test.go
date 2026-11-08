package keepass

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeKDBX пишет kdbx-файл (пароль pass, вход Group/Entry) и возвращает путь.
func makeKDBX(t *testing.T, pass, keyfile string) string {
	t.Helper()
	db := newTempDB(t, pass, keyfile)

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

// Чтение: поле Entry по пути Group/Entry, пароль+username+title.
func TestVaultField(t *testing.T) {
	path := makeKDBX(t, "master", "")
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	cases := []struct {
		field string
		want  string
	}{
		{"password", "s3cret"},
		{"username", "root"},
		{"title", "Entry"},
		{"url", "https://example.test"},
	}
	for _, c := range cases {
		got, err := v.Field(c.field, "/Group/Entry")
		if err != nil {
			t.Errorf("Field(%q): %v", c.field, err)
			continue
		}
		if got != c.want {
			t.Errorf("Field(%q) = %q, want %q", c.field, got, c.want)
		}
	}
}

func TestVaultWrongMasterPassword(t *testing.T) {
	path := makeKDBX(t, "master", "")
	if _, err := Open(path, "wrong", ""); err == nil {
		t.Fatal("неверный мастер-пароль должен давать ошибку")
	}
}

func TestVaultKeyfile(t *testing.T) {
	keyfile := filepath.Join(t.TempDir(), "key.xml")
	writeKeyFile(t, keyfile)
	path := makeKDBX(t, "master", keyfile)

	v, err := Open(path, "master", keyfile)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if got, _ := v.Field("password", "/Group/Entry"); got != "s3cret" {
		t.Fatalf("пароль из keyfile-vault: %q", got)
	}
}

// Поле регистронезависимо при обращении.
func TestVaultFieldCaseInsensitive(t *testing.T) {
	path := makeKDBX(t, "master", "")
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if got, _ := v.Field("PASSWORD", "/Group/Entry"); got != "s3cret" {
		t.Fatalf("PASSWORD: %q", got)
	}
}

// Голый title (без группового пути) — легаси-маска <password|Entry> (ревью-находка H2).
func TestVaultFieldBareTitle(t *testing.T) {
	path := makeKDBX(t, "master", "")
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if got, err := v.Field("password", "Entry"); err != nil || got != "s3cret" {
		t.Fatalf("bare title: %q, %v", got, err)
	}
}

// Путь с "/" при неудаче группы: ошибка, не чужая запись с тем же титулом (ревью N1).
func TestVaultWrongGroupPathFails(t *testing.T) {
	path := makeKDBX(t, "master", "")
	v, err := Open(path, "master", "")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	if _, err := v.Field("password", "/Missing/Entry"); err == nil {
		t.Fatal("ожидалась ошибка ErrNotFound для несуществующего группового пути")
	}
}

// M6: KDBX 3.1/ARC4-поток не поддержан gokeepasslib — дать понятное сообщение.
func TestUnlockErrARC4(t *testing.T) {
	err := unlockErr(fmt.Errorf("unsupported stream type: ARC4StreamID"))
	if !strings.Contains(err.Error(), "KDBX 3.1") {
		t.Fatalf("ARC4-ошибка не распознана: %v", err)
	}
	if !strings.Contains(err.Error(), "ARC4") {
		t.Fatalf("нет упоминания ARC4: %v", err)
	}
	// обычная ошибка (не ARC4) — просто обёртка.
	err = unlockErr(fmt.Errorf("invalid key"))
	if strings.Contains(err.Error(), "KDBX 3.1") {
		t.Fatalf("обычная ошибка ошибочно помечена ARC4: %v", err)
	}
}
