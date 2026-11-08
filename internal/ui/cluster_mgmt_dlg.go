package ui

// M32: PACCluster — Qt-диалог управления членством в кластерах (тонкая плёнка
// над чистыми функциями addClusterMember/removeClusterMember/allClusters).

import (
	"log/slog"
	"sort"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// showClusterDialog — диалог: кластер, его члены, добавить/убрать соединение.
// M35: изменения членства СОХРАНЯЮТСЯ (иначе пропадали на рестарте).
func showClusterDialog(cfg *config.Config, cfgPath string) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Cluster", "Кластеры"))
	d.SetMinimumWidth(420)

	cluster := qt.NewQComboBox2()
	connPick := qt.NewQComboBox2()
	members := qt.NewQTreeWidget2()
	members.SetHeaderLabel(i18n.Tr("Cluster", "Соединения кластера"))
	byItem := make(map[unsafe.Pointer]string)

	// соединения (не группы) с именами — для выбора добавления.
	var conns []struct{ uuid, name string }
	for uuid, e := range cfg.Environments {
		if e.IsGroup != 1 && e.Name != "" {
			conns = append(conns, struct{ uuid, name string }{uuid, e.Name})
		}
	}
	sort.Slice(conns, func(i, j int) bool { return conns[i].name < conns[j].name })

	refreshConns := func() {
		connPick.Clear()
		for _, c := range conns {
			connPick.AddItem(c.name)
		}
	}
	refreshClusters := func() {
		cluster.Clear()
		for _, c := range allClusters(cfg) {
			cluster.AddItem(c)
		}
	}
	refreshMembers := func() {
		members.Clear()
		byItem = map[unsafe.Pointer]string{}
		name := cluster.CurrentText()
		for _, uuid := range clusterIDs(cfg, name) {
			it := qt.NewQTreeWidgetItem()
			it.SetText(0, cfg.Environments[uuid].Name)
			byItem[itemID(it)] = uuid
			members.AddTopLevelItem(it)
		}
	}
	refreshClusters()
	refreshConns()
	refreshMembers()

	cluster.OnCurrentIndexChanged(func(_ int) { refreshMembers() })

	add := qt.NewQPushButton3(i18n.Tr("Cluster", "Добавить соединение"))
	add.OnClicked(func() {
		name := cluster.CurrentText()
		if name == "" {
			return
		}
		idx := connPick.CurrentIndex()
		if idx < 0 || idx >= len(conns) {
			return
		}
		addClusterMember(cfg, conns[idx].uuid, name)
		if err := saveCfgUI(cfg, cfgPath, d.QWidget); err != nil {
			slog.Error("сохранение кластера", "err", err)
		}
		refreshMembers()
	})
	remove := qt.NewQPushButton3(i18n.Tr("Cluster", "Убрать выделенное"))
	remove.OnClicked(func() {
		name := cluster.CurrentText()
		item := members.CurrentItem()
		uuid, ok := byItem[itemID(item)]
		if !ok {
			return
		}
		removeClusterMember(cfg, uuid, name)
		if err := saveCfgUI(cfg, cfgPath, d.QWidget); err != nil {
			slog.Error("сохранение кластера", "err", err)
		}
		refreshMembers()
	})
	close := qt.NewQPushButton3(i18n.Tr("Cluster", "Закрыть"))
	close.OnClicked(d.Reject)

	rows := qt.NewQFormLayout2()
	rows.AddRow3(i18n.Tr("Cluster", "Кластер"), cluster.QWidget)
	rows.AddRow3(i18n.Tr("Cluster", "Добавить"), connPick.QWidget)

	btnRow := qt.NewQHBoxLayout2()
	btnRow.AddWidget(add.QWidget)
	btnRow.AddWidget(remove.QWidget)
	btnRow.AddStretch()

	closer := qt.NewQHBoxLayout2()
	closer.AddStretch()
	closer.AddWidget(close.QWidget)

	box := qt.NewQVBoxLayout2()
	box.AddLayout(rows.QLayout)
	box.AddWidget(members.QWidget)
	box.AddLayout(btnRow.QLayout)
	box.AddLayout(closer.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
}
