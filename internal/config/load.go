package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadConfig читает asbru.yml и дешифрует известные секретные поля.
// Ключом шифрования управляет c; передайте NewCBC() для совместимости с Perl.
func LoadConfig(path string, c Cipher) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	decryptFields(&cfg, c)
	return &cfg, nil
}

// truthy1 — истинность «1» как в Perl (легаси `'hide' eq '1'`): принимает и
// int 1, и строку "1" (YAML `hide: '1'`), и иные правдивые значения (COSMETIC-2).
func truthy1(v any) bool {
	switch n := v.(type) {
	case int:
		return n == 1
	case int64:
		return n == 1
	case float64:
		return n == 1
	case string:
		return n == "1" || (n != "" && n != "0")
	case bool:
		return n
	}
	return false
}

// transformSecrets — общая итерация секретных полей (дедуп близнецов
// decryptFields/encryptFields): к каждому секрету применяется transform.
// Decrypt ↔ encryptIfPlain — инверсии (encrypt толерантен к уже шифрованному).
func transformSecrets(cfg *Config, transform func(string) string) {
	if transform == nil {
		return
	}
	d := &cfg.Defaults
	d.GUIPassword = transform(d.GUIPassword)
	d.SudoPassword = transform(d.SudoPassword)
	d.Keepass.Password = transform(d.Keepass.Password)
	for name, v := range d.GlobalVariables {
		if v.Hidden == 1 {
			v.Value = transform(v.Value)
			d.GlobalVariables[name] = v
		}
	}
	for uuid, env := range cfg.Environments {
		env.Pass = transform(env.Pass)
		env.Passphrase = transform(env.Passphrase)
		if env.Variables != nil {
			for i, v := range env.Variables {
				if truthy1(v["hide"]) {
					if txt, ok := v["txt"].(string); ok {
						v["txt"] = transform(txt)
					}
				}
				env.Variables[i] = v
			}
		}
		for i, x := range env.Expect {
			if truthy1(x["hidden"]) {
				if txt, ok := x["send"].(string); ok {
					x["send"] = transform(txt)
				}
			}
			env.Expect[i] = x
		}
		cfg.Environments[uuid] = env
	}
}

// decryptFields расшифровывает поля, которые Perl шифровал в _cipherCFG.
func decryptFields(cfg *Config, c Cipher) {
	if c == nil {
		return
	}
	transformSecrets(cfg, c.Decrypt)
}

// encryptFields — инверсия decryptFields: секреты приводятся к шифртексту.
// Значения, выглядящие как уже зашифрованные, пропускаются (толерантность
// дешифровки в легаси могла оставить шифртекст в памяти).
func encryptFields(cfg *Config, c Cipher) {
	if c == nil {
		return
	}
	transformSecrets(cfg, func(s string) string { return encryptIfPlain(s, c) })
}

// ciphertextPrefix — hex(Salted__ || соль): любой шифртекст этого шифра начинается с него.
const ciphertextPrefix = "53616c7465645f5f4e61bc0000000000"

func encryptIfPlain(val string, c Cipher) string {
	if strings.HasPrefix(strings.ToLower(val), ciphertextPrefix) {
		return val
	}
	return c.Encrypt(val) // пустые строки легаси тоже шифрует (ревью #5)
}

// errNilCipherSave — защита от записи паролей в открытом виде без шифра.
func ValidateSaveCipher(c Cipher) error {
	if c == nil {
		return errors.New("config save requires a cipher to protect secrets")
	}
	return nil
}
