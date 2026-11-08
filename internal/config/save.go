package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// shellUUID — временная вкладка локальной оболочки; перед сохранением удаляется.
const shellUUID = "__PAC_SHELL__"

// Save пишет конфиг атомарно (временный файл + rename), удаляя __PAC_SHELL__
// и шифруя секретные поля. Входящий Config не мутируется.
func (cfg *Config) Save(path string, c Cipher) error {
	if err := ValidateSaveCipher(c); err != nil {
		return err
	}
	out := cfg.cloneDropShell()
	encryptFields(out, c)

	data, err := yaml.Marshal(out)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".asbru-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// WithoutShell возвращает копию конфига без временной вкладки __PAC_SHELL__.
func (cfg *Config) WithoutShell() *Config { return cfg.cloneDropShell() }

func (cfg *Config) cloneDropShell() *Config {
	n := *cfg
	n.Raw = cloneAny(cfg.Raw)

	d := cfg.Defaults
	d.Raw = cloneAny(d.Raw)
	d.GlobalVariables = make(map[string]VarEntry, len(cfg.Defaults.GlobalVariables))
	for k, v := range cfg.Defaults.GlobalVariables {
		d.GlobalVariables[k] = v
	}
	n.Defaults = d

	// Клонирование environments с отбрасыванием вкладки оболочки.
	envs := make(map[string]Environment, len(cfg.Environments))
	for k, v := range cfg.Environments {
		if k == shellUUID {
			continue
		}
		v.Raw = cloneAny(v.Raw)
		if v.Children != nil {
			children := make(map[string]FlexInt, len(v.Children))
			for ck, cv := range v.Children {
				children[ck] = cv
			}
			v.Children = children
		}
		if v.TerminalOptions != nil {
			v.TerminalOptions = cloneAny(v.TerminalOptions)
		}
		v.Variables = cloneSlice(v.Variables)
		v.Screenshots = append([]any(nil), v.Screenshots...)
		v.LocalBefore = cloneSlice(v.LocalBefore)
		v.Expect = cloneSlice(v.Expect)
		v.LocalConnected = cloneSlice(v.LocalConnected)
		v.Macros = cloneSlice(v.Macros)
		v.LocalAfter = cloneSlice(v.LocalAfter)
		envs[k] = v
	}
	n.Environments = envs
	return &n
}

func cloneAny(m map[string]any) map[string]any {
	n := make(map[string]any, len(m))
	for k, v := range m {
		n[k] = v
	}
	return n
}

func cloneSlice(s []map[string]any) []map[string]any {
	if s == nil {
		return nil
	}
	n := make([]map[string]any, len(s))
	for i, m := range s {
		n[i] = cloneAny(m)
	}
	return n
}
