package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M23: локальные команды (легаси _wPrePostExec, PACTerminal.pm:3379-3400):
// отбираются только с non-empty command; ask → диалог с default-предвыбором,
// без ask → выполняются все.
func TestLocalSelected(t *testing.T) {
	env := &config.Environment{LocalBefore: []map[string]any{
		{"command": "true", "ask": 1, "default": 1},
		{"command": "false", "ask": 1, "default": 0},
		{"command": "echo hi"},
		{"command": ""},
	}}
	dialog, selected := localSelected(env.LocalBefore)
	if !dialog {
		t.Fatal("есть ask → нужен диалог")
	}
	if len(selected) != 1 || selected[0] != "true" {
		t.Fatalf("selected: %v", selected)
	}
	// легаси YAML::Tiny: ask: '1' строками (ревью M23 P2)
	env.LocalBefore = []map[string]any{
		{"command": "a", "ask": "0"}, {"command": "b"}, {},
	}
	dialog, selected = localSelected(env.LocalBefore)
	if dialog || len(selected) != 2 || selected[0] != "a" || selected[1] != "b" {
		t.Fatalf("no-ask: dialog=%v sel=%v", dialog, selected)
	}
}

// M23: побег команды через sh -c; пустота команд = no-op.
func TestRunLocalCommands(t *testing.T) {
	var ran []string
	old := runLocalDetached
	runLocalDetached = func(cmd string) { ran = append(ran, cmd) }
	defer func() { runLocalDetached = old }()
	runLocalCommands([]string{"a", "b"})
	if ran[0] != "a" || ran[1] != "b" {
		t.Fatalf("ran: %v", ran)
	}
}
