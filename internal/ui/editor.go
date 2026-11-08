package ui

import (
	"strconv"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/method"
	qt "github.com/mappu/miqt/qt6"
)

// methodNames — все диалекты для комбо редактора. База — курированный порядок
// (как привык пользователь), ХВОСТ — все диалекты реестра, которых в базе нет:
// новый диалект (M45: PVE) не может «потеряться» из списка типов при добавлении
// в реестр (живой инцидент 2026-10-14).
var methodNames = func() []string {
	ds := []method.Dialect{
		method.SSH, method.SFTP, method.Telnet, method.FTP, method.WebDAV,
		method.MOSH, method.SerialCu, method.SerialRemoteTTY, method.IBM3270,
		method.RDesktop, method.XFreeRDP, method.VNC, method.Generic, method.LocalShell,
	}
	seen := map[method.Dialect]bool{}
	out := make([]string, 0, len(ds)+4)
	for _, d := range ds {
		seen[d] = true
		out = append(out, method.DisplayName(d))
	}
	for _, d := range method.AllDialects() {
		if !seen[d] {
			out = append(out, method.DisplayName(d))
		}
	}
	return out
}()

// mkVTab — вкладка с QFormLayout.
func mkVTab(form *qt.QFormLayout) (*qt.QWidget, *qt.QFormLayout) {
	v := qt.NewQWidget2()
	v.SetLayout(form.QLayout)
	return v, form
}

// clearVBox — удалить все виджеты из layout и освободить.
func clearVBox(lay *qt.QVBoxLayout, widgets *[]*qt.QWidget) {
	for _, w := range *widgets {
		lay.RemoveWidget(w)
		w.DeleteLater()
	}
	*widgets = nil
}

type editorState struct {
	auth     *qt.QComboBox
	protoBox *qt.QWidget
	protoLay *qt.QVBoxLayout
	protoCBs []*qt.QWidget // галки-опции (для clearVBox)
	cmdBox   *qt.QWidget   // блок терминально-командных опций (RDP/VNC — скрыт)
	// M37 (п.4): строка «Безопасность VNC» — показывается только VNC-диалекту
	// (ярлык свой, иначе QFormLayout не даёт спрятать авто-созданный).
	vncLbl  *qt.QLabel
	vncSec  *qt.QComboBox
	dialect func() method.Dialect
	curD    method.Dialect // текущий диалект (для конверсии живых опций)
	rebuild bool           // перестройка идёт (auth-обработчик не дублирует renderAuthRows)
	// M38: блок «Экран» (RDP) — размер/глубина/раскладка + «картиночные» галки.
	screenBox  *qt.QWidget
	screenLay  *qt.QVBoxLayout // галки эффектов
	screenCBs  []*qt.QWidget
	sizeCombo  *qt.QComboBox
	sizeWH     *qt.QWidget // строка «Свой…» (Ш x В)
	sizeW      *qt.QLineEdit
	sizeH      *qt.QLineEdit
	depthCombo *qt.QComboBox
	kbdEdit    *qt.QLineEdit
}

// editConnection — модальный редактор соединения (M13; M34: протокол-зависимые
// auth-поля и опции-галки). Смена типа конвертирует опции и перестраивает
// auth/галки (issues 1–3). Возвращает true, если пользователь сохранил.
func editConnection(parent *qt.QWidget, env *config.Environment, uuid string, save func(*config.Environment)) bool {
	// M36-ревью MAJOR: единая точка защиты _protected — все вызовы (tree_menu,
	// тулбар, терминальное меню, hotkey Alt+e, IPC edit-uuid, --edit-uuid)
	// больше не обходят защиту. Возвращаем false (не открыт редактор).
	if env != nil && env.Protected == 1 {
		showErr(parent, i18n.Tr("Editor", "Узел защищён: редактирование запрещено"))
		return false
	}
	f := formFromEnv(env)
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Editor", "Соединение: ") + env.Name)
	d.SetMinimumWidth(760)
	tabs := qt.NewQTabWidget2()

	// ---- Основные -------------------------------------------------
	mainForm := qt.NewQFormLayout2()
	name := qt.NewQLineEdit3(f.Name)
	ip := qt.NewQLineEdit3(f.IP)
	port := qt.NewQLineEdit3(f.Port)
	user := qt.NewQLineEdit3(f.User)
	pass := qt.NewQLineEdit3(f.Pass)
	pass.SetEchoMode(qt.QLineEdit__Password)

	combo := qt.NewQComboBox2()
	for _, m := range methodNames {
		combo.AddItem(m)
	}
	target, known := comboTarget(env.Method)
	if !known {
		combo.AddItem(target) // незнакомый метод остаётся пунктом (ревью R4)
	}
	combo.SetCurrentText(target)
	opts := qt.NewQLineEdit3(f.Options)

	st := &editorState{}
	st.dialect = func() method.Dialect {
		if dd, ok := method.FromDisplay(combo.CurrentText()); ok {
			return dd
		}
		return ""
	}

	// ---- Auth (issue 1: протокол-зависимые типы и поля) ------------
	st.auth = qt.NewQComboBox2()

	fallback := qt.NewQComboBox2()
	for _, a := range []string{"0", "1"} {
		fallback.AddItem(a)
	}
	fallback.SetCurrentText(f.AuthFallback)
	fallback.SetToolTip(i18n.Tr("Editor", "0 — строго основной способ; 1 — разрешить резервную авторизацию (напр. пароль, если ключ не сработал)."))
	pub := qt.NewQLineEdit3(f.PublicKey)
	// a) Приватный ключ: поле + кнопка «Выбрать…» (вызов файловой системы), как у KeePass UUID.
	pubBrowse := qt.NewQPushButton3(i18n.Tr("Editor", "Выбрать…"))
	pubBrowse.OnClicked(func() {
		// Очный дебаг issue 1: диалог должен быть модальным к РЕДАКТОРУ (d), а не
		// к дереву/главному окну — иначе окно не возвращается из фона после выбора.
		if p := qt.QFileDialog_GetOpenFileName2(d.QWidget, i18n.Tr("Editor", "Выберите закрытый ключ")); p != "" {
			pub.SetText(p)
		}
	})
	pubRow := qt.NewQWidget2()
	pubLay := qt.NewQHBoxLayout2()
	pubLay.AddWidget2(pub.QWidget, 1)
	pubLay.AddWidget(pubBrowse.QWidget)
	pubRow.SetLayout(pubLay.QLayout)
	pph := qt.NewQLineEdit3(f.Passphrase)

	mainForm.AddRow3(i18n.Tr("Editor", "Имя"), name.QWidget)
	mainForm.AddRow3(i18n.Tr("Editor", "IP"), ip.QWidget)
	mainForm.AddRow3(i18n.Tr("Editor", "Порт"), port.QWidget)
	mainForm.AddRow3(i18n.Tr("Editor", "Тип"), combo.QWidget)
	mainForm.AddRow3(i18n.Tr("Editor", "Опции"), opts.QWidget)
	mainForm.AddRow3(i18n.Tr("Editor", "Пользователь"), user.QWidget)
	// M37 (п.3): «глаз» — показать/скрыть введённый пароль.
	passRow, _ := passEyeRow(pass)
	mainForm.AddRow3(i18n.Tr("Editor", "Пароль"), passRow)
	mainForm.AddRow3(i18n.Tr("Editor", "Тип авторизации"), st.auth.QWidget)
	// Auth-зависимые строки добавлены ПРЯМО в mainForm (выровнены по сетке с
	// остальными), а не вложенным под-блоком (иначе метки съезжали — очный
	// дебаг 2b/2c). Показ/скрытие — SetRowVisible по виджету (без removeRow).
	mainForm.AddRow3(i18n.Tr("Editor", "Резервная авторизация"), fallback.QWidget)
	mainForm.AddRow3(i18n.Tr("Editor", "Приватный ключ"), pubRow)
	mainForm.AddRow3(i18n.Tr("Editor", "Парольная фраза"), pph.QWidget)

	// Основные: БАЗОВАЯ страница (как Remmina) — должно идти ПЕРВОЙ вкладкой.
	// Имя/IP/Порт/Тип/Пользователь/Пароль + auth-блок. Раньше mainForm строился,
	// но НЕ добавлялся в tabs — вкладка «Основные» отсутствовала, а осиротевшие
	// виджеты (auth-блок) всплывали отдельным окном (очный дебаг, issue 2).
	// M38: графические опции RDP (размер экрана / глубина / раскладка +
	// «картиночные» галки). Показываются только RDP-диалектам — данные в method.
	screenBox := qt.NewQWidget2()
	screenForm := qt.NewQFormLayout2()
	sizeCombo := qt.NewQComboBox2()
	sizeCombo.AddItem(i18n.Tr("Editor", "По размеру окна"))
	sizeCombo.AddItem(i18n.Tr("Editor", "Во весь экран"))
	for _, pz := range method.ScreenSizePresets() {
		sizeCombo.AddItem(pz)
	}
	sizeCombo.AddItem(i18n.Tr("Editor", "Свой…"))
	sizeW, sizeH := qt.NewQLineEdit2(), qt.NewQLineEdit2()
	sizeW.SetFixedWidth(64)
	sizeH.SetFixedWidth(64)
	sizeWH := qt.NewQWidget2()
	sizeWHLay := qt.NewQHBoxLayout2()
	sizeWHLay.AddWidget(sizeW.QWidget)
	sizeWHLay.AddWidget(qt.NewQLabel3("x").QWidget)
	sizeWHLay.AddWidget(sizeH.QWidget)
	sizeWHLay.AddStretch()
	sizeWH.SetLayout(sizeWHLay.QLayout)
	depthCombo := qt.NewQComboBox2()
	depthCombo.AddItem(i18n.Tr("Editor", "Не задана"))
	for _, b := range []string{"8", "15", "16", "24", "32"} {
		depthCombo.AddItem(b)
	}
	kbdEdit := qt.NewQLineEdit2()
	// Ревью M43 D3: подсказка «en-us» вводила в заблуждение — FreeRDP знает
	// ЛОКАЛЬНЫЕ ИМЕНА («Russian», «United States - English»), «en-us» он молча
	// игнорирует. Пусто = объявим локальную раскладку автоматически.
	kbdEdit.SetPlaceholderText(i18n.Tr("Editor", "напр. Russian (пусто — определить автоматически)"))
	screenForm.AddRow3(i18n.Tr("Editor", "Размер экрана"), sizeCombo.QWidget)
	screenForm.AddRow3("", sizeWH)
	screenForm.AddRow3(i18n.Tr("Editor", "Глубина цвета"), depthCombo.QWidget)
	screenForm.AddRow3(i18n.Tr("Editor", "Раскладка клавиатуры"), kbdEdit.QWidget)
	effBox := qt.NewQWidget2()
	effLay := qt.NewQVBoxLayout2()
	effLay.SetContentsMargins(0, 0, 0, 0)
	effBox.SetLayout(effLay.QLayout)
	screenForm.AddRowWithWidget(effBox)
	screenBox.SetLayout(screenForm.QLayout)
	mainForm.AddRowWithWidget(screenBox)
	st.screenBox, st.screenLay = screenBox, effLay
	st.sizeCombo, st.sizeWH, st.sizeW, st.sizeH = sizeCombo, sizeWH, sizeW, sizeH
	st.depthCombo, st.kbdEdit = depthCombo, kbdEdit

	// «Свой…» → показать поля Ш x В; иначе — применить выбранный размер.
	applySizeWidgets := func() {
		custom := sizeCombo.CurrentText() == i18n.Tr("Editor", "Свой…")
		sizeWH.SetVisible(custom)
	}
	sizeCombo.OnCurrentTextChanged(func(string) {
		if st.rebuild {
			return
		}
		applySizeWidgets()
		size := ""
		switch t := sizeCombo.CurrentText(); {
		case t == i18n.Tr("Editor", "По размеру окна"):
			size = ""
		case t == i18n.Tr("Editor", "Во весь экран"):
			size = "full"
		case t == i18n.Tr("Editor", "Свой…"):
			size = sizeW.Text() + "x" + sizeH.Text()
		default:
			size = t
		}
		if out, err := method.SetScreenSize(opts.Text(), st.curD, size); err == nil {
			opts.SetText(out)
		}
	})
	applyCustom := func() {
		if st.rebuild || sizeCombo.CurrentText() != i18n.Tr("Editor", "Свой…") {
			return
		}
		if out, err := method.SetScreenSize(opts.Text(), st.curD, sizeW.Text()+"x"+sizeH.Text()); err == nil {
			opts.SetText(out)
		}
	}
	sizeW.OnEditingFinished(applyCustom)
	sizeH.OnEditingFinished(applyCustom)
	depthCombo.OnCurrentTextChanged(func(t string) {
		if st.rebuild {
			return
		}
		if t == i18n.Tr("Editor", "Не задана") {
			t = ""
		}
		if out, err := method.SetColorDepth(opts.Text(), st.curD, t); err == nil {
			opts.SetText(out)
		}
	})
	kbdEdit.OnEditingFinished(func() {
		if st.rebuild {
			return
		}
		if out, err := method.SetKeyboardLayout(opts.Text(), st.curD, kbdEdit.Text()); err == nil {
			opts.SetText(out)
		}
	})

	mainV, _ := mkVTab(mainForm)
	tabs.AddTab(mainV, i18n.Tr("Editor", "Основные"))

	// ---- Опции: галки (issue 2/3) ----------------------------------
	optForm := qt.NewQFormLayout2()
	prepend := qt.NewQLineEdit3(f.Prepend)
	postpend := qt.NewQLineEdit3(f.Postpend)
	sendStr := qt.NewQLineEdit3(f.SendString)
	sendEvery := qt.NewQLineEdit3(strconv.Itoa(f.SendStringEvery))
	// QuoteCommand/QuotePostCommand легаси-редактора здесь намеренно нет:
	// Go-порт собирает argv без sh, кавычки ни на что не влияют (раньше
	// чекбоксы сохранялись, но runtime их нигде не читал).
	onlyIdle := qt.NewQCheckBox3("send string only when idle")
	onlyIdle.SetChecked(f.SendStringOnlyWhenIdle)
	useSudo := qt.NewQCheckBox3("use sudo")
	useSudo.SetChecked(f.UseSudo)
	autoreconn := qt.NewQCheckBox3("autoreconnect")
	autoreconn.SetChecked(f.AutoReconnect)
	autossh := qt.NewQCheckBox3("autossh")
	autossh.SetChecked(f.Autossh)
	socks := qt.NewQCheckBox3(i18n.Tr("Editor", "Создать случайный SOCKS-туннель")) // бэклог Incognito (M30)
	socks.SetChecked(f.EnableRandomSocks)

	protoBox := qt.NewQWidget2()
	protoLay := qt.NewQVBoxLayout2()
	protoBox.SetLayout(protoLay.QLayout)
	st.protoBox, st.protoLay = protoBox, protoLay

	optForm.AddRowWithWidget(protoBox) // галки-опции протокола (перестраиваются)
	// Команды/терминальные опции — только для методов с командным интерфейсом
	// (SSH/Telnet/SFTP/…), НЕ для RDP/VNC (иначе на RDP показывались SSH-галки,
	// очный дебаг issue 1). Весь блок в cmdBox — показываем/прячем по диалекту.
	cmdBox := qt.NewQWidget2()
	cmdForm := qt.NewQFormLayout2()
	cmdBox.SetLayout(cmdForm.QLayout)
	st.cmdBox = cmdBox
	cmdForm.AddRow3(i18n.Tr("Editor", "Команда перед"), prepend.QWidget)
	cmdForm.AddRow3(i18n.Tr("Editor", "Команда после"), postpend.QWidget)
	cmdForm.AddRow3(i18n.Tr("Editor", "Строка отправки"), sendStr.QWidget)
	cmdForm.AddRow3(i18n.Tr("Editor", "Send string every (с)"), sendEvery.QWidget)
	cmdForm.AddRowWithWidget(onlyIdle.QWidget)
	cmdForm.AddRowWithWidget(useSudo.QWidget)
	cmdForm.AddRowWithWidget(autoreconn.QWidget)
	cmdForm.AddRowWithWidget(autossh.QWidget)
	cmdForm.AddRowWithWidget(socks.QWidget)
	// M30: структурные виджеты -L/-R/-D (SSH forwarding) сверх сырого options.
	forwards := qt.NewQTableWidget3(len(forwardRows(f.Options)), 2)
	forwards.SetHorizontalHeaderLabels([]string{i18n.Tr("Editor", "Тип (L/R/D)"), i18n.Tr("Editor", "Спецификация")})
	for i, r := range forwardRows(f.Options) {
		forwards.SetItem(i, 0, qt.NewQTableWidgetItem2(r.Kind))
		forwards.SetItem(i, 1, qt.NewQTableWidgetItem2(r.Spec))
	}
	addFwd := qt.NewQPushButton3(i18n.Tr("Editor", "Добавить проброс"))
	addFwd.OnClicked(func() { forwards.SetRowCount(forwards.RowCount() + 1) })
	fwdV := qt.NewQWidget2()
	fwdLay := qt.NewQVBoxLayout2()
	fwdLay.AddWidget(forwards.QWidget)
	fwdLay.AddWidget(addFwd.QWidget)
	fwdV.SetLayout(fwdLay.QLayout)
	cmdForm.AddRow3(i18n.Tr("Editor", "Проброс (L/R/D)"), fwdV)
	optForm.AddRowWithWidget(cmdBox)
	optV, _ := mkVTab(optForm)
	tabs.AddTab(optV, i18n.Tr("Editor", "Опции"))

	// ---- Сеть -----------------------------------------------------
	netForm := qt.NewQFormLayout2()
	useProxy := qt.NewQComboBox2()
	for _, a := range []string{"0", "1", "2", "3"} {
		useProxy.AddItem(a)
	}
	useProxy.SetCurrentText(strconv.Itoa(f.UseProxy))
	pIP, pPort, pUser, pPass := qt.NewQLineEdit3(f.ProxyIP), qt.NewQLineEdit3(f.ProxyPort),
		qt.NewQLineEdit3(f.ProxyUser), qt.NewQLineEdit3(f.ProxyPass)
	pPass.SetEchoMode(qt.QLineEdit__Password) // MINOR-1: прокси-пароль не показывать открытым текстом
	jIP, jPort, jUser, jPass, jKey := qt.NewQLineEdit3(f.JumpIP), qt.NewQLineEdit3(strconv.Itoa(f.JumpPort)),
		qt.NewQLineEdit3(f.JumpUser), qt.NewQLineEdit3(f.JumpPass), qt.NewQLineEdit3(f.JumpKey)
	jPass.SetEchoMode(qt.QLineEdit__Password) // MINOR-1: jump-пароль не показывать открытым текстом
	netForm.AddRow3(i18n.Tr("Editor", "Использовать прокси"), useProxy.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Прокси IP"), pIP.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Порт прокси"), pPort.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Пользователь прокси"), pUser.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Пароль прокси"), pPass.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Jump IP"), jIP.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Jump port"), jPort.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Jump user"), jUser.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Jump pass"), jPass.QWidget)
	netForm.AddRow3(i18n.Tr("Editor", "Jump key"), jKey.QWidget)
	netV, _ := mkVTab(netForm)
	tabs.AddTab(netV, i18n.Tr("Editor", "Сеть"))

	// ---- Переменные (name|value) ----------------------------------
	varTable := qt.NewQTableWidget3(len(f.Variables), 2)
	varTable.SetHorizontalHeaderLabels([]string{i18n.Tr("Editor", "Имя"), i18n.Tr("Editor", "Значение")})
	for i, m := range f.Variables {
		varTable.SetItem(i, 0, qt.NewQTableWidgetItem2(str(m, "name")))
		varTable.SetItem(i, 1, qt.NewQTableWidgetItem2(str(m, "txt")))
	}
	addVar := qt.NewQPushButton3(i18n.Tr("Editor", "+ переменную"))
	addVar.OnClicked(func() { varTable.SetRowCount(varTable.RowCount() + 1) })
	varV := qt.NewQWidget2()
	varLayout := qt.NewQVBoxLayout2()
	varLayout.AddWidget(varTable.QWidget)
	varLayout.AddWidget(addVar.QWidget)
	varV.SetLayout(varLayout.QLayout)
	tabs.AddTab(varV, i18n.Tr("Editor", "Переменные"))

	// ---- Expect (таблица блоков) -----------------------------------
	expectTable := qt.NewQTableWidget3(len(f.ExpectRows), 8)
	expectTable.SetHorizontalHeaderLabels([]string{i18n.Tr("Editor", "Акт"), i18n.Tr("Editor", "Ожидание"), i18n.Tr("Editor", "Отправка"), i18n.Tr("Editor", "Скрыт"), "Return", "on_match", "on_fail", "time_out"})
	for i, r := range f.ExpectRows {
		cellActive := qt.NewQTableWidgetItem2("")
		cellActive.SetCheckState(checkedOrNot(r.Active))
		expectTable.SetItem(i, 0, cellActive)
		expectTable.SetItem(i, 1, qt.NewQTableWidgetItem2(r.Expect))
		expectTable.SetItem(i, 2, qt.NewQTableWidgetItem2(r.Send))
		cellHidden := qt.NewQTableWidgetItem2("")
		cellHidden.SetCheckState(checkedOrNot(r.Hidden))
		expectTable.SetItem(i, 3, cellHidden)
		expectTable.SetItem(i, 4, qt.NewQTableWidgetItem2(strconv.Itoa(boolToInt(r.Return))))
		expectTable.SetItem(i, 5, qt.NewQTableWidgetItem2(strconv.Itoa(r.OnMatch)))
		expectTable.SetItem(i, 6, qt.NewQTableWidgetItem2(strconv.Itoa(r.OnFail)))
		expectTable.SetItem(i, 7, qt.NewQTableWidgetItem2(strconv.Itoa(r.TimeOut)))
	}
	addRow := qt.NewQPushButton3(i18n.Tr("Editor", "+ строку"))
	addRow.OnClicked(func() { expectTable.SetRowCount(expectTable.RowCount() + 1) })
	expV := qt.NewQWidget2()
	expLayout := qt.NewQVBoxLayout2()
	expLayout.AddWidget(expectTable.QWidget)
	expLayout.AddWidget(addRow.QWidget)
	expV.SetLayout(expLayout.QLayout)
	tabs.AddTab(expV, i18n.Tr("Editor", "Ожидания"))

	// ---- Расширенные (M29-остаток #2): Description/Title/KeePass-per-conn/
	// startup (запуск/скрипт) + макросы -----------------------------------
	advForm := qt.NewQFormLayout2()
	desc := qt.NewQLineEdit3(f.Description)
	title := qt.NewQLineEdit3(f.Title)
	kpxu := qt.NewQLineEdit3(f.KPXUUID)
	startLaunch := qt.NewQCheckBox3(i18n.Tr("Editor", "Запускать соединение при старте"))
	startLaunch.SetChecked(f.StartUpLaunch)
	startScript := qt.NewQLineEdit3(f.StartUpScript)
	startScriptName := qt.NewQLineEdit3(f.StartUpScriptName)
	advForm.AddRow3(i18n.Tr("Editor", "Описание"), desc.QWidget)
	advForm.AddRow3(i18n.Tr("Editor", "Заголовок"), title.QWidget)
	// KPX UUID + «Выбрать…» (KeePassXC-list, #4): выбор записи по метаданным.
	kpxuRow := qt.NewQWidget2()
	kpxuLay := qt.NewQHBoxLayout2()
	kpxuLay.AddWidget(kpxu.QWidget)
	pickKPX := qt.NewQPushButton3(i18n.Tr("Editor", "Выбрать…"))
	pickKPX.OnClicked(func() {
		// Диалог должен быть модальным к РЕДАКТОРУ (d), как у файлового
		// picker'а выше: иначе остаётся за модальным окном редактора.
		if uuid, ok := pickKeePassUUID(d.QWidget); ok {
			kpxu.SetText(uuid)
		}
	})
	kpxuLay.AddWidget(pickKPX.QWidget)
	kpxuRow.SetLayout(kpxuLay.QLayout)
	advForm.AddRow3(i18n.Tr("Editor", "KeePass UUID"), kpxuRow)
	// VNC security (#4, VeNCrypt): auto/vencrypt/none.
	vncSec := qt.NewQComboBox2()
	for _, v := range vncSecurityValues() {
		vncSec.AddItem(v)
	}
	vncCur := f.VNCEncryption
	if !validVNCEncryption(vncCur) {
		vncCur = "auto"
	}
	vncSec.SetCurrentText(vncCur)
	// M37 (п.4): свой ярлык — чтобы прятать строку целиком для не-VNC методов.
	vncLbl := qt.NewQLabel3(i18n.Tr("Editor", "Безопасность VNC"))
	advForm.AddRow(vncLbl.QWidget, vncSec.QWidget)
	st.vncLbl, st.vncSec = vncLbl, vncSec
	// #7-UI: use secret service + Store/Delete (пароль в SecretService по метке
	// asbru:<uuid>). Ревью: Store/Delete — явные действия (не на каждый save).
	ussChk := qt.NewQCheckBox3("use secret service")
	ussChk.SetChecked(f.UseSecretService)
	advForm.AddRowWithWidget(ussChk.QWidget)
	ssStore := qt.NewQPushButton3(i18n.Tr("Editor", "Сохранить пароль в SecretService"))
	ssStore.OnClicked(func() {
		if uuid == "" {
			return
		}
		// ревью MAJOR: пустой пароль НЕ сохраняем — иначе затираем существующий
		// секрет пустым (silent credential destruction). #7-x: храним и пароль-фразу.
		if strings.TrimSpace(pass.Text()) == "" && strings.TrimSpace(pph.Text()) == "" {
			showErr(parent, i18n.Tr("Editor", "Пароль пуст — нечего сохранять в SecretService"))
			return
		}
		if err := StoreSecretServiceCreds(&config.Environment{Pass: pass.Text(), Passphrase: pph.Text()}, uuid); err != nil {
			showErr(parent, i18n.Tr("Editor", "SecretService: ")+err.Error())
		}
	})
	ssDel := qt.NewQPushButton3(i18n.Tr("Editor", "Удалить из SecretService"))
	ssDel.OnClicked(func() {
		if uuid != "" {
			if err := DeleteSecretServiceCreds(&config.Environment{}, uuid); err != nil {
				showErr(parent, i18n.Tr("Editor", "SecretService: ")+err.Error())
			}
		}
	})
	ssRow := qt.NewQWidget2()
	ssLay := qt.NewQHBoxLayout2()
	ssLay.AddWidget(ssStore.QWidget)
	ssLay.AddWidget(ssDel.QWidget)
	ssRow.SetLayout(ssLay.QLayout)
	advForm.AddRowWithWidget(ssRow)
	advForm.AddRowWithWidget(startLaunch.QWidget)
	advForm.AddRow3(i18n.Tr("Editor", "Стартовый скрипт"), startScript.QWidget)
	advForm.AddRow3(i18n.Tr("Editor", "Имя стартового скрипта"), startScriptName.QWidget)
	// макросы (name/cmd): таблица, как переменные (легаси PACTerminal.pm macros).
	macroTable := qt.NewQTableWidget3(len(f.Macros), 2)
	macroTable.SetHorizontalHeaderLabels([]string{i18n.Tr("Editor", "Название"), i18n.Tr("Editor", "Команда")})
	for i, m := range f.Macros {
		macroTable.SetItem(i, 0, qt.NewQTableWidgetItem2(str(m, "description")))
		macroTable.SetItem(i, 1, qt.NewQTableWidgetItem2(str(m, "txt")))
	}
	addMacro := qt.NewQPushButton3(i18n.Tr("Editor", "+ макрос"))
	addMacro.OnClicked(func() { macroTable.SetRowCount(macroTable.RowCount() + 1) })
	advV := qt.NewQWidget2()
	advLayout := qt.NewQVBoxLayout2()
	advLayout.AddLayout(advForm.QLayout)
	advLayout.AddWidget(macroTable.QWidget)
	advLayout.AddWidget(addMacro.QWidget)
	advV.SetLayout(advLayout.QLayout)
	tabs.AddTab(advV, i18n.Tr("Editor", "Расширенные"))

	// ---- Terminal Options (M29-остаток #2): per-conn Look&Feel ----------
	// Перекрытия глобальных defaults при 'use personal settings'. Показываем
	// эффективные значения (терминальные defaults, перекрытые per-conn).
	var cfgDefaults *config.Defaults
	if c := currentConfig(); c != nil {
		cfgDefaults = &c.Defaults
	}
	te := termOptsEditFromEnv(cfgDefaults, env)
	termForm := qt.NewQFormLayout2()
	personal := qt.NewQCheckBox3(i18n.Tr("Editor", "Использовать личные настройки"))
	personal.SetChecked(te.UsePersonal)
	tFont := qt.NewQLineEdit3(te.Font)
	tFg := qt.NewQLineEdit3(te.TextColor)
	tBg := qt.NewQLineEdit3(te.BackColor)
	cursor := qt.NewQComboBox2()
	for _, c := range []string{"block", "underline", "ibeam"} {
		cursor.AddItem(c)
	}
	cursor.SetCurrentText(te.CursorShape)
	tScroll := qt.NewQLineEdit3(te.Scrollback)
	tTransp := qt.NewQLineEdit3(te.Transparency)
	tWord := qt.NewQLineEdit3(te.WordChars)
	boldLike := qt.NewQCheckBox3("bold color like text")
	boldLike.SetChecked(te.BoldLikeText)
	tBold := qt.NewQLineEdit3(te.BoldColor)
	termForm.AddRowWithWidget(personal.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Шрифт"), tFont.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Цвет текста"), tFg.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Цвет фона"), tBg.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Форма курсора"), cursor.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Строк истории"), tScroll.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Прозрачность (%)"), tTransp.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Слова (double-click)"), tWord.QWidget)
	termForm.AddRowWithWidget(boldLike.QWidget)
	termForm.AddRow3(i18n.Tr("Editor", "Цвет жирного"), tBold.QWidget)
	termV, _ := mkVTab(termForm)
	tabs.AddTab(termV, i18n.Tr("Editor", "Опции терминала"))

	// renderAuthRows — показать/скрыть auth-строки под диалект и auth-тип.
	// SetRowVisible по виджету (скрывает строку с меткой) — без removeRow
	// (тот удаляет виджеты). Строки теперь в mainForm, выровнены по сетке.
	renderAuthRows := func(d method.Dialect, cur string) {
		fe := method.FeaturesFor(d)
		showPub := fe.HasPubKey && cur == method.AuthPublickey.String()
		mainForm.SetRowVisible2(fallback.QWidget, fe.HasAuthFallback)
		// Очный дебаг issue 6: при publickey пароль соединения не используется
		// (кред — ключ + парольная фраза) → скрываем поле «Пароль».
		// Скрывать нужно СТРОКУ passRow (поле + «глаз»), а не сам pass: строка
		// добавлена в форму как passRow, и SetRowVisible2(pass) не находил её.
		mainForm.SetRowVisible2(passRow, !showPub)
		mainForm.SetRowVisible2(pubRow, showPub)
		mainForm.SetRowVisible2(pph.QWidget, showPub)
	}

	// ---- Динамика по методу ----------------------------------------
	// Перестроить auth-поля (issue 1) + галки (issue 3) под диалект.
	refreshByMethod := func(d method.Dialect) {
		st.rebuild = true
		defer func() { st.rebuild = false }()
		fe := method.FeaturesFor(d)
		// auth combo: только допустимые типы
		st.auth.Clear()
		for _, a := range fe.AuthTypes {
			st.auth.AddItem(a.String())
		}
		cur := f.AuthType
		if !method.ValidAuthType(d, cur) {
			cur = ""
			if len(fe.AuthTypes) > 0 {
				cur = fe.AuthTypes[0].String()
			}
		}
		if cur != "" {
			st.auth.SetCurrentText(cur)
		}
		// auth-зависимые поля (fallback/pubkey/passphrase)
		renderAuthRows(d, cur)
		// галки-опции (issue 3): живая строка opts
		clearVBox(st.protoLay, &st.protoCBs)
		clearVBox(st.screenLay, &st.screenCBs)
		for _, t := range method.OptionsFor(d) {
			toggle := t
			cb := qt.NewQCheckBox3(toggle.Label)
			cb.SetChecked(method.TogglePresent(opts.Text(), toggle))
			cb.OnStateChanged(func(state int) {
				checked := state == 2
				newOpts, err := method.SetToggle(opts.Text(), toggle, checked)
				if err != nil {
					return
				}
				opts.SetText(newOpts)
			})
			if method.InScreenGroup(toggle.Setting) {
				st.screenLay.AddWidget(cb.QWidget)
				st.screenCBs = append(st.screenCBs, cb.QWidget)
			} else {
				st.protoLay.AddWidget(cb.QWidget)
				st.protoCBs = append(st.protoCBs, cb.QWidget)
			}
		}
		// M37 (п.2): опции-значения (комбо) — напр. уровень TLS xfreerdp.
		// Пишут/убирают ровно один токен в живой строке «Опции», как галки.
		for _, ov := range method.OptionValuesFor(d) {
			val := ov
			combo := qt.NewQComboBox2()
			for _, s := range val.Values {
				combo.AddItem(s)
			}
			cur := method.ValuePresent(opts.Text(), val)
			if cur == "" {
				cur = val.Off
			}
			combo.SetCurrentText(cur)
			combo.OnCurrentTextChanged(func(text string) {
				if st.rebuild {
					return // программная установка значения — не пишем в «Опции»
				}
				newOpts, err := method.SetValue(opts.Text(), val, text)
				if err != nil {
					return
				}
				opts.SetText(newOpts)
			})
			row := qt.NewQWidget2()
			lay := qt.NewQHBoxLayout2()
			lbl := qt.NewQLabel3(val.Label)
			lay.AddWidget(lbl.QWidget)
			lay.AddWidget(combo.QWidget)
			lay.AddStretch()
			row.SetLayout(lay.QLayout)
			st.protoLay.AddWidget(row)
			st.protoCBs = append(st.protoCBs, row)
		}
		// M38: блок «Экран» — только RDP; значения синхронизируем с «Опциями».
		hasScreen := method.HasScreenOptions(d)
		st.screenBox.SetVisible(hasScreen)
		if hasScreen {
			st.rebuild = true
			switch sz := method.ScreenSize(opts.Text(), d); {
			case sz == "full":
				st.sizeCombo.SetCurrentText(i18n.Tr("Editor", "Во весь экран"))
			case sz == "":
				st.sizeCombo.SetCurrentText(i18n.Tr("Editor", "По размеру окна"))
			default:
				found := false
				for i := 0; i < st.sizeCombo.Count(); i++ {
					if st.sizeCombo.ItemText(i) == sz {
						st.sizeCombo.SetCurrentText(sz)
						found = true
						break
					}
				}
				if !found { // WxH вне пресетов → «Свой…»
					st.sizeCombo.SetCurrentText(i18n.Tr("Editor", "Свой…"))
					if w, h, ok := method.SplitSize(sz); ok {
						st.sizeW.SetText(w)
						st.sizeH.SetText(h)
					}
				}
			}
			if bpp := method.ColorDepth(opts.Text(), d); bpp != "" {
				st.depthCombo.SetCurrentText(bpp)
			} else {
				st.depthCombo.SetCurrentText(i18n.Tr("Editor", "Не задана"))
			}
			st.kbdEdit.SetText(method.KeyboardLayout(opts.Text(), d))
			st.rebuild = false
			st.sizeWH.SetVisible(st.sizeCombo.CurrentText() == i18n.Tr("Editor", "Свой…"))
		}
		st.protoBox.SetVisible(len(st.protoCBs) > 0)
		// M37 (п.4): разделы редактора — из ДЕСКРИПТОРА диалекта, а не из
		// разбросанных условий: командные поля — командным методам,
		// «Безопасность VNC» — только VNC (для RDP показывалась зря).
		spec := method.SpecFor(d)
		st.cmdBox.SetVisible(spec.Commandable)
		if st.vncLbl != nil && st.vncSec != nil {
			st.vncLbl.SetVisible(spec.VNCSecurity)
			st.vncSec.SetVisible(spec.VNCSecurity)
		}
	}

	// первоначальное заполнение: curD = текущий диалект (из combo).
	st.curD = st.dialect()
	refreshByMethod(st.dialect())

	// смена типа → конверсия ЖИВЫХ опций (opts.Text()) в новый диалект
	// (method.ConvertOptions; M34: согласовано с freerdp↔rdesktop) + перестройка.
	combo.OnCurrentTextChanged(func(text string) {
		newD, ok := method.FromDisplay(combo.CurrentText())
		if !ok {
			return
		}
		if newD == st.curD {
			return
		}
		converted, _ := method.ConvertOptions(st.curD, newD, opts.Text())
		opts.SetText(converted)
		st.curD = newD
		refreshByMethod(newD)
	})

	// смена auth-типа → скрыть/показать pubkey/passphrase
	st.auth.OnCurrentTextChanged(func(text string) {
		if st.rebuild || !method.ValidAuthType(st.dialect(), text) {
			return
		}
		renderAuthRows(st.dialect(), text)
	})

	// ---- Сохранение ------------------------------------------------
	saved := false // сохранено ли (для addConn: откат заглушки при отмене)
	doSave := func() {
		f.Name = name.Text()
		f.IP = ip.Text()
		f.Port = port.Text()
		f.User = user.Text()
		f.Pass = pass.Text()
		f.Method = combo.CurrentText() // B1: «Тип» из combo в форму (иначе env.Method оставался старым)
		dd := st.dialect()
		if method.ValidAuthType(dd, st.auth.CurrentText()) {
			f.AuthType = st.auth.CurrentText()
		}
		f.AuthFallback = fallback.CurrentText()
		f.PublicKey = pub.Text()
		f.Passphrase = pph.Text()
		// M30: таблица forwarding (L/R/D) применяется к сырому opts.Text():
		// applyForwardRows убирает/пере-эмитит -L/-R/-D, кавычки пробельных
		// значений сохраняются (method.JoinArgs).
		fwdRows := make([]forwardRow, 0, forwards.RowCount())
		for i := 0; i < forwards.RowCount(); i++ {
			kind, spec := "", ""
			if it := forwards.Item(i, 0); it != nil {
				kind = it.Text()
			}
			if it := forwards.Item(i, 1); it != nil {
				spec = it.Text()
			}
			fwdRows = append(fwdRows, forwardRow{Kind: kind, Spec: spec})
		}
		f.Options = applyForwardRows(opts.Text(), fwdRows)
		f.Prepend = prepend.Text()
		f.Postpend = postpend.Text()
		f.SendString = sendStr.Text()
		f.SendStringEvery = atoiOr(sendEvery.Text(), f.SendStringEvery)
		f.SendStringOnlyWhenIdle = onlyIdle.IsChecked()
		f.UseSudo = useSudo.IsChecked()
		f.AutoReconnect = autoreconn.IsChecked()
		f.Autossh = autossh.IsChecked()
		f.EnableRandomSocks = socks.IsChecked()
		f.UseProxy = atoiOr(useProxy.CurrentText(), f.UseProxy)
		f.ProxyIP, f.ProxyPort, f.ProxyUser, f.ProxyPass = pIP.Text(), pPort.Text(), pUser.Text(), pPass.Text()
		f.JumpIP, f.JumpUser, f.JumpPass, f.JumpKey = jIP.Text(), jUser.Text(), jPass.Text(), jKey.Text()
		f.JumpPort = atoiOr(jPort.Text(), f.JumpPort)
		oldVars := f.Variables // до обнуления — иначе orig всегда nil (ре-ревью)
		cells := make([][2]string, 0, varTable.RowCount())
		for i := 0; i < varTable.RowCount(); i++ {
			nm, txt := "", ""
			if it := varTable.Item(i, 0); it != nil {
				nm = it.Text()
			}
			if it := varTable.Item(i, 1); it != nil {
				txt = it.Text()
			}
			cells = append(cells, [2]string{nm, txt})
		}
		f.Variables = mergeVariables(oldVars, cells)
		f.ExpectRows = nil
		for i := 0; i < expectTable.RowCount(); i++ {
			r := expectRow{Active: true, Return: true, OnMatch: -1, OnFail: -1, TimeOut: -1}
			if it := expectTable.Item(i, colActive); it != nil {
				r.Active = it.CheckState() == qt.Checked
			}
			if it := expectTable.Item(i, colHidden); it != nil {
				r.Hidden = it.CheckState() == qt.Checked
			}
			cells := map[int]string{}
			for c := colExpect; c <= colReturn; c++ {
				if it := expectTable.Item(i, c); it != nil {
					cells[c] = it.Text()
				}
			}
			for _, c := range []int{colOnMatch, colOnFail, colTimeOut} {
				if it := expectTable.Item(i, c); it != nil {
					cells[c] = it.Text()
				}
			}
			r = expectRowFromCells(cells, r)
			f.ExpectRows = append(f.ExpectRows, r)
		}
		// Расширенные (M29-остаток #2): поля + макросы (mergeMacros хранит ключи).
		f.Description, f.Title, f.KPXUUID = desc.Text(), title.Text(), kpxu.Text()
		f.StartUpLaunch = startLaunch.IsChecked()
		f.StartUpScript, f.StartUpScriptName = startScript.Text(), startScriptName.Text()
		// M37 (п.4): поле VNC-безопасности пишем ТОЛЬКО VNC-диалекту — иначе
		// скрытая для RDP строка протаскивала бы 'vnc security' в его профиль.
		if st.curD == method.VNC {
			f.VNCEncryption = vncSec.CurrentText()
		}
		f.UseSecretService = ussChk.IsChecked()
		oldMacros := f.Macros // до обнуления — как oldVars (не теряем ключи)
		mcells := make([][2]string, 0, macroTable.RowCount())
		for i := 0; i < macroTable.RowCount(); i++ {
			nm, cmd := "", ""
			if it := macroTable.Item(i, 0); it != nil {
				nm = it.Text()
			}
			if it := macroTable.Item(i, 1); it != nil {
				cmd = it.Text()
			}
			mcells = append(mcells, [2]string{nm, cmd})
		}
		f.Macros = mergeMacros(oldMacros, mcells)
		applyTermOptsEdit(env, termOptsEdit{
			UsePersonal:  personal.IsChecked(),
			Font:         tFont.Text(),
			TextColor:    tFg.Text(),
			BackColor:    tBg.Text(),
			CursorShape:  cursor.CurrentText(),
			Scrollback:   tScroll.Text(),
			Transparency: tTransp.Text(),
			WordChars:    tWord.Text(),
			BoldLikeText: boldLike.IsChecked(),
			BoldColor:    tBold.Text(),
		})
		applyForm(env, f)
		save(env)
		saved = true
		d.Accept()
	}

	outer := qt.NewQVBoxLayout2()
	outer.AddWidget(tabs.QWidget)
	dialogButtons(outer, d, i18n.Tr("Editor", "Сохранить"), doSave)
	d.SetLayout(outer.QLayout)
	d.Exec()
	return saved
}

func checkedOrNot(b bool) qt.CheckState {
	if b {
		return qt.Checked
	}
	return qt.Unchecked
}
