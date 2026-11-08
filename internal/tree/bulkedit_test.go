package tree

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// mkenv — вспомогатель: окружение с минимальными полями в карте.
func mkenv(name string, group bool, children ...string) config.Environment {
	e := config.Environment{Name: name}
	if group {
		e.IsGroup = 1
		if len(children) > 0 {
			e.Children = map[string]config.FlexInt{}
			for _, c := range children {
				e.Children[c] = 1
			}
		}
	}
	return e
}

// TestExpandSelectionGroups — раскрытие групп в выбранные uuid.
// Recurse=false: только 1-й уровень детей; Recurse=true: все уровни.
func TestExpandSelectionGroups(t *testing.T) {
	envs := map[string]config.Environment{
		"g":  mkenv("g", true, "c1", "g2"),
		"g2": mkenv("g2", true, "c2"),
		"c1": mkenv("c1", false),
		"c2": mkenv("c2", false),
		"root": {
			Name:     "root",
			IsGroup:  1,
			Children: map[string]config.FlexInt{"g": 1},
		},
	}
	// 1-й уровень: g → c1, g2 (не раскрываем g2).
	got := ExpandSelection(envs, []string{"g"}, false)
	want := map[string]bool{"g": true, "c1": true, "g2": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandSelection(1-й ур.) = %v, want %v", got, want)
	}
	// все уровни: g → c1, g2, c2.
	got = ExpandSelection(envs, []string{"g"}, true)
	want = map[string]bool{"g": true, "c1": true, "g2": true, "c2": true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExpandSelection(все ур.) = %v, want %v", got, want)
	}
}

// TestApplyBulkEditsLiteral — литеральная замена полей по выбранным uuid.
func TestApplyBulkEditsLiteral(t *testing.T) {
	envs := map[string]config.Environment{
		"a": {Name: "a", IP: "1.1.1.1", User: "u"},
		"b": {Name: "b", IP: "2.2.2.2", User: "u2"},
		"c": {Name: "c", IP: "3.3.3.3"},
	}
	edits := []FieldEdit{
		{Field: "ip", Value: "10.0.0.1"}, // литерал
	}
	n := ApplyBulkEdits(envs, []string{"a", "b"}, edits, false)
	if n != 2 {
		t.Fatalf("ApplyBulkEdits: %d изменений, want 2", n)
	}
	if envs["a"].IP != "10.0.0.1" || envs["b"].IP != "10.0.0.1" {
		t.Fatalf("литеральная замена IP применилась неверно: %+v %+v", envs["a"], envs["b"])
	}
	if envs["c"].IP != "3.3.3.3" {
		t.Fatalf("невыбранный env изменён: %+v", envs["c"])
	}
}

// TestApplyBulkEditsRegex — regex-подстановка s/match/new/.
func TestApplyBulkEditsRegex(t *testing.T) {
	envs := map[string]config.Environment{
		"a": {Name: "host-01", User: "userA"},
		"b": {Name: "host-02", User: "userB"},
	}
	edits := []FieldEdit{
		{Field: "name", Value: "svr-", IsRegex: true, Match: "host", Replace: "svr"},
	}
	if n := ApplyBulkEdits(envs, []string{"a", "b"}, edits, false); n != 2 {
		t.Fatalf("regex-редактирование: %d, want 2", n)
	}
	if envs["a"].Name != "svr-01" || envs["b"].Name != "svr-02" {
		t.Fatalf("regex-замена неверна: %+v %+v", envs["a"], envs["b"])
	}
}

// TestApplyBulkEditsSkipGroups — по умолчанию группы НЕ меняются (только
// non-group листья), если не включён ApplyToGroups.
func TestApplyBulkEditsSkipGroups(t *testing.T) {
	envs := map[string]config.Environment{
		"g":  {Name: "g", IsGroup: 1, IP: "1.1.1.1"},
		"c1": {Name: "c1", IP: "2.2.2.2"},
	}
	edits := []FieldEdit{{Field: "ip", Value: "10.0.0.1"}}
	if n := ApplyBulkEdits(envs, []string{"g", "c1"}, edits, false); n != 1 {
		t.Fatalf("ApplyBulkEdits: %d, want 1 (группа пропущена)", n)
	}
	if envs["g"].IP != "1.1.1.1" {
		t.Fatalf("группа изменена: %+v", envs["g"])
	}
	if envs["c1"].IP != "10.0.0.1" {
		t.Fatalf("лист не изменён: %+v", envs["c1"])
	}
}

// TestApplyBulkEditsGroupExpansion — выбор группы применяет правку к её детям.
func TestApplyBulkEditsGroupExpansion(t *testing.T) {
	envs := map[string]config.Environment{
		"g":  mkenv("g", true, "c1", "c2"),
		"c1": {Name: "c1", User: "u1"},
		"c2": {Name: "c2", User: "u2"},
	}
	edits := []FieldEdit{{Field: "user", Value: "admin"}}
	// recurse=true → группа раскрывается в детей.
	if n := ApplyBulkEdits(envs, []string{"g"}, edits, true); n != 2 {
		t.Fatalf("ApplyBulkEdits(группа) = %d, want 2", n)
	}
	if envs["c1"].User != "admin" || envs["c2"].User != "admin" {
		t.Fatalf("дети группы не изменены: %+v %+v", envs["c1"], envs["c2"])
	}
	// сама группа НЕ изменяется (правка только листьев).
	if envs["g"].IsGroup != 1 {
		t.Fatalf("группа-родитель изменена: %+v", envs["g"])
	}
}

// TestApplyBulkEditsRegexNoop — regex, воспроизводящий исходное значение, не
// считается изменением (иначе лишний save()).
func TestApplyBulkEditsRegexNoop(t *testing.T) {
	envs := map[string]config.Environment{
		"a": {Name: "host-01"},
	}
	// match "host" → replace "host" — результат идентичен исходному.
	edits := []FieldEdit{{Field: "name", IsRegex: true, Match: "host", Replace: "host"}}
	if n := ApplyBulkEdits(envs, []string{"a"}, edits, false); n != 0 {
		t.Fatalf("regex-noop: %d изменений, want 0", n)
	}
	if envs["a"].Name != "host-01" {
		t.Fatalf("regex-noop изменил значение: %+v", envs["a"])
	}
}

// TestApplyBulkEditsBadRegex — некорректный regex не применяется (не падает).
func TestApplyBulkEditsBadRegex(t *testing.T) {
	envs := map[string]config.Environment{
		"a": {Name: "host-01"},
	}
	edits := []FieldEdit{{Field: "name", IsRegex: true, Match: "(", Replace: "x"}} // "(" — битый regex
	if n := ApplyBulkEdits(envs, []string{"a"}, edits, false); n != 0 {
		t.Fatalf("битый regex: %d изменений, want 0", n)
	}
	if envs["a"].Name != "host-01" {
		t.Fatalf("битый regex изменил значение: %+v", envs["a"])
	}
}
