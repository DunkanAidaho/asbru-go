// Пакет tree: модель дерева соединений (группы + соединения по UUID).
package tree

import (
	"sort"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// Node — узел дерева (группа или соединение).
type Node struct {
	UUID     string
	Parent   string
	Name     string
	IsGroup  bool
	Env      *config.Environment
	Children []string // группы первыми + по имени (lc cmp), как SortedTreeStore легаси
}

// Build строит дерево из environments; возвращает карту uuid→узел.
// Источник иерархии — легаси-карта children{} у групп (Parent не используется:
// в старых конфигах children задают порядок/состав, а Parent мог отсутствовать).
// Ссылки на отсутствующие uuid пропускаются.
func Build(envs map[string]config.Environment) (map[string]*Node, error) {
	nodes := make(map[string]*Node, len(envs))
	for uuid, e := range envs {
		nodes[uuid] = &Node{
			UUID:    uuid,
			Parent:  e.Parent,
			Name:    e.Name,
			IsGroup: e.IsGroup == 1,
			Env:     &e,
		}
	}
	for _, n := range nodes {
		if n.Env == nil || n.Env.Children == nil {
			continue
		}
		for child := range n.Env.Children {
			if _, ok := nodes[child]; ok {
				n.Children = append(n.Children, child)
			}
		}
		sort.Slice(n.Children, func(i, j int) bool {
			a, b := nodes[n.Children[i]], nodes[n.Children[j]]
			if a.IsGroup != b.IsGroup {
				return a.IsGroup // группы первыми (легаси 'sort groups first' // 1)
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name) // lc cmp
		})
	}
	return nodes, nil
}

// Walk обходит дерево глубиной-в-глубину от uuid (узел вызывается до детей).
// Циклы в children{} не приводят к бесконечной рекурсии (visited-множество).
func Walk(nodes map[string]*Node, uuid string, fn func(*Node) error) error {
	visited := make(map[string]bool)
	var walk func(string) error
	walk = func(u string) error {
		if visited[u] {
			return nil
		}
		visited[u] = true
		n, ok := nodes[u]
		if !ok {
			return nil
		}
		if err := fn(n); err != nil {
			return err
		}
		for _, child := range n.Children {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(uuid)
}
