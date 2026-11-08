package ui

// #1 (user order): группированная боковая панель Connections/Favourites/History/
// Clusters. Чистые геттеры данных для секций (без Qt): Favourites —
// favouriteEntries (M31); History — новейшие подключения из history.Store;
// Clusters — живые кластеры + участники из конфига. Виджеты — build-only.

import (
	"strconv"
	"time"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/history"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// historyEntry — запись панели History (uuid + имя, новейшие сверху вниз).
type historyEntry struct {
	UUID string
	Name string
	T    time.Time
}

// historyPanelEntries — новейшие подключения из store, разрешённые в имена по
// конфигу (fallback — uuid). store может быть nil.
func historyPanelEntries(store *history.Store, cfg *config.Config) []historyEntry {
	if store == nil {
		return nil
	}
	recent := store.Recent()
	if len(recent) == 0 {
		return nil
	}
	out := make([]historyEntry, 0, len(recent))
	for _, e := range recent {
		name := e.UUID
		if cfg != nil {
			if env, ok := cfg.Environments[e.UUID]; ok && env.Name != "" {
				name = env.Name
			}
		}
		out = append(out, historyEntry{UUID: e.UUID, Name: name, T: e.T})
	}
	return out
}

// clusterEntry — кластер (имя + участники-соединения + счётчик).
type clusterEntry struct {
	Name        string
	Connections []string
	Count       int
}

// clusterPanelEntries — живые кластеры (clusterNames, напр. из clusters.clusters())
// с участниками из конфига (env.Cluster). Пустые/неизвестные кластеры отбрасываются.
func clusterPanelEntries(cfg *config.Config, clusterNames []string) []clusterEntry {
	var out []clusterEntry
	for _, name := range clusterNames {
		if name == "" {
			continue
		}
		ce := clusterEntry{Name: name}
		if cfg != nil {
			for uuid, env := range cfg.Environments {
				if env.IsGroup != 1 && clusterHas(env.Cluster, name) {
					ce.Connections = append(ce.Connections, uuid)
				}
			}
		}
		ce.Count = len(ce.Connections)
		if ce.Count == 0 {
			continue // нет участников — не показываем (пустые/неизвестные)
		}
		out = append(out, ce)
	}
	return out
}

// panelItem — строка секции боковой панели (label + key для double-click).
type panelItem struct {
	Label string
	Key   string
}

// buildPanelList — QTreeWidget-список секции: header, элементы, двойной клик
// (double-click) → onDouble(key). Qt-нить.
func buildPanelList(header string, items []panelItem, onDouble func(key string)) *qt.QWidget {
	tree := qt.NewQTreeWidget2()
	tree.SetHeaderLabel(header)
	byKey := make(map[unsafe.Pointer]string, len(items))
	for _, it := range items {
		ti := qt.NewQTreeWidgetItem()
		ti.SetText(0, it.Label)
		byKey[itemID(ti)] = it.Key
		tree.AddTopLevelItem(ti)
	}
	tree.OnItemDoubleClicked(func(item *qt.QTreeWidgetItem, _ int) {
		if key, ok := byKey[itemID(item)]; ok {
			onDouble(key)
		}
	})
	return tree.QWidget
}

// favPanelItems — список избранного для панели (uuid → имя).
func favPanelItems(cfg *config.Config) []panelItem {
	if cfg == nil {
		return nil
	}
	var out []panelItem
	for _, f := range favouriteEntries(cfg.Environments) {
		out = append(out, panelItem{Label: f.Name, Key: f.UUID})
	}
	return out
}

// historyPanelItems — список последних подключений для панели (uuid → имя).
func historyPanelItems(store *history.Store, cfg *config.Config) []panelItem {
	var out []panelItem
	for _, e := range historyPanelEntries(store, cfg) {
		out = append(out, panelItem{Label: e.Name, Key: e.UUID})
	}
	return out
}

// clusterPanelItems — список живых кластеров для панели (имя → участники).
func clusterPanelItems(cfg *config.Config, live func() []string) []panelItem {
	if live == nil {
		return nil
	}
	var out []panelItem
	for _, ce := range clusterPanelEntries(cfg, live()) {
		label := ce.Name
		if ce.Count > 0 {
			label = ce.Name + " (" + strconv.Itoa(ce.Count) + ")"
		}
		out = append(out, panelItem{Label: label, Key: ce.Name})
	}
	return out
}

// panelListPage — страница-контейнер вкладки со списком; перечитывается при
// переключении вкладки (RemoveWidget+DeleteLater старого списка, AddWidget нового).
type panelListPage struct {
	container *qt.QWidget
	lay       *qt.QVBoxLayout
	cur       *qt.QWidget
}

func newPanelListPage(header string, items []panelItem, onDouble func(key string)) *panelListPage {
	p := &panelListPage{}
	p.container = qt.NewQWidget2()
	p.lay = qt.NewQVBoxLayout2()
	p.container.SetLayout(p.lay.QLayout)
	p.fill(header, items, onDouble)
	return p
}

func (p *panelListPage) fill(header string, items []panelItem, onDouble func(key string)) {
	if p.cur != nil {
		p.lay.RemoveWidget(p.cur)
		p.cur.DeleteLater()
		p.cur = nil
	}
	p.cur = buildPanelList(header, items, onDouble)
	p.lay.AddWidget(p.cur)
}

// buildSidePanel — группированная боковая панель (#1): QTabWidget с секциями
// Connections (дерево+кнопки) / Favourites / History / Clusters. История и
// кластеры перечитываются при переключении вкладки (OnCurrentChanged).
// launch(uuid) — запуск соединения; launchCluster(name) — запуск всех в кластере.
func buildSidePanel(cfg *config.Config, tw *qt.QTreeWidget, buttons *qt.QWidget,
	historyStore *history.Store, liveCluster func() []string,
	launch func(uuid string), launchCluster func(name string)) *qt.QWidget {

	left := qt.NewQWidget2()
	box := qt.NewQVBoxLayout2()
	tabs := qt.NewQTabWidget2()

	// Connections: дерево + панель кнопок (tree-тулбар).
	connBox := qt.NewQVBoxLayout2()
	connBox.AddWidget(tw.QWidget)
	connBox.AddWidget(buttons)
	connV := qt.NewQWidget2()
	connV.SetLayout(connBox.QLayout)
	tabs.AddTab(connV, i18n.Tr("Panel", "Подключения"))

	// Favourites / History / Clusters — list-секции; перечитываем при показе.
	fav := newPanelListPage(i18n.Tr("Panel", "Избранные"), favPanelItems(cfg), launch)
	hist := newPanelListPage(i18n.Tr("Panel", "История"), historyPanelItems(historyStore, cfg), launch)
	clu := newPanelListPage(i18n.Tr("Panel", "Кластеры"), clusterPanelItems(cfg, liveCluster), func(name string) {
		launchCluster(name)
	})
	tabs.AddTab(fav.container, i18n.Tr("Panel", "Избранные"))
	tabs.AddTab(hist.container, i18n.Tr("Panel", "История"))
	tabs.AddTab(clu.container, i18n.Tr("Panel", "Кластеры"))
	tabs.OnCurrentChanged(func(index int) {
		switch index {
		case 1:
			fav.fill(i18n.Tr("Panel", "Избранные"), favPanelItems(cfg), launch)
		case 2:
			hist.fill(i18n.Tr("Panel", "История"), historyPanelItems(historyStore, cfg), launch)
		case 3:
			clu.fill(i18n.Tr("Panel", "Кластеры"), clusterPanelItems(cfg, liveCluster), func(name string) {
				launchCluster(name)
			})
		}
	})

	box.AddWidget(tabs.QWidget)
	left.SetLayout(box.QLayout)
	return left
}
