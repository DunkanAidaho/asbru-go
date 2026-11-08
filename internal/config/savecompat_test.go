package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Легаси шифрует и пустые строки; save должен писать шифртекст, не `”` (ревью #5).
func TestSaveEncryptsEmptyStrings(t *testing.T) {
	cfg := &Config{Environments: map[string]Environment{
		"u1": {Name: "c", Method: "SSH"},
	}}
	out := filepath.Join(t.TempDir(), "asbru.yml")
	if err := cfg.Save(out, NewCBC()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	for _, key := range []string{"pass", "passphrase"} {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, key+":") {
				if !strings.HasPrefix(strings.TrimSpace(line[len(key)+1:]), ciphertextPrefix) {
					t.Fatalf("%s сохранён не зашифрованным: %q", key, line)
				}
			}
		}
	}
}
