package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// PACScripts GUI: listScripts — имена файлов (не каталоги), сортировка.
func TestListScripts(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"b.sh", "a.pl", "z.py"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// подкаталог — не скрипт.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	got := listScripts(dir)
	want := []string{"a.pl", "b.sh", "z.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listScripts: %v", got)
	}
	if listScripts(filepath.Join(dir, "nope")) != nil {
		t.Fatal("несуществующий каталог → nil")
	}
}
