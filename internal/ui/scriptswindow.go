package ui

// M32 PACScripts GUI: окно пользовательских скриптов (--scripts) — список
// скриптов из ScriptsDir + запуск (RunAPI) + построчный вывод. API-бэкенд
// над живыми сессиями — api_backend.go (scripts.API: Connect/StopConn/Send/OpenConn).

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/scripts"
	qt "github.com/mappu/miqt/qt6"
)

// listScripts — имена файлов-скриптов в каталоге (не каталоги, без пустых),
// отсортированные по имени.
func listScripts(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// showScriptsWindow — диалог скриптов: список + «Выполнить» + вывод. Qt-нить.
func showScriptsWindow(cfg *config.Config, cfgPath string, parent *qt.QWidget) {
	// Контекст жизни диалога: закрытие окна отменяет запущенные скрипты
	// (раньше передавался context.Background(), и зависший скрипт нельзя было
	// остановить, а вывод рос без предела). d.Exec() модален — отмена ниже
	// срабатывает ровно при закрытии диалога.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sdir := scripts.ScriptsDir(cfgPath)
	names := listScripts(sdir)
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Scripts", "Пользовательские скрипты"))
	d.SetMinimumSize(qt.NewQSize2(540, 420))

	list := qt.NewQListWidget2()
	for _, n := range names {
		list.AddItem(n)
	}
	output := qt.NewQPlainTextEdit2()
	output.SetReadOnly(true)
	output.SetPlaceholderText(i18n.Tr("Scripts", "Вывод скрипта…"))

	run := qt.NewQPushButton3(i18n.Tr("Scripts", "Выполнить"))
	run.OnClicked(func() {
		item := list.CurrentItem()
		if item == nil {
			return
		}
		name := item.Text()
		path, ok := scripts.Lookup(sdir, name)
		if !ok {
			output.SetPlainText(i18n.Tr("Scripts", "Скрипт не найден: ") + name)
			return
		}
		// ревью: RunAPI НЕ вызывать синхронно из Qt-обработчика (иначе Qt-нить
		// заморожена в cmd.Wait() → mainthreadW из API/onLine-горутины дедлочит).
		// Запускаем в горутине; все обращения к output — через mainthreadW.
		go func() {
			cmd := exec.CommandContext(ctx, path)
			cmd.Dir = sdir
			out, err := scripts.RunAPI(ctx, cmd, &apiBackend{cfg: cfg}, func(line string) {
				mainthreadW(func() { output.AppendPlainText(line + "\n") })
			})
			mainthreadW(func() {
				if err != nil {
					output.SetPlainText(out + "\n" + err.Error())
				} else {
					output.SetPlainText(out)
				}
			})
		}()
	})
	create := qt.NewQPushButton3(i18n.Tr("Scripts", "Создать…"))
	create.OnClicked(func() {
		name := strings.TrimSpace(inputText(d.QWidget, i18n.Tr("Scripts", "Создать скрипт"), i18n.Tr("Scripts", "Имя файла"), ""))
		if name == "" {
			return
		}
		p, ok := scripts.Path(sdir, name)
		if !ok {
			showErr(d.QWidget, i18n.Tr("Scripts", "Недопустимое имя файла"))
			return
		}
		content, ok := scriptEditorDialog(d.QWidget, i18n.Tr("Scripts", "Создать скрипт"), name, "")
		if !ok {
			return
		}
		// O_EXCL — исключает TOCTOU и перезапись существующего (ревью ACCEPT MINOR).
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
		if err != nil {
			if os.IsExist(err) {
				showErr(d.QWidget, i18n.Tr("Scripts", "Скрипт уже существует: ")+name)
			} else {
				showErr(d.QWidget, i18n.Tr("Scripts", "Ошибка записи: ")+err.Error())
			}
			return
		}
		if _, err := f.WriteString(content); err != nil {
			f.Close()
			showErr(d.QWidget, i18n.Tr("Scripts", "Ошибка записи: ")+err.Error())
			return
		}
		f.Close()
		list.Clear()
		for _, n := range listScripts(sdir) {
			list.AddItem(n)
		}
	})
	edit := qt.NewQPushButton3(i18n.Tr("Scripts", "Редактировать"))
	edit.OnClicked(func() {
		item := list.CurrentItem()
		if item == nil {
			return
		}
		name := item.Text()
		p, ok := scripts.Lookup(sdir, name)
		if !ok {
			output.SetPlainText(i18n.Tr("Scripts", "Скрипт не найден: ") + name)
			return
		}
		b, err := os.ReadFile(p)
		if err != nil {
			showErr(d.QWidget, i18n.Tr("Scripts", "Ошибка чтения: ")+err.Error())
			return
		}
		content, ok := scriptEditorDialog(d.QWidget, i18n.Tr("Scripts", "Редактировать скрипт"), name, string(b))
		if !ok {
			return
		}
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			showErr(d.QWidget, i18n.Tr("Scripts", "Ошибка записи: ")+err.Error())
			return
		}
	})
	close := qt.NewQPushButton3(i18n.Tr("Scripts", "Закрыть"))
	close.OnClicked(d.Reject)
	btnRow := qt.NewQHBoxLayout2()
	btnRow.AddWidget(run.QWidget)
	btnRow.AddWidget(create.QWidget)
	btnRow.AddWidget(edit.QWidget)
	btnRow.AddWidget(close.QWidget)
	btnRow.AddStretch()

	box := qt.NewQVBoxLayout2()
	box.AddWidget(list.QWidget)
	box.AddWidget(output.QWidget)
	box.AddLayout(btnRow.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
}

// scriptEditorDialog — редактор содержимого скрипта (QPlainTextEdit).
// Возвращает отредактированное содержимое и ok=false при отмене. Qt-нить.
func scriptEditorDialog(parent *qt.QWidget, title, name, content string) (string, bool) {
	_ = parent // miqt NewQDialog2() без родителя; диалог модальный (stacking ок)
	d := qt.NewQDialog2()
	d.SetWindowTitle(title + " — " + name)
	d.SetMinimumSize(qt.NewQSize2(560, 420))
	edit := qt.NewQPlainTextEdit2()
	edit.SetPlainText(content)
	var result string
	box := qt.NewQVBoxLayout2()
	box.AddWidget(edit.QWidget)
	dialogButtons(box, d, i18n.Tr("Dialog", "Сохранить"), func() { result = edit.ToPlainText(); d.Accept() })
	d.SetLayout(box.QLayout)
	if d.Exec() != int(qt.QDialog__Accepted) {
		return "", false
	}
	return result, true
}
