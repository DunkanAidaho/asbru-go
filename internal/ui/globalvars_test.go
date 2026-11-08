package ui

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M29: globalVarRows — сортировка по имени; applyGlobalVars — rebuild map.
func TestGlobalVarsRoundTrip(t *testing.T) {
	cfg := &config.Config{Defaults: config.Defaults{GlobalVariables: map[string]config.VarEntry{
		"b": {Type: "string", Value: "2", Hidden: 0},
		"a": {Type: "string", Value: "1", Hidden: 1},
	}}}
	rows := globalVarRows(cfg)
	if len(rows) != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Name != "a" || rows[0].Hidden != true {
		t.Fatalf("сорт/Hidden: %+v", rows[0])
	}
	if rows[1].Name != "b" || rows[1].Value != "2" {
		t.Fatalf("сорт: %+v", rows[1])
	}
	// rebuild: изменение + пустая строка отбрасывается.
	rows[0].Value = "changed"
	out := &config.Config{}
	applyGlobalVars(out, append(rows, globalVarRow{Name: "", Type: "x", Value: "y"}))
	want := map[string]config.VarEntry{
		"a": {Type: "string", Value: "changed", Hidden: 1},
		"b": {Type: "string", Value: "2", Hidden: 0},
	}
	if !reflect.DeepEqual(out.Defaults.GlobalVariables, want) {
		t.Fatalf("apply: %#v", out.Defaults.GlobalVariables)
	}
	if globalVarRows(nil) != nil {
		t.Fatal("nil cfg → nil")
	}
}
