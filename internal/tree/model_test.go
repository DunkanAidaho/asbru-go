package tree

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func envs() map[string]config.Environment {
	return map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"aaa": 1, "zzz": 1}},
		"aaa":  {Name: "b-connection", Method: "SSH", Parent: "root"},
		"zzz":  {Name: "a-group", IsGroup: 1, Parent: "root", Children: map[string]config.FlexInt{"ccc": 1}},
		"ccc":  {Name: "bee", Method: "SSH", Parent: "zzz"},
	}
}

// Коллекция детей отсортирована по имени (как SortedTreeStore легаси).
func TestBuildSortedChildren(t *testing.T) {
	nodes, err := Build(envs())
	if err != nil {
		t.Fatal(err)
	}
	root := nodes["root"]
	if !reflect.DeepEqual(root.Children, []string{"zzz", "aaa"}) { // a-group < b-connection
		t.Fatalf("root children = %v", root.Children)
	}
	grp := nodes["zzz"]
	if !reflect.DeepEqual(grp.Children, []string{"ccc"}) {
		t.Fatalf("group children = %v", grp.Children)
	}
}

// Dfs — плоский обход в порядке отображения, начиная с детей корня.
func TestDfsOrder(t *testing.T) {
	nodes, err := Build(envs())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	_ = Walk(nodes, "root", func(n *Node) error {
		if n.UUID != "root" {
			out = append(out, n.Name)
		}
		return nil
	})
	want := []string{"a-group", "bee", "b-connection"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("DFS = %v, want %v", out, want)
	}
}

// Ссылки на отсутствующие дети игнорируются; сироты (недоступные из корня) не ломают обход.
func TestBuildIgnoresDangling(t *testing.T) {
	broken := map[string]config.Environment{
		"root":   {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"ghost": 1}},
		"orphan": {Name: "orphan", Method: "SSH", Parent: "missing"},
	}
	nodes, err := Build(broken)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes["root"].Children) != 0 {
		t.Fatalf("dangling child must be skipped: %v", nodes["root"].Children)
	}
	// сирота (Parent без children{}-ссылки) не достижима из корня
	reached := false
	_ = Walk(nodes, "root", func(n *Node) error {
		if n.UUID == "orphan" {
			reached = true
		}
		return nil
	})
	if reached {
		t.Fatal("orphan должен быть недостижим (источник иерархии — children{})")
	}
}

// Walk терпит циклическую children{}-карту и не зацикливается (ревью-находка M1).
func TestWalkCycleGuard(t *testing.T) {
	cyclic := map[string]config.Environment{
		"a": {Name: "A", IsGroup: 1, Children: map[string]config.FlexInt{"b": 1}},
		"b": {Name: "B", IsGroup: 1, Children: map[string]config.FlexInt{"a": 1}},
	}
	nodes, err := Build(cyclic)
	if err != nil {
		t.Fatal(err)
	}
	visited := 0
	if err := Walk(nodes, "a", func(n *Node) error {
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited > 2 {
		t.Fatalf("Walk зациклился: %d узлов", visited)
	}
}

// M35: сортировка — группы ПЕРВЫМИ (легаси 'sort groups first' // 1) и
// case-insensitive (lc(name) cmp lc(name)) — SortedTreeStore.pm:162,193.
func TestBuildGroupsFirstAndCaseInsensitive(t *testing.T) {
	envs := map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"conn": 1, "grp": 1}},
		"grp":  {Name: "z-group", IsGroup: 1, Parent: "root"},
		"conn": {Name: "alpha", Method: "SSH", Parent: "root"},
	}
	nodes, err := Build(envs)
	if err != nil {
		t.Fatal(err)
	}
	// group-first: grp ("z-group") перед conn ("alpha"), хотя "alpha" < "z-group".
	if want := []string{"grp", "conn"}; !reflect.DeepEqual(nodes["root"].Children, want) {
		t.Fatalf("root children = %v, want %v", nodes["root"].Children, want)
	}
}

func TestBuildCaseInsensitive(t *testing.T) {
	envs := map[string]config.Environment{
		"root": {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"a": 1, "b": 1}},
		"a":    {Name: "alpha", Method: "SSH", Parent: "root"},
		"b":    {Name: "Beta", Method: "SSH", Parent: "root"},
	}
	nodes, err := Build(envs)
	if err != nil {
		t.Fatal(err)
	}
	// case-insensitive: "alpha" < "Beta" → a перед b.
	if want := []string{"a", "b"}; !reflect.DeepEqual(nodes["root"].Children, want) {
		t.Fatalf("root children = %v, want %v", nodes["root"].Children, want)
	}
}
