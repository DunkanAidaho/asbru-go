package ui

// #4: UI-диалог выбора KeePassXC-записи (список метаданных через List(), без
// паролей) с монтированием KPX uuid в редактор; и поле 'vnc security' в
// редакторе (VeNCrypt: auto/vencrypt/none). Чистые геттеры — TDD; Qt-диалог —
// build-only.

import (
	"sort"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/secretservice"
	qt "github.com/mappu/miqt/qt6"
)

// keepassPickerItem — строка выбора KeePass-записи (label + uuid).
type keepassPickerItem struct {
	Label string
	UUID  string
}

// keepassPickerItems — записи для диалога: "Title (username)" → uuid, сортировка
// по label (детерминированно). Записи без uuid отбрасываются (не для ссылки).
func keepassPickerItems(entries []secretservice.Entry) []keepassPickerItem {
	var out []keepassPickerItem
	for _, e := range entries {
		if e.UUID == "" {
			continue
		}
		label := e.Title
		if e.Username != "" {
			if label == "" {
				label = e.Username
			} else {
				label = e.Title + " (" + e.Username + ")"
			}
		}
		if label == "" {
			label = e.UUID
		}
		out = append(out, keepassPickerItem{Label: label, UUID: e.UUID})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].UUID < out[j].UUID
	})
	return out
}

// vncSecurityValues — допустимые значения 'vnc security' (VeNCrypt).
func vncSecurityValues() []string { return []string{"auto", "vencrypt", "none"} }

// validVNCEncryption — одно из auto/vencrypt/none.
func validVNCEncryption(s string) bool {
	switch s {
	case "auto", "vencrypt", "none":
		return true
	}
	return false
}

// pickKeePassUUID — диалог выбора KeePassXC-записи (список List(), без паролей).
// Возвращает выбранный uuid и true; иначе ("", false). Qt-нить.
func pickKeePassUUID(parent *qt.QWidget) (string, bool) {
	entries, err := secretServiceList()
	if err != nil || len(entries) == 0 {
		showErr(parent, i18n.Tr("KeePass", "KeePassXC: нет записей или сервис недоступен"))
		return "", false
	}
	d := qt.NewQDialog(parent) // parent — правильное window-stacking редактора
	d.SetWindowTitle(i18n.Tr("KeePass", "Выберите запись KeePass"))
	d.SetMinimumSize(qt.NewQSize2(420, 380))

	tree := qt.NewQTreeWidget2()
	tree.SetHeaderLabel(i18n.Tr("KeePass", "Запись"))
	byUUID := make(map[unsafe.Pointer]string)
	for _, it := range keepassPickerItems(entries) {
		n := qt.NewQTreeWidgetItem()
		n.SetText(0, it.Label)
		byUUID[itemID(n)] = it.UUID
		tree.AddTopLevelItem(n)
	}
	// двойной клик — тоже выбор (ревью #4): захватываем picked ДО d.Accept,
	// иначе диалог закрывался без подстановки uuid (silent no-op).
	picked := ""
	tree.OnItemDoubleClicked(func(item *qt.QTreeWidgetItem, _ int) {
		if item != nil {
			if uuid, ok2 := byUUID[itemID(item)]; ok2 {
				picked = uuid
			}
		}
		d.Accept()
	})

	box := qt.NewQVBoxLayout2()
	box.AddWidget(tree.QWidget)
	dialogButtons(box, d, i18n.Tr("KeePass", "Выбрать"), func() {
		if item := tree.CurrentItem(); item != nil {
			if uuid, ok2 := byUUID[itemID(item)]; ok2 {
				picked = uuid
			}
		}
		d.Accept()
	})
	d.SetLayout(box.QLayout)
	d.Exec()
	return picked, picked != ""
}
