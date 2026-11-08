package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M36-ревью MAJOR: групповое редактирование / вырезка не должны трогать
// защищённые узлы (себя или потомков в выборке).
func TestSelHasProtected(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g": 1}},
		"g":    {Name: "g", IsGroup: 1, Parent: "root", Children: map[string]config.FlexInt{"l": 1}},
		"l":    {Name: "l", Method: "SSH", Parent: "g"},
		"p":    {Name: "p", Method: "SSH", Parent: "root", Protected: 1},
	}}
	// выборка без защищённых — false.
	if selHasProtected(cfg, []string{"l"}, true) {
		t.Fatal("l не защищён, а selHasProtected=true")
	}
	// выборка защищённого узла — true.
	if !selHasProtected(cfg, []string{"p"}, true) {
		t.Fatal("p защищён, а selHasProtected=false")
	}
	// раскрытие группы (recurse) — потомок защищён? нет.
	if selHasProtected(cfg, []string{"g"}, true) {
		t.Fatal("g без защищённых потомков, а selHasProtected=true")
	}
	// recurse до уровня защищённого потомка.
	cfg.Environments["l"] = config.Environment{Name: "l", Method: "SSH", Parent: "g", Protected: 1}
	if !selHasProtected(cfg, []string{"g"}, true) {
		t.Fatal("у g защищённый потомок, а recurse-проверка пропустила")
	}
}
