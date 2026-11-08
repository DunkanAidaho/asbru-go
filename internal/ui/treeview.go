package ui

import (
	"strings"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/tree"
	qt "github.com/mappu/miqt/qt6"
)

// itemID — стабильный ключ QTreeWidgetItem (miqt пересоздаёт Go-обёртку при
// каждом обращении, поэтому ключ — C-хэндл UnsafePointer, а не указатель на
// обёртку; иначе byItem[item] всегда мимо — дебаг «двойной клик/меню/кнопки»).
func itemID(item *qt.QTreeWidgetItem) unsafe.Pointer { return item.UnsafePointer() }

// captureExpanded — uuid раскрытых элементов дерева (для сохранения состояния
// при reload; иначе fillTree перестраивает QTreeWidget с нуля и всё схлопывается
// — очный дебаг «сохранил соединение — дерево свернулось»).
func captureExpanded(tw *qt.QTreeWidget, byItem map[unsafe.Pointer]string) map[string]bool {
	out := map[string]bool{}
	var walk func(*qt.QTreeWidgetItem)
	walk = func(it *qt.QTreeWidgetItem) {
		if it == nil {
			return
		}
		if it.IsExpanded() {
			if u, ok := byItem[itemID(it)]; ok {
				out[u] = true
			}
		}
		for i := 0; i < it.ChildCount(); i++ {
			walk(it.Child(i))
		}
	}
	for i := 0; i < tw.TopLevelItemCount(); i++ {
		walk(tw.TopLevelItem(i))
	}
	return out
}

// restoreExpanded — раскрыть узлы по сохранённому набору uuid.
func restoreExpanded(tw *qt.QTreeWidget, byItem map[unsafe.Pointer]string, expanded map[string]bool) {
	var walk func(*qt.QTreeWidgetItem)
	walk = func(it *qt.QTreeWidgetItem) {
		if it == nil {
			return
		}
		if u, ok := byItem[itemID(it)]; ok && expanded[u] {
			it.SetExpanded(true)
		}
		for i := 0; i < it.ChildCount(); i++ {
			walk(it.Child(i))
		}
	}
	for i := 0; i < tw.TopLevelItemCount(); i++ {
		walk(tw.TopLevelItem(i))
	}
}

// iconNameForMethod — тема-иконка для метода подключения (бэклог «иконки по типу»).
// Возвращает имя иконки темы; группы используют "folder".
func iconNameForMethod(method string) string {
	m := strings.ToLower(method)
	switch {
	case strings.Contains(m, "sftp"), strings.Contains(m, "webdav"), strings.Contains(m, "ftp"):
		return "folder-remote"
	case strings.Contains(m, "rdp"), strings.Contains(m, "vnc"):
		return "preferences-desktop-remote"
	case strings.Contains(m, "telnet"), strings.Contains(m, "serial"), m == "cu":
		return "network-server"
	default: // ssh, mosh, 3270, generic, local, прочие → терминал
		return "utilities-terminal"
	}
}

// rebuildNodes — построение модели после изменений конфигурации.
func rebuildNodes(cfg *config.Config) map[string]*tree.Node {
	nodes, _ := tree.Build(cfg.Environments)
	return nodes
}

// fillTree переносит внутреннее дерево (internal/tree) в QTreeWidget.
// Возвращает карту item → uuid (для двойного клика и редактора).
func fillTree(w *qt.QTreeWidget, nodes map[string]*tree.Node) map[unsafe.Pointer]string {
	byItem := make(map[unsafe.Pointer]string)

	makeItem := func(n *tree.Node) *qt.QTreeWidgetItem {
		it := qt.NewQTreeWidgetItem()
		it.SetText(0, treeLabel(n.Env, n.IsGroup))
		// бэклог «иконки по типу»: иконка по методу (группа — папка).
		if n.IsGroup {
			it.SetIcon(0, qt.QIcon_FromTheme("folder"))
		} else if n.Env != nil {
			it.SetIcon(0, qt.QIcon_FromTheme(iconNameForMethod(n.Env.Method)))
		}
		return it
	}

	// корень: его дети — верхние элементы
	root, ok := nodes[config.RootUUID]
	if !ok {
		return byItem
	}
	visited := map[string]bool{} // H2-ревью: порченная карта не роняет перестройку
	var add func(parent *qt.QTreeWidgetItem, uuid string)
	add = func(parent *qt.QTreeWidgetItem, uuid string) {
		if visited[uuid] {
			return
		}
		visited[uuid] = true
		n := nodes[uuid]
		if n == nil {
			return
		}
		it := makeItem(n)
		byItem[itemID(it)] = uuid
		if parent == nil {
			w.AddTopLevelItem(it)
		} else {
			parent.AddChild(it)
		}
		for _, child := range n.Children {
			add(it, child)
		}
	}
	for _, child := range root.Children {
		add(nil, child)
	}
	return byItem
}

// orderFromTree — порядок элементов дерева (top-level + иерархия).
// КОРЕНЬ виртуален (нет item'а) — первой обязана идти root-строка с
// top-level uuid, иначе reconcile осиротит весь верхний уровень (M15-
// ревью H1: пустое дерево + порча на диск после первого дропа).
func orderFromTree(tw *qt.QTreeWidget, byItem map[unsafe.Pointer]string) []reconcileNode {
	root := reconcileNode{UUID: config.RootUUID}
	order := []reconcileNode{root}
	seen := map[unsafe.Pointer]bool{} // M15-ре-ревью: цикл не роняет walk
	var walk func(item *qt.QTreeWidgetItem) reconcileNode
	walk = func(item *qt.QTreeWidgetItem) reconcileNode {
		id := itemID(item)
		if seen[id] {
			return reconcileNode{}
		}
		seen[id] = true
		uuid, ok := byItem[id]
		if !ok {
			return reconcileNode{} // неизвестный/внутренний элемент
		}
		n := reconcileNode{UUID: uuid}
		for i := 0; i < item.ChildCount(); i++ {
			c := walk(item.Child(i))
			if c.UUID != "" {
				n.Children = append(n.Children, c.UUID)
				order = append(order, c)
			}
		}
		return n
	}
	for i := 0; i < tw.TopLevelItemCount(); i++ {
		n := walk(tw.TopLevelItem(i))
		if n.UUID != "" {
			order[0].Children = append(order[0].Children, n.UUID)
			order = append(order, n)
		}
	}
	return order
}
