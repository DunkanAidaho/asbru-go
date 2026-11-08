package config

import "testing"

// COSMETIC-2: hide/hidden строкой "1" (легаси YAML: hide: '1') должно считаться
// истиной — иначе секрет не шифруется на save и утекает plaintext-ом в файл.
func TestTransformSecretsStringHide(t *testing.T) {
	c := NewCBC()
	plain := "my-secret"
	cfg := &Config{}
	cfg.Environments = map[string]Environment{"u1": {Variables: []map[string]any{{"hide": "1", "txt": plain}}}}
	encryptFields(cfg, c)
	if got := cfg.Environments["u1"].Variables[0]["txt"]; got == plain {
		t.Fatalf("hide: \"1\" (string) не зашифрован — plaintext утечёт в файл")
	}
	// и обратно: decrypt возвращает plaintext.
	decryptFields(cfg, c)
	if got := cfg.Environments["u1"].Variables[0]["txt"]; got != plain {
		t.Fatalf("decrypt: got %q want %q", got, plain)
	}
}
