package ui

import (
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	qt "github.com/mappu/miqt/qt6"
)

// showPCC — окно Power Cluster Controller (легаси PACPCC): ввод в поле
// уходит во ВСЕ живые терминалы выбранного кластера. Реестр терминалов —
// clusterRegistry (ядро в cluster.go, под мьютексом).
func showPCC(parent *qt.QWidget) {
	d := qt.NewQDialog2() // miqt: NewQDialog2 без parent; модальность через Exec
	d.SetWindowTitle(i18n.Tr("Pcc", "Power Cluster Controller"))
	d.SetMinimumWidth(420)
	form := qt.NewQFormLayout2()

	combo := qt.NewQComboBox2()
	refresh := func() {
		combo.Clear()
		for _, name := range clusters.clusters() {
			combo.AddItem(name)
		}
	}
	refresh()
	form.AddRow3(i18n.Tr("Pcc", "Кластер"), combo.QWidget)

	entry := qt.NewQLineEdit2()
	entry.SetPlaceholderText(i18n.Tr("Pcc", "Команда для всех терминалов кластера"))
	form.AddRow3(i18n.Tr("Pcc", "Ввод"), entry.QWidget)

	send := func() {
		clu := combo.CurrentText()
		if clu == "" || entry.Text() == "" {
			return
		}
		dropped := clusters.send(clu, []byte(entry.Text()))
		if clusters.count(clu) == 0 {
			// ре-вью M20: пустых кластеров не существует (add при name!="",
			// remove очищает), но конфигурация живых терминалов могла
			// измениться с момента открытия (все закрылись) — ввод в никуда
			// не похороним молча: не очищаем поле.
			entry.SelectAll()
			return
		}
		if dropped > 0 {
			entry.SelectAll() // переполненный буфер — поправить и повторить
			return
		}
		entry.Clear()
	}
	entry.OnReturnPressed(send)
	ok := qt.NewQPushButton3(i18n.Tr("Pcc", "Отправить"))
	ok.OnClicked(send)
	cancel := qt.NewQPushButton3(i18n.Tr("Pcc", "Закрыть"))
	cancel.OnClicked(d.Reject)
	buttons := qt.NewQHBoxLayout2()
	buttons.AddStretch()
	buttons.AddWidget(cancel.QWidget)
	buttons.AddWidget(ok.QWidget)

	refreshBtn := qt.NewQPushButton3(i18n.Tr("Pcc", "Обновить список"))
	refreshBtn.OnClicked(refresh)

	row := qt.NewQHBoxLayout2()
	row.AddWidget(refreshBtn.QWidget)
	row.AddStretch()

	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	box.AddLayout(row.QLayout)
	box.AddLayout(buttons.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
}
