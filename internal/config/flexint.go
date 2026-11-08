package config

import (
	"strconv"

	"gopkg.in/yaml.v3"
)

// FlexInt — целое с толерантным YAML-разбором: легаси (Perl/YAML::Tiny)
// сохраняет «числа» строками ("1") и булями (true), и реальные конфиги
// содержат и то и другое; жёсткий int ломал бы загрузку (ревью major #1).
type FlexInt int

// UnmarshalYAML принимает !!int, !!str и !!bool.
func (f *FlexInt) UnmarshalYAML(node *yaml.Node) error {
	var v int
	switch node.Tag {
	case "!!int":
		// YAML-целые: 0x30/0o17/0b101/1_000 и легаси-октал 0022. node.Decode
		// применяет резолвер yaml.v3 (ParseInt base 0), а Atoi их отвергал.
		var n int
		if err := node.Decode(&n); err != nil {
			return err
		}
		v = n
	case "!!float":
		// COSMETIC-4: timeout connect: 1.5 — не игнорировать (раньше → 0); truncate.
		if n, err := strconv.ParseFloat(node.Value, 64); err == nil {
			v = int(n)
		}
	case "!!bool":
		if node.Value == "true" {
			v = 1
		}
	case "!!str":
		if node.Value == "" {
			v = 0
		} else if n, err := strconv.Atoi(node.Value); err == nil {
			v = n
		}
		// строки "true"/"false" → 0: легаси сравнивает eq '1', истинно только "1"
		// (ревью: unquoted `true` приходит как !!bool и выше даёт 1)
	}
	*f = FlexInt(v)
	return nil
}

// MarshalYAML сохраняет число как целое.
func (f FlexInt) MarshalYAML() (any, error) { return int(f), nil }
