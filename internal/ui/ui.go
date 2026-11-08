// Пакет ui: Qt6-обвязка (минимальная плёнка; вся логика — в pure-Go пакетах).
// Виджеты Qt создаются только на главной нити: NewQApplication сам делает
// runtime.LockOSThread (miqt v0.7.0+).
package ui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/embed"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/ipc"
	"github.com/DunkanAidaho/asbru-go/internal/keepass"
	"github.com/DunkanAidaho/asbru-go/internal/method"
	"github.com/DunkanAidaho/asbru-go/internal/notify"
	"github.com/DunkanAidaho/asbru-go/internal/scripts"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	"github.com/DunkanAidaho/asbru-go/internal/tray"
	"github.com/DunkanAidaho/asbru-go/internal/tree"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt/rdpview"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt/vncview"
	qt "github.com/mappu/miqt/qt6"
	"github.com/mappu/miqt/qt6/mainthread"
)

// ErrGUIExit — тихий выход по отмене/неверному паролю GUI-гейта
// (легаси exit 0, без сообщений; M19-ревью).
var ErrGUIExit = errors.New("gui locked")

// текущий конфиг/путь — для операций из меню, которым не передают cfg
// (например, скриншот терминала); выставляются в Run.
var (
	curCfg     *config.Config
	curCfgPath string
)

// traceOn — трассировка бага #4 (alt+tab → схлопывание дерева). Активна только
// при ASBRU_TRACE=1 (иначе инертно); логирует focus/window/splitter и изменения
// левой панели, чтобы поймать момент схлопывания.
var traceOn = os.Getenv("ASBRU_TRACE") != ""

func currentConfig() *config.Config { return curCfg }

// restoreWindowSize — user-тест #1/#2: восстановить прошлое состояние окна
// (размер, позиция, максимизация). Приоритет: отдельный файл состояния
// (ui-state.json, пишется на выходе ВСЕГДА, не зависит от «save on exit»),
// затем — fallback из defaults (window geometry / width/height / maximized).
func restoreWindowSize(w *qt.QMainWindow, d *config.Defaults) {
	if w == nil || d == nil {
		return
	}
	if loadWindowState(w) {
		return
	}
	if s, ok := d.Raw["window geometry"].(string); ok && s != "" {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) > 0 {
			w.RestoreGeometry(b)
			return
		}
	}
	if v, ok := d.Raw["window width"].(int); ok {
		if h, ok2 := d.Raw["window height"].(int); ok2 {
			if v > 0 && h > 0 {
				w.Resize(v, h)
			}
		}
	}
	if rawInt(d, "window maximized") == 1 {
		w.ShowMaximized()
	}
}

// windowStatePath — отдельный файл состояния окна (в каталоге конфига).
// Легаси хранил это в asbru.yml.gui; здесь — свой JSON, независимый от
// «save on exit» (иначе при выключенной настройке геометрия не сохранялась).
func windowStatePath() string {
	if curCfgPath != "" {
		return filepath.Join(filepath.Dir(curCfgPath), "ui-state.json")
	}
	if u, err := os.UserConfigDir(); err == nil {
		return filepath.Join(u, "asbru", "ui-state.json")
	}
	return filepath.Join(os.TempDir(), "asbru-ui-state.json")
}

// saveWindowState — записать геометрию окна в ui-state.json (вызывается на
// выходе ВСЕГДА, независимо от «save on exit»). saveGeometry учитывает
// size+position+maximized. Ошибка записи не роняет приложение.
func saveWindowState() {
	if mainWindow == nil {
		return
	}
	g := mainWindow.SaveGeometry()
	if len(g) == 0 {
		return
	}
	st := struct {
		Geometry string `json:"geometry"`
	}{Geometry: base64.StdEncoding.EncodeToString(g)}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	p := windowStatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o600)
}

// loadWindowState — прочитать и восстановить геометрию из ui-state.json. true —
// если восстановлено (независимо от результата RestoreGeometry).
func loadWindowState(w *qt.QMainWindow) bool {
	if w == nil {
		return false
	}
	data, err := os.ReadFile(windowStatePath())
	if err != nil {
		return false
	}
	var st struct {
		Geometry string `json:"geometry"`
	}
	if json.Unmarshal(data, &st) != nil || st.Geometry == "" {
		return false
	}
	b, err := base64.StdEncoding.DecodeString(st.Geometry)
	if err != nil || len(b) == 0 {
		return false
	}
	return w.RestoreGeometry(b)
}

// persistWindowSize — записать состояние окна в defaults (резервный путь при
// сохранении конфига; основной — saveWindowState на выходе). saveGeometry даёт
// нормальный размер и state; Width()/Height() при развороте дали бы размер
// развёрнутого окна.
func persistWindowSize(cfg *config.Config) {
	if cfg == nil || mainWindow == nil {
		return
	}
	if g := mainWindow.SaveGeometry(); len(g) > 0 {
		rawSet(&cfg.Defaults, "window geometry", base64.StdEncoding.EncodeToString(g))
	}
	if ng := mainWindow.NormalGeometry(); ng != nil && ng.Width() > 0 && ng.Height() > 0 {
		rawSet(&cfg.Defaults, "window width", ng.Width())
		rawSet(&cfg.Defaults, "window height", ng.Height())
	} else {
		rawSet(&cfg.Defaults, "window width", mainWindow.Width())
		rawSet(&cfg.Defaults, "window height", mainWindow.Height())
	}
	rawSet(&cfg.Defaults, "window maximized", boolToInt(mainWindow.IsMaximized()))
}

// screenshotDir — каталог скриншотов (легаси $CFG_DIR/screenshots).
func screenshotDir(cfgPath string) string {
	return filepath.Join(filepath.Dir(cfgPath), "screenshots")
}

// M26: маршрутизация IPC-действий. Сокет слушается рано (до гейта пароля),
// а диспетчер (на Qt-нити) устанавливается после готовности окна. Пришедшие
// раньше сообщения буферизуются и flush-ятся при setIpcDispatch (ревью M26).
var (
	ipcMu       sync.Mutex
	ipcQ        []string
	ipcDispatch atomic.Pointer[func(string)]
)

// ipcRoute — приём IPC-сообщения: в диспетчер, если готов, иначе в буфер.
func ipcRoute(msg string) {
	if fn := ipcDispatch.Load(); fn != nil {
		(*fn)(msg)
		return
	}
	ipcMu.Lock()
	ipcQ = append(ipcQ, msg)
	ipcMu.Unlock()
}

// setIpcDispatch — устанавливает диспетчер (Qt-нить) и flush буфера.
func setIpcDispatch(fn func(string)) {
	ipcDispatch.Store(&fn)
	ipcMu.Lock()
	q := ipcQ
	ipcQ = nil
	ipcMu.Unlock()
	for _, m := range q {
		fn(m)
	}
}

// drainIpcQ — выгрузить буферизованные IPC-сообщения и залогировать их (MINOR-3:
// на пути выхода, когда диспетчер не установился (отказ password-гейта), они иначе
// потерялись молча — делаем видимыми, не «тихими»). Сообщения не исполняются
// (инстанс выходит) — только диагностика.
func drainIpcQ() {
	ipcMu.Lock()
	q := ipcQ
	ipcQ = nil
	ipcMu.Unlock()
	for _, m := range q {
		slog.Warn("ipc: необработанный форвард при выходе (password-гейт)", "msg", m)
	}
}

// splitClusterSuffix — M25: --start-uuid=uuid[:cluster] (легаси asbru-cm:42):
// суффикс после последнего ':' — имя кластера. Девиация: легаси
// PACMain.pm:398 split(':', ...) берёт ПЕРВЫЙ разделитель; у UUID ':' нет,
// различий на практике, но закрепляем поведение последнего ':'.
func splitClusterSuffix(s string) (uuid, cluster string) {
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// termContextMenu — контекстное меню терминала (M28/M36; на Qt-нити).
// Копирование/вставка через QTermWidget copyClipboard/pasteClipboard
// (легаси _vteMenu, PACTerminal.pm:1713+; макросы/команды — расширение).
// M36: подменю «Действия с сессией» (легаси подменю Session/Actions).
func termContextMenu(id int, x, y int) {
	menu := wgt.NewMenu()
	defer menu.Free()
	// M36: подменю действий с сессией (легаси PACTerminal.pm Session-menu).
	acts := menu.Submenu(i18n.Tr("Menu", "Действия с сессией"))
	closeItem := acts.Item(i18n.Tr("Menu", "Закрыть вкладку"))
	closeOtherItem := acts.Item(i18n.Tr("Menu", "Закрыть другие вкладки"))
	closeAllItem := acts.Item(i18n.Tr("Menu", "Закрыть все вкладки"))
	disconnItem := acts.Item(i18n.Tr("Menu", "Отключиться"))
	restartItem := acts.Item(i18n.Tr("Menu", "Перезапустить"))
	dupItem := acts.Item(i18n.Tr("Menu", "Дублировать"))
	titleItem := acts.Item(i18n.Tr("Menu", "Заголовок вкладки…"))
	cmdItem := acts.Item(i18n.Tr("Menu", "Отправить команду..."))
	insValItem := acts.Item(i18n.Tr("Menu", "Вставить значение…"))
	copyPassItem := acts.Item(i18n.Tr("Menu", "Копировать пароль"))
	editItem := acts.Item(i18n.Tr("Menu", "Отредактировать сессию"))
	// M36: «Открыть SFTP-окно» — только для SSH-подобных соединений (легаси
	// PACTerminal.pm:2274-2278 /^.*ssh.*$/ или 'SSH').
	var sftpItem unsafe.Pointer
	if isSSHConn(id) {
		sftpItem = acts.Item(i18n.Tr("Menu", "Открыть SFTP-окно"))
	}
	detachItem := acts.Item(i18n.Tr("Menu", "Отцепить вкладку"))
	// M36: если вкладка уже отцеплена — пункт «Прикрепить обратно» (attach).
	attachItem := acts.Item(i18n.Tr("Menu", "Прикрепить обратно"))
	copyItem := menu.Item(i18n.Tr("Menu", "Копировать"))
	pasteItem := menu.Item(i18n.Tr("Menu", "Вставить"))
	findItem := menu.Item(i18n.Tr("Menu", "Найти…"))
	kbItem := menu.Item(i18n.Tr("Menu", "Показать привязки клавиш"))
	shotItem := menu.Item(i18n.Tr("Menu", "Скриншот терминала"))
	saveLogItem := menu.Item(i18n.Tr("Menu", "Сохранить лог сессии…"))
	// M36: Remote/Local commands подменю (легаси PACTerminal.pm:2020-2075).
	// Динамические пункты собираем в cmdExtra (сбрасывается на каждый клик).
	cmdExtra = nil
	cfg := currentConfig()
	env := commandEnv(id)
	if env != nil {
		// M36-ревью MAJOR: легаси включает и ГЛОБАЛЬНЫЕ defaults.'remote/local
		// commands'. Собираем global + per-conn в один список (defensive copy:
		// не алиасить backing-массив defaults.Raw — M36-ревью NIT).
		remote := append([]map[string]any{}, defaultsCommandList(cfg, "remote commands")...)
		remote = append(remote, env.Macros...)
		if items := commandsOf(remote); len(items) > 0 {
			rsub := menu.Submenu(i18n.Tr("Menu", "Удалённые команды"))
			for _, it := range items {
				txt := it.txt
				p := rsub.Item(it.desc)
				cmdExtra = append(cmdExtra, cmdMenuItem{ptr: p, on: func() {
					tabMu.Lock()
					sess := tabSess[id]
					tabMu.Unlock()
					if sess == nil {
						return
					}
					select {
					case sess.Control <- session.ControlMsg{Kind: session.ControlRunCmd, Cmd: txt}:
					default:
					}
				}})
			}
		}
		local := append([]map[string]any{}, env.LocalConnected...)
		local = append(local, defaultsCommandList(cfg, "local commands")...)
		if items := commandsOf(local); len(items) > 0 {
			lsub := menu.Submenu(i18n.Tr("Menu", "Локальные команды"))
			for _, it := range items {
				txt := it.txt
				p := lsub.Item(it.desc)
				cmdExtra = append(cmdExtra, cmdMenuItem{ptr: p, on: func() { runLocalDetached(txt) }})
			}
		}
	}
	// M36 Split: подменю «Сплит с…» — список других работающих терминалов
	// (только БЕЗОПАСНЫЕ: не split, не detached, не embedded).
	// M36-ревью MINOR: не показывать, если сам `id` уже split/detach/embedded.
	{
		tabMu.Lock()
		_, selfSplit := splitPeer[id]
		_, selfDet := detachTop[id]
		selfEmbed := tabEmbed[id] != EmbedNone
		var otherIDs []int
		for tid := range tabID {
			if tid == id {
				continue
			}
			if _, isSplit := splitPeer[tid]; isSplit {
				continue
			}
			if _, isDet := detachTop[tid]; isDet {
				continue
			}
			if tabEmbed[tid] != EmbedNone {
				continue
			}
			otherIDs = append(otherIDs, tid)
		}
		tabMu.Unlock()
		if !selfSplit && !selfDet && !selfEmbed && len(otherIDs) > 0 {
			ssub := menu.Submenu(i18n.Tr("Menu", "Сплит с…"))
			for _, oid := range otherIDs {
				tabMu.Lock()
				oname := tabName[oid]
				tabMu.Unlock()
				p := ssub.Item(oname)
				fpeer := oid
				cmdExtra = append(cmdExtra, cmdMenuItem{ptr: p, on: func() {
					splitTerm(id, fpeer, false)
				}})
			}
		}
	}
	chosen := menu.Check(x, y)
	// M36: динамические пункты подменю (Remote/Local commands).
	for _, it := range cmdExtra {
		if chosen == it.ptr {
			it.on()
			return
		}
	}
	// M36-ревью: sftpItem может быть nil (не-SSH соединение); `case sftpItem:`
	// при nil совпал бы с nil chosen (отключение меню) и случайно вызвал бы
	// openSFTP — guard вне switch.
	if sftpItem != nil && chosen == sftpItem {
		openSFTP(id)
		return
	}
	switch chosen {
	case closeItem:
		closeTabForID(id)
	case closeOtherItem:
		closeOtherTabs(id)
	case closeAllItem:
		closeAllTabs()
	case disconnItem:
		disconnectTerm(id)
	case restartItem:
		restartTerm(id)
	case dupItem:
		duplicateTerm(id)
	case titleItem:
		setTermTitle(id)
	case cmdItem:
		sendCommandForID(id)
	case insValItem:
		insertValueForID(id)
	case copyPassItem:
		copyPasswordForID(id)
	case editItem:
		editSessionForID(id)
	case detachItem:
		detachTerm(id)
	case attachItem:
		reattachTerm(id) // no-op, если вкладка не отцеплена (detachTop[id]==nil)
	case copyItem:
		wgt.Copy(id)
	case pasteItem:
		wgt.Paste(id)
	case findItem:
		wgt.Find(id)
	case kbItem:
		// M28: привязки клавиш — информативный диалог (легаси
		// ListKeyBindings('terminal')); список — из defaults.keybindings.
		showKeybindings()
	case shotItem:
		captureScreenshot(id)
	case saveLogItem:
		saveSessionLog(id)
	}
}

// commandEnv — окружение соединения вкладки (для Remote/Local commands).
func commandEnv(id int) *config.Environment {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return nil
	}
	cfg := currentConfig()
	if cfg == nil {
		return nil
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return nil
	}
	return &env
}

// insertValueForID — «Вставить значение»: диалог + подстановка масок
// (<V:>/<GV:>/<ENV:>) → запись СЫРЫХ байт в поток (без '\n'; легаси
// «Insert value» шлёт значение как есть). В отличие от «Отправить команду» —
// без перевода строки.
func insertValueForID(id int) {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	val := inputText(mainWindow.QWidget, i18n.Tr("Dialog", "Вставить значение"), i18n.Tr("Dialog", "Значение (маски <V:>/<GV:>/<ENV:>)"), "")
	if val == "" {
		return
	}
	// подстановка масок (<V:>/<GV:>/<ENV:> + local) БЕЗ исполнения <CMD:>
	// (M36-ревью MINOR: «вставить значение» не должен локально шеллить команду).
	res := session.ResolverFromConfig(session.BuildEnv{UUID: uuid}, cfg, &env)
	val = session.SubstituteValues(val, res)
	if val == "" {
		return
	}
	tabMu.Lock()
	ch, ok := tabIn[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- []byte(val):
	default:
		slog.Warn("ввод отброшен: канал сессии переполнен", "value", "insert")
	}
}
func disconnectTerm(id int) {
	tabMu.Lock()
	sess := tabSess[id]
	tabMu.Unlock()
	if sess == nil {
		return
	}
	select {
	case sess.Control <- session.ControlMsg{Kind: session.ControlDisconnect}:
	default:
	}
}

// restartTerm — перезапустить соединение вкладки wgt-id (легаси ctrl('RESTART')).
// Живой сессии шлём ControlRestart. Если сессия уже завершилась (pre-CONNECT
// ошибка оставляет вкладку для чтения причины; обрыв при close-on-disconnect=off
// тоже), обрабатывать сообщение некому — перезапускаем соединение заново:
// убираем зомби-вкладку и открываем новую (легаси restart = close + start).
func restartTerm(id int) {
	tabMu.Lock()
	sess := tabSess[id]
	_, alive := tabID[id]
	tabMu.Unlock()
	if !alive {
		return
	}
	if sess != nil {
		select {
		case sess.Control <- session.ControlMsg{Kind: session.ControlRestart}:
			// A2: перезапуск = снова рукопожатие — жёлтый индикатор, иначе вкладка
			// держала бы прежний красный/зелёный до самого CONNECTED.
			setConnStatus(id, connConnecting, i18n.Tr("Status", "подключение…"))
			return
		default:
			// Сессия ещё числится живой, но канал заполнен — не порождаем второй
			// запуск; мёртвая сессия к этому моменту уже вычищена
			// detachDeadSession, и сюда мы не попадём.
			return
		}
	}
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	removeTab(id)
	launchConnection(uuid, &env, cfg, firstCluster(env.Cluster))
}

// sendCommandForID — отправить команду в сессию вкладки (легаси «Send command»;
// ControlRunCmd пишет команду + '\n' в поток).
func sendCommandForID(id int) {
	tabMu.Lock()
	sess := tabSess[id]
	tabMu.Unlock()
	if sess == nil {
		return
	}
	cmd := inputText(mainWindow.QWidget, i18n.Tr("Dialog", "Отправить команду"), i18n.Tr("Dialog", "Команда"), "")
	if cmd == "" {
		return
	}
	select {
	case sess.Control <- session.ControlMsg{Kind: session.ControlRunCmd, Cmd: cmd}:
	default:
	}
}

// copyPasswordForID — пароль соединения вкладки → буфер обмена (легаси
// PACUtils.pm _copyPass:3866-3874). env.Pass расшифрован при загрузке;
// KeePass-маска <field|uuid> разрешается (иначе плейсхолдер в буфер).
func copyPasswordForID(id int) {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	copyPasswordForUUID(uuid)
}

// copyPasswordForUUID — пароль соединения → буфер обмена (M19/M36; переиспользуется
// терминальным меню по wgt-id и деревом по uuid). Легаси PACUtils.pm _copyPass:
// парольная фраза ключа (publickey) приоритетнее пароля; KeePass-маска
// <field|uuid> разрешается (иначе плейсхолдер в буфер).
func copyPasswordForUUID(uuid string) {
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	// легаси: парольная фраза ключа (publickey) ПРИОРИТЕТНЕЕ пароля.
	pass := env.Pass
	if env.Passphrase != "" {
		pass = env.Passphrase
	}
	if pass == "" || pass == "<<ASK_PASS>>" {
		return
	}
	// KeePass-маска — разрешить через открытый vault; ошибка = не копируем.
	if v := appVault.Load(); v != nil {
		resolved, err := keepass.ReplaceMasks(pass, v)
		if err != nil {
			return
		}
		pass = resolved
	}
	qt.QGuiApplication_Clipboard().SetText(pass)
}

// duplicateTerm — «Дублировать» вкладку: открыть новый терминал того же
// соединения (легаси PACTerminal.pm «Duplicate»). Обрабатывается на Qt-нити.
// М36-ревью MINOR: дубликат — СТАНДАЛОН (cluster=”, как легаси _launchTerminals
// [[uuid]] → $cluster=”), а не тот же PCC-кластер (иначе станет транслянт-членом).
func duplicateTerm(id int) {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	launchConnection(uuid, &env, cfg, "")
}

// setTermTitle — «Заголовок вкладки»: задать название вкладки терминала
// (легаси PACTerminal.pm «Set title»). Промпт + mainTabs.SetTabText + tabName.
func setTermTitle(id int) {
	tabMu.Lock()
	cur := tabName[id]
	wrap, ok := tabID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	nv := inputText(mainWindow.QWidget, i18n.Tr("Dialog", "Заголовок вкладки"), i18n.Tr("Dialog", "Новое название"), cur)
	// M36-ревью MINOR: отклоняем пустое И пробельное (легаси /\s/ PACTerminal:2242).
	if strings.TrimSpace(nv) == "" {
		return // отмена или пусто/пробел — без изменений
	}
	// M36-ревью MINOR: после модального диалога вкладка могла закрыться
	// (сессия завершилась) — перепроверяем tabID под tabMu перед коммитом,
	// иначе восстановили бы stale tabName для удалённой вкладки.
	tabMu.Lock()
	if _, ok := tabID[id]; !ok {
		tabMu.Unlock()
		return
	}
	tabName[id] = nv
	statusLabel := tabStatus[id] // бэклог: refresh строки состояния терминала
	prev := tabConn[id]          // A2: последний статус — чтобы не потерять его текстом имени
	tabMu.Unlock()
	// Qt-нить (menu handler — на Qt-нити): обновляем текст вкладки и label.
	if uiLive.Load() {
		mainthreadW(func() {
			// A2: подсказка собирается из нового имени и ПОСЛЕДНЕГО статуса —
			// иначе после переименования она показывала бы старое имя, а текст
			// состояния терялся бы до следующего события сессии.
			p := presentTab(nv, prev.text, prev.st)
			if idx := mainTabs.IndexOf(wrap); idx >= 0 {
				mainTabs.SetTabText(idx, p.Title)
				mainTabs.SetTabToolTip(idx, p.Tooltip)
			}
			if statusLabel != nil {
				if prev.text != "" {
					statusLabel.SetText(prev.text)
				} else {
					statusLabel.SetText(nv)
				}
			}
		})
	}
}

// isSSHConn — SSH-подобное соединение вкладки? (guard «Открыть SFTP-окно»).
func isSSHConn(id int) bool {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return false
	}
	cfg := currentConfig()
	if cfg == nil {
		return false
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return false
	}
	m := strings.ToLower(env.Method)
	return strings.Contains(m, "ssh") || strings.Contains(m, "sftp") || m == "mosh"
}

// openSFTP — «Открыть SFTP-окно» (легаси PACTerminal.pm _openSFTP:2540):
// клонировать соединение, method='SFTP', очистить expect/options/autossh,
// запустить НОВЫЙ терминал. Только для SSH-подобных (guard в меню).
func openSFTP(id int) {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	// легаси: dclone + method='SFTP', ожидание/опции/autossh — пустые.
	env.Method = "SFTP"
	env.Expect = nil
	env.Options = ""
	env.Autossh = 0
	env.Parent = ""
	launchConnection(uuid, &env, cfg, "")
}

// saveSessionLog — «Сохранить лог сессии…» (легаси _saveSessionLog:3246):
// буфер вывода вкладки → файл по выбору пользователя (опции — убрать ESC-посл.).
func saveSessionLog(id int) {
	path := inputText(mainWindow.QWidget, i18n.Tr("Dialog", "Сохранить лог сессии"), i18n.Tr("Dialog", "Файл лога"), "")
	if path == "" {
		return
	}
	tabMu.Lock()
	var data []byte
	if lb, ok := tabLog[id]; ok {
		data = append([]byte(nil), lb.bytes()...)
	}
	tabMu.Unlock()
	if len(data) == 0 {
		showErr(mainWindow.QWidget, i18n.Tr("Dialog", "Лог сессии пуст"))
		return
	}
	// легаси: опция «убрать ESC-последовательности» — вопрос пользователю.
	strip := showConfirm(mainWindow.QWidget, i18n.Tr("Dialog", "Убрать ESC-последовательности из лога?"))
	if strip {
		data = []byte(removeEscapeSeqs(string(data)))
		if len(data) == 0 {
			showErr(mainWindow.QWidget, i18n.Tr("Dialog", "Лог сессии пуст (только ESC-последовательности)"))
			return
		}
	}
	// M36-ревью MINOR: не перезаписывать существующий файл без подтверждения.
	if _, err := os.Stat(path); err == nil {
		if !showConfirm(mainWindow.QWidget, i18n.Tr("Dialog", "Файл существует. Перезаписать?")) {
			return
		}
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		showErr(mainWindow.QWidget, i18n.Tr("Dialog", "Сохранение лога: ")+err.Error())
		return
	}
	slog.Info("лог сессии сохранён", "path", path, "bytes", len(data))
}

// detachTerm — «Отцепить вкладку»: показать терминал отдельным top-level окном
// (сессия жива дальше; сессия/вкладка остаются в tab-картах для re-attach).
// Только для реального терминала (не embedded RDP/VNC, у которых нет виджета).
func detachTerm(id int) {
	tabMu.Lock()
	wrap, ok := tabID[id]
	name := tabName[id]
	_, isSplit := splitPeer[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	// M36-ревью MAJOR: split-вкладку нельзя отцепить (иначе пустая split-страница
	// + рассинхрон splitPeer/splitTop/detachTop). Логику держит и menu (скрывает
	// «Отцепить»?), но detachTerm — второй guard.
	if isSplit {
		return
	}
	if uiLive.Load() {
		mainthreadW(func() {
			// Защита: не отцепляем уже отцепленную вкладку.
			tabMu.Lock()
			if _, exists := detachTop[id]; exists {
				tabMu.Unlock()
				return
			}
			tabMu.Unlock()

			d := qt.NewQDialog2()
			d.SetWindowTitle(name + " — Ásbrú CM")
			d.SetMinimumSize(qt.NewQSize2(700, 450))
			// M36-ревью MAJOR: НУЖНА layout-геометрия — иначе терминал не
			// ресайзится с окном (standalone-окно теряет смысл). QVBoxLayout →
			// wrap растягивается на всю область окна.
			box := qt.NewQVBoxLayout2()
			box.AddWidget(wrap)
			d.SetLayout(box.QLayout)
			// M36-ревью MAJOR (race): регистрируем detachTop ПОД lock ДО
			// пере-парента — иначе сессия, завершившаяся в этот миг, не нашла бы
			// окно (dw=nil) и оставила бы пустое SHOWN top-level окно. removeTab
			// увидит detachTop[id] и спрячет его.
			tabMu.Lock()
			if _, exists := detachTop[id]; exists {
				tabMu.Unlock()
				return // уже отцеплена
			}
			detachTop[id] = d
			tabMu.Unlock()
			// пере-парент виджета терминала в top-level окно.
			wrap.SetParent(d.QWidget)
			wrap.Show()
			tabMu.Lock()
			_, alive := tabID[id]
			tabMu.Unlock()
			if !alive {
				// сессия закрылась между регистрацией и репартентом — удалить окно.
				tabMu.Lock()
				delete(detachTop, id)
				tabMu.Unlock()
				d.DeleteLater()
				return
			}
			// Пользователь закрыл окно (X/Reject) → вернуть вкладку.
			d.OnRejected(func() { reattachTerm(id) })
			d.Show()
		})
	}
}

// reattachTerm — вернуть отцепленную вкладку в mainTabs (при закрытии окна).
func reattachTerm(id int) {
	if !uiLive.Load() {
		return // M36-ревью NIT: без живой Qt-петли mainthread.Wait завис бы
	}
	mainthreadW(func() {
		tabMu.Lock()
		wrap, ok := tabID[id]
		name := tabName[id]
		prev := tabConn[id] // A2: вернуть состояние/подсказку на новую вкладку
		d := detachTop[id]
		delete(detachTop, id)
		tabMu.Unlock()
		if !ok || d == nil || wrap == nil {
			return
		}
		wrap.SetParent(mainTabs.QWidget)
		wrap.Show()
		mainTabs.AddTab(wrap, name)
		mainTabs.SetCurrentWidget(wrap)
		// A2: вкладка создаётся заново — индикатор и подсказка восстанавливаются
		// из последнего известного состояния (иначе после «Прицепить» индикатора
		// не было бы до следующего события сессии).
		if idx := mainTabs.IndexOf(wrap); idx >= 0 {
			mainTabs.SetTabToolTip(idx, presentTab(name, prev.text, prev.st).Tooltip)
			mainTabs.SetTabIcon(idx, connStateIcon(prev.st))
		}
		// M39-ревью MAJOR-1: фокус — только если страница САМА является канвасом
		// сессии. У терминала страница — контейнер [макросы, терминал, лейбл], и
		// фокус на контейнер оставил бы терминал без ввода (Qt отдаёт клавиши
		// виджету фокуса и вверх по родителям, а не вниз к детям).
		if _, isEmbed := tabEmbed[id]; isEmbed {
			wrap.SetFocus()
		}
		d.DeleteLater() // M36-ревью MINOR: убрать окно-обёртку (не Hide — иначе утечка)
	})
}

// splitTerm — «Сплит»: объединить ДВЕ вкладки в один QSplitter (верт/гориз.).
// Обе сессии живы; split-контейнер — это НОВАЯ вкладка в mainTabs. Записи
// tabID/sess/uuid остаются для обеих; dissolve — по splitPeer/splitTop.
func splitTerm(selfID, otherID int, vertical bool) {
	if selfID == otherID {
		return
	}
	if !uiLive.Load() {
		return
	}
	mainthreadW(func() {
		tabMu.Lock()
		w1, selfOK := tabID[selfID]
		w2, otherOK := tabID[otherID]
		_, selfSplit := splitPeer[selfID]
		_, otherSplit := splitPeer[otherID]
		_, selfDetach := detachTop[selfID]
		_, otherDetach := detachTop[otherID]
		selfEmbed := tabEmbed[selfID] != EmbedNone
		otherEmbed := tabEmbed[otherID] != EmbedNone
		name1, name2 := tabName[selfID], tabName[otherID]
		tabMu.Unlock()
		// M36-ревью MAJOR+NIT: нельзя сплитить с уже split/detach/embedded, и сам.
		if !selfOK || !otherOK || selfSplit || otherSplit || selfDetach || otherDetach || selfEmbed || otherEmbed {
			return
		}
		// M36-ревью MINOR (TOCTOU): w1/w2 могут стать nil между проверкой и
		// использованием (removeTab) — не передаём NULL в QSplitter.
		if w1 == nil || w2 == nil {
			return
		}
		// Убрать исходные вкладки-страницы ДО AddWidget (QSplitter::addWidget
		// reparent'ит виджеты из mainTabs → IndexOf после вернул бы -1, иначе
		// ghost-вкладки). IndexOf по виджету стабилен независимо от смещения.
		for _, w := range []*qt.QWidget{w1, w2} {
			if i := mainTabs.IndexOf(w); i >= 0 {
				mainTabs.RemoveTab(i)
			}
		}
		var qo qt.Orientation
		if vertical {
			qo = qt.Vertical
		} else {
			qo = qt.Horizontal
		}
		sp := qt.NewQSplitter3(qo)
		sp.AddWidget(w1)
		sp.AddWidget(w2)
		sp.SetChildrenCollapsible(false)
		mainTabs.AddTab(sp.QWidget, name1+" + "+name2)
		mainTabs.SetCurrentWidget(sp.QWidget)
		tabMu.Lock()
		splitTop[selfID] = sp
		splitTop[otherID] = sp
		splitPeer[selfID] = otherID
		splitPeer[otherID] = selfID
		tabMu.Unlock()
	})
}

// cmdItem — пункт подменю Remote/Local commands (ptr для сопоставления с Check).
type cmdMenuItem struct {
	ptr unsafe.Pointer
	on  func()
}

// cmdExtra — динамические пункты подменю Remote/Local commands (per клик меню).
// Пакет-scope: терм. меню не имеет entries-среза (использует switch/guard).
var cmdExtra []cmdMenuItem

// cmdOf — читает команды из []map[string]any (ключи 'command'/'txt', 'description'/'desc').
type cmdOf struct {
	txt  string
	desc string
}

// defaultsCommandList — глобальные команды из defaults.Raw (легаси
// defaults.'local/remote commands'; ключи не типизированы в Defaults — Raw).
func defaultsCommandList(cfg *config.Config, key string) []map[string]any {
	if cfg == nil || cfg.Defaults.Raw == nil {
		return nil
	}
	raw, ok := cfg.Defaults.Raw[key]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, e := range v {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func commandsOf(entries []map[string]any) []cmdOf {
	var out []cmdOf
	for _, e := range entries {
		txt, _ := e["command"].(string)
		// M36-ревью MINOR: fallback на 'txt' при пробельной 'command'.
		if strings.TrimSpace(txt) == "" {
			txt, _ = e["txt"].(string)
		}
		if strings.TrimSpace(txt) == "" {
			continue
		}
		desc, _ := e["description"].(string)
		if desc == "" {
			desc = txt
		}
		out = append(out, cmdOf{txt: txt, desc: desc})
	}
	return out
}

// removeEscapeSeqs — вырезает управляющие/ESC-последовательности из лога
// (легаси _removeEscapeSeqs; порт для «Сохранить лог сессии»). Поддерживает
// CSI (ESC [...), OSC (ESC ] ... BEL/ST), 2- и 3-байтные ESC-коды; прочие
// control-байты отбрасывает, '\n'/'\r' сохраняет.
func removeEscapeSeqs(s string) string {
	var out strings.Builder
	b := []byte(s)
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c != 0x1b {
			if c == '\n' || c == '\r' {
				out.WriteByte(c)
			} else if c >= 0x20 {
				out.WriteByte(c)
			}
			continue
		}
		if i+1 >= len(b) {
			break
		}
		switch b[i+1] {
		case '[': // CSI: до final-байта 0x40-0x7e
			j := i + 2
			for j < len(b) && !(b[j] >= 0x40 && b[j] <= 0x7e) {
				j++
			}
			i = j // consume через final (или до конца, если нет final)
		case ']': // OSC: до BEL или ST (ESC \\); неитерминированный — до конца.
			j := i + 2
			i = len(b) - 1 // по умолчанию consumed до конца (нет терминатора)
			for k := j; k < len(b); k++ {
				if b[k] == 0x07 {
					i = k
					break
				}
				if b[k] == 0x1b && k+1 < len(b) && b[k+1] == '\\' {
					i = k + 1
					break
				}
			}
		default: // 2- или 3-байтный ESC-код (ESC [intermediate] final)
			// intermediate (0x20-0x2f) + final (0x30-0x7e); иначе — одиночная пара.
			if i+2 < len(b) && b[i+2] >= 0x30 && b[i+2] <= 0x7e {
				i = i + 2
			} else {
				i = i + 1
			}
		}
	}
	return out.String()
}

// pickSplitMember — активный член split-страницы: тот, где фокус; иначе
// детерминированно меньший id (итерация splitTop неупорядочена).
func pickSplitMember(a, b int, aActive, bActive bool) int {
	switch {
	case aActive && !bActive:
		return a
	case bActive && !aActive:
		return b
	}
	if a < b {
		return a
	}
	return b
}

// splitMemberHasFocus — виджет участника split содержит клавиатурный фокус
// (фокус обычно на самом терминале-канвасе, а не на контейнере-члене).
func splitMemberHasFocus(w, focus *qt.QWidget) bool {
	if w == nil || focus == nil {
		return false
	}
	return w == focus || w.IsAncestorOf(focus)
}

// closeTabForID — закрыть вкладку wgt-id. Вкладка в mainTabs закрывается через
// closeTab (нормальный путь с disconnect+removeTab). Split-член или отцепленная
// вкладка в mainTabs не адресуются (IndexOf=-1): явное закрытие обязано убрать
// их детерминированно — раньше звался только disconnectTerm, а removeTab
// срабатывал лишь при CloseTerminalOnDisconnect (по умолчанию off) → вкладка
// оставалась зомби навсегда.
func closeTabForID(id int) {
	tabMu.Lock()
	wrap, ok := tabID[id]
	sess := tabSess[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	if idx := mainTabs.IndexOf(wrap); idx >= 0 {
		closeTab(idx)
		return
	}
	// Отцепленная/сплит-вкладка: просим сессию отключиться, затем убираем сами.
	if sess != nil {
		select {
		case sess.Control <- session.ControlMsg{Kind: session.ControlDisconnect}:
		default:
		}
	}
	removeTab(id)
}

// closeOtherTabs — закрыть все вкладки, кроме текущей (легаси «Close Terminal/Other»).
func closeOtherTabs(id int) {
	tabMu.Lock()
	ids := make([]int, 0, len(tabID))
	for tid := range tabID {
		if tid != id {
			ids = append(ids, tid)
		}
	}
	tabMu.Unlock()
	for _, tid := range ids {
		closeTabForID(tid)
	}
}

// closeAllTabs — закрыть все вкладки (легаси «Close Terminal/All»).
func closeAllTabs() {
	tabMu.Lock()
	ids := make([]int, 0, len(tabID))
	for tid := range tabID {
		ids = append(ids, tid)
	}
	tabMu.Unlock()
	for _, tid := range ids {
		closeTabForID(tid)
	}
}

// editSessionForID — открыть редактор соединения вкладки wgt-id (легаси
// PACTerminal.pm «Edit session»); на Qt-нити (модальный диалог).
func editSessionForID(id int) {
	tabMu.Lock()
	uuid, ok := tabUUID[id]
	sess := tabSess[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	env, ok := curCfg.Environments[uuid]
	if !ok {
		return
	}
	// M36-ревью MAJOR: терминальное меню «Отредактировать сессию» должно
	// уважать _protected (как tree_menu/тулбар) — иначе защита обходится.
	if env.Protected == 1 {
		showErr(mainWindow.QWidget, i18n.Tr("Tree", "Узел защищён: редактирование запрещено"))
		return
	}
	editConnection(mainWindow.QWidget, &env, uuid, func(updated *config.Environment) {
		if updated == nil { // NIT-защита (ревьюер): иначе s.cfg=nil → openStream(nil)
			return
		}
		curCfg.Environments[uuid] = *updated
		// M36-ревью MAJOR: «Отредактировать сессию» затем «Перезапустить» должен
		// применять правки. Session копирует cfg при старте — обновляем у
		// работающей сессии, иначе restart переподключал бы старый host/port/pass.
		if sess != nil {
			sess.RefreshConfig(updated)
		}
		if err := saveCfgUI(curCfg, curCfgPath, mainWindow.QWidget); err != nil {
			slog.Error("сохранение после редактирования сессии", "err", err)
		}
	})
}

// captureScreenshot — скриншот терминала (wgt-id → QWidget::Grab → сохранение в
// каталог скриншотов + запись в env.Screenshots; легаси PACScreenshots).
func captureScreenshot(id int) {
	// M36-ревью MINOR: tabID/tabUUID читаются под tabMu — иначе data race с
	// removeTab (гонка с сессией-горутиной).
	tabMu.Lock()
	wrap, ok := tabID[id]
	uuid, ok2 := tabUUID[id]
	tabMu.Unlock()
	if !ok {
		return
	}
	if !ok2 {
		return
	}
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	env, ok := cfg.Environments[uuid]
	if !ok {
		return
	}
	name := screenshotName(time.Now(), 0)
	path := filepath.Join(screenshotDir(curCfgPath), name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		slog.Error("скриншот: каталог", "err", err)
		return
	}
	if pm := wrap.Grab(); pm != nil {
		if pm.Save2(path, "PNG") {
			addScreenshot(&env, path)
			cfg.Environments[uuid] = env
			if err := saveCfgUI(cfg, curCfgPath, wrap); err != nil {
				slog.Debug("скриншот: сохранение конфига", "err", err)
			}
			slog.Info("скриншот сохранён", "path", path)
		} else {
			slog.Error("скриншот: не удалось сохранить PNG", "path", path)
		}
	}
}

// showKeybindings — список активных привязок клавиш (M28; легаси
// PACMain FUNCS{_KEYBINDS}->ListKeybindings('terminal')). Batch C: вместо
// заглушки — диалог с таблицей Окно/Действие/Клавиша/Описание.
func showKeybindings() {
	cfg := currentConfig()
	if cfg == nil {
		return
	}
	entries := keybindingsList(cfg)
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Keys", "Привязки клавиш"))
	d.SetMinimumSize(qt.NewQSize2(480, 300))

	table := qt.NewQTableWidget3(0, 4)
	table.SetHorizontalHeaderLabels([]string{i18n.Tr("Keys", "Окно"), i18n.Tr("Keys", "Действие"), i18n.Tr("Keys", "Клавиша"), i18n.Tr("Keys", "Описание")})
	for _, e := range entries {
		row := table.RowCount()
		table.SetRowCount(row + 1)
		table.SetItem(row, 0, qt.NewQTableWidgetItem2(e.Window))
		table.SetItem(row, 1, qt.NewQTableWidgetItem2(e.Action))
		table.SetItem(row, 2, qt.NewQTableWidgetItem2(e.Key))
		table.SetItem(row, 3, qt.NewQTableWidgetItem2(e.Desc))
	}

	close := qt.NewQPushButton3(i18n.Tr("Dialog", "Закрыть"))
	close.OnClicked(d.Reject)
	buttons := qt.NewQHBoxLayout2()
	buttons.AddStretch()
	buttons.AddWidget(close.QWidget)

	box := qt.NewQVBoxLayout2()
	box.AddWidget(table.QWidget)
	box.AddLayout(buttons.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
}

// parseIPC — разбор IPC-сообщения "command|text" (легаси _sendAppMessage:
// hint 'command|text', PACMain.pm:1132; текст может содержать '|').
func parseIPC(msg string) (cmd, text string) {
	if i := strings.IndexByte(msg, '|'); i >= 0 {
		return msg[:i], msg[i+1:]
	}
	return msg, ""
}

// applyIpcMsg — диспетчеризация пришедшего IPC-действия на Qt-нити (M26,
// легаси PACMain.pm:1130-1154). Переиспользует существующие действия.
func applyIpcMsg(cfg *config.Config, cfgPath string, tw *qt.QTreeWidget,
	reload func(), msg string) {
	cmd, text := parseIPC(msg)
	switch cmd {
	case "start-shell":
		env := &config.Environment{Name: "Local Shell", Method: "Local Shell"}
		launchConnection("__PAC_SHELL__", env, cfg, "")
	case "quick-conn":
		quickConnect(tw.QWidget, cfg)
	case "start-uuid":
		uuid, cluster := splitClusterSuffix(text)
		if env, ok := cfg.Environments[uuid]; ok && env.IsGroup != 1 {
			launchConnection(uuid, &env, cfg, cluster)
		} else {
			slog.Warn("IPC: uuid не найден/группа", "uuid", uuid)
		}
	case "edit-uuid":
		if env, ok := cfg.Environments[text]; ok && env.IsGroup != 1 {
			editConnection(tw.QWidget, &env, text, func(updated *config.Environment) {
				cfg.Environments[text] = *updated
				if err := saveCfgUI(cfg, cfgPath, tw.QWidget); err != nil {
					// диалог уже показан (ErrReadOnly) или ошибка в логе
				}
				reload()
			})
		} else {
			slog.Warn("IPC: uuid не найден/группа", "uuid", text)
		}
	case "show-conn":
		// M26: восстановление окна — как из трея: гейт GUI-пароля
		// (легаси PACMain.pm:2214/_showConnectionsList; не минуем защиту).
		if guiPassTrayRequired(&cfg.Defaults) && !guiPassGate(cfg, mainWindow.QWidget, "") {
			return
		}
		mainWindow.Show()
		mainWindow.Raise()
	default:
		slog.Warn("неизвестное IPC-сообщение", "msg", msg)
	}
}

// Startup — стартовые действия (CLI-флаги; M17, M25).
type Startup struct {
	StartUUID   string
	EditUUID    string
	QuickConn   bool
	StartShell  bool
	GUIPassword string // M25: --password (легаси PACMain.pm:245)
	Prefs       bool   // M25: --preferences
	Iconified   bool   // M25: --iconified
	ReadOnly    bool   // M25: --readonly
	NoSplash    bool   // M25: --no-splash (в порте сплэша нет — no-op)
	NoBackup    bool   // M25: --no-backup (порт пишет атомарно — no-op)
	Scripts     bool   // M25: --scripts (окно скриптов — M32)
	StartScript string // M25: --start-script (исполнение — M32)
}

// M25: --readonly — запрет записи конфига (легаси PACMain.pm:301-304).
var readOnly atomic.Bool

// ErrReadOnly — попытка сохранить конфиг в readonly-режиме.
var ErrReadOnly = errors.New("asbru: конфиг в режиме только для чтения (--readonly)")

// saveCfg — сохранение конфига с учётом readonly-режима (M25).
func saveCfg(cfg *config.Config, path string) error {
	if readOnly.Load() {
		return ErrReadOnly
	}
	return cfg.Save(path, config.NewCBC())
}

// saveCfgUI — saveCfg + диалог при ErrReadOnly (ревью M25: в readonly-режиме
// изменения в памяти не должны выглядеть сохранёнными молча).
func saveCfgUI(cfg *config.Config, path string, parent *qt.QWidget) error {
	persistWindowSize(cfg) // user-тест #1: запомнить размер окна при сохранении
	if err := saveCfg(cfg, path); err != nil {
		if errors.Is(err, ErrReadOnly) {
			showErr(parent, i18n.Tr("Dialog", "Конфиг в режиме только для чтения (--readonly): изменения не сохранены."))
		} else {
			slog.Error("сохранение конфига", "err", err)
		}
		return err
	}
	return nil
}

// RunWithStartup — Run + стартовые действия после построения окна.
func RunWithStartup(cfgPath string, start Startup) error {
	return Run(cfgPath, start)
}

// installTranslator — аудит #4 / KDE Tier-1: локализация интерфейса по $LANG.
// Ищет .qm (asbru_<lang>.qm) в каталогах (config-dir/i18n, installed share,
// рядом с бинарником), ставит QTranslator и переводит i18n.Tr на реальный
// Qt QCoreApplication::translate. Без .qm — no-op (интерфейс остаётся на
// исходном тексте). Вызывается после NewQApplication (Qt-нить).
func installTranslator() {
	i18n.SetTranslate(qt.QCoreApplication_Translate)
	lang := i18n.CurrentLang()
	if lang == "" {
		return
	}
	var dirs []string
	if d := os.Getenv("ASBRU_CFG"); d != "" {
		dirs = append(dirs, filepath.Join(d, "i18n"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".config", "asbru", "i18n"))
	}
	dirs = append(dirs, "/usr/share/asbru/i18n", "/usr/share/asbru-cm/i18n")
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "i18n"))
	}
	for _, dir := range dirs {
		path := i18n.QMPath(lang, dir)
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		tr := qt.NewQTranslator()
		if tr.Load(path) {
			qt.QCoreApplication_InstallTranslator(tr)
			slog.Debug("i18n: перевод загружен", "lang", lang, "path", path)
			return
		}
		// .qm присутствует, но не загрузился (повреждён) — идём к след. каталогу.
	}
	slog.Debug("i18n: перевод не найден", "lang", lang)
}

// Run запускает приложение: главное окно с деревом соединений слева и
// пустой правой областью (как в оригинале — никаких автозапускаемых
// шеллов и консолей при старте); подключение — только по клику
// пользователя.
func Run(cfgPath string, startOpts ...Startup) error {
	// Очный дебаг: любая необработанная паника логируется в crash-файл (стек),
	// затем ре-паникуется — поведение сохраняется, но стек доступен без гадания.
	defer crashRecover("run", true)
	qt.NewQApplication(os.Args)
	// Иконка приложения из встроенных PNG: «голый» бинарник показывает логотип
	// asbru без установленного пакета иконок (значок окна/таскбара).
	applyAppIcon()
	// Аудит #4 / KDE Tier-1: локализация — установить QTranslator по $LANG и
	// переключить i18n.Tr на реальный Qt-перевод (после создания QApplication).
	installTranslator()

	var start Startup
	if len(startOpts) > 0 {
		start = startOpts[0]
	}
	readOnly.Store(start.ReadOnly) // M25: --readonly

	cfg, err := loadCfg(cfgPath)
	if err != nil {
		// Отказ от запуска — единственная гарантия, что оригинальный asbru.yml
		// не будет перезаписан пустым конфигом из следующего сохранения.
		slog.Error("загрузка конфига: запуск отменён, файл не перезаписываем", "path", cfgPath, "err", err)
		showErr(nil, i18n.Tr("Dialog", "Не удалось прочитать конфиг: ")+err.Error()+
			i18n.Tr("Dialog", "\nПриложение не запущено, чтобы не перезаписать файл."))
		return err
	}
	curCfg, curCfgPath = cfg, cfgPath // скриншот/операции из меню (№5/№6)

	// M10: накопленная статистика/история переживает перезапуск (легаси
	// PACStatistics readStats): читаем до построения окна (не требует Qt).
	loadStatsHistory(cfgPath)

	// Аудит #8: D-Bus IPC поднимаем рано (до гейта пароля) — вторая копия
	// вызывает org.asbru_cm.Forward и выходит 0; действия буферизуются до
	// готовности UI. Unix-socket удалён (только D-Bus).
	if _, closeFn, err := ipc.Listen(ipc.Name(filepath.Dir(cfgPath)), ipcRoute); err != nil {
		slog.Warn("D-Bus IPC не зарегистрирован: несколько инстансов не смогут форвардить флаги", "err", err)
	} else {
		defer closeFn()
	}

	// M19: пароль GUI (легаси PACMain.pm:243-258) — до построения окна;
	// отмена/неверный пароль = тихий выход, как в оригинале (exit 0).
	// M25: --password подставляется вместо диалога.
	if !guiPassGate(cfg, nil, start.GUIPassword) {
		drainIpcQ() // MINOR-3: не терять подтверждённый форвард молча
		return ErrGUIExit
	}

	uiLive.Store(true) // с этого момента mainthread-Wait исполняется (Qt-петля)
	w := qt.NewQMainWindow(nil)
	mainWindow = w
	w.SetWindowTitle("Ásbrú Connection Manager") // легаси set_title (PACMain.pm:963)
	w.Resize(900, 600)
	restoreWindowSize(w, &cfg.Defaults) // user-тест #1: прошлый размер окна
	// KeePass открывается один раз при старте — не на каждый клик (M2-ревью);
	// ошибка не блокирует приложение, маски остаются литералами.
	sudoShowPassword = cfg.Defaults.SudoShowPassword == 1 // ре-ревью R2
	if k := cfg.Defaults.Keepass; k.UseKeepass == 1 && k.Database != "" {
		if v, err := keepassOpen(k.Database, k.Password, k.KeyFile); err != nil {
			slog.Warn("keepass: хранилище не открыто — маски не разрешаются", "err", err)
		} else {
			appVault.Store(v)
		}
	}

	nodes, _ := tree.Build(cfg.Environments)
	tw := qt.NewQTreeWidget2()
	tw.SetHeaderLabel(i18n.Tr("Tree", "Соединения"))
	byItem := fillTree(tw, nodes)

	// двойной клик → ПОДКЛЮЧИТЬ соединение; редактирование — кнопкой.
	tw.OnItemDoubleClicked(func(item *qt.QTreeWidgetItem, column int) {
		uuid, ok := byItem[itemID(item)]
		if !ok {
			return
		}
		env := cfg.Environments[uuid]
		if env.IsGroup == 1 {
			// бэклог: двойной клик по группе = запустить все её подключения
			// (легаси PACMain.pm; см. config.GroupConnections).
			for _, u := range cfg.GroupConnections(uuid) {
				if e, okc := cfg.Environments[u]; okc {
					launchConnection(u, &e, cfg, firstCluster(e.Cluster))
				}
			}
			return
		}
		launchConnection(uuid, &env, cfg, firstCluster(env.Cluster))
	})

	// reload — перестроение дерева после изменения конфигурации.
	reload := func() {
		if traceOn {
			slog.Info("trace reload (дерево перестраивается)")
		}
		// очный дебаг: сохранить раскрытые узлы до перестройки — иначе после
		// редактирования/сохранения дерево схлопывалось (состояние терялось).
		expanded := captureExpanded(tw, byItem)
		tw.Clear()
		byItem = fillTree(tw, rebuildNodes(cfg))
		restoreExpanded(tw, byItem, expanded)
	}

	// M15: drag'n'drop узлов (P0-5): InternalMove; после дропа порядок
	// элементов QTreeWidget согласуется с конфигом и сохраняется.
	tw.SetDragEnabled(true)
	tw.SetDragDropMode(qt.QAbstractItemView__InternalMove)
	tw.SetDefaultDropAction(qt.MoveAction)
	tw.OnDropEvent(func(super func(event *qt.QDropEvent), event *qt.QDropEvent) {
		// M15-ре-ревью H1: QTreeWidget::dropEvent защищает только от дропа
		// «на себя»; дроп ГРУППЫ в её собственного потомка отцепляет всё
		// поддерево от root (взаимный цикл вне дерева) → поддерево исчезает
		// из UI, reconcile схлопывает иерархию и записывает на диск.
		// Отсекаем ДО super: событие остаётся не принятым, движения нет.
		// (Известная экзотика за пределами guard: dropOn=false уводит хвост
		// в QAbstractItemView::dropMimeData с клонами items — byItem не
		// найдёт перенос → flatten; редко, не чиним.)
		if dropInsideDraggedSubtree(tw, event) {
			return
		}
		super(event) // после super элементы переставлены Qt
		// сохраняем иерархию БЕЗ reload: InternalMove перевешивает те же
		// элементы (byItem валиден), а Clear() внутри активного drop-стека
		// терял бы развёрнутость и откатывал имя-сорт (P2-ревью M15)
		reconcileOrder(cfg, orderFromTree(tw, byItem))
		if err := saveCfgUI(cfg, cfgPath, tw.QWidget); err != nil {
			// диалог уже показан (ErrReadOnly) или ошибка в логе
		}
	})

	// M14: контекстное меню дерева (P0-3 аудита): правый клик.
	cfgPtr := cfg
	tw.SetContextMenuPolicy(qt.CustomContextMenu)
	tw.OnCustomContextMenuRequested(func(pos *qt.QPoint) {
		item := tw.ItemAt(pos)
		uuid := ""
		if item != nil {
			uuid = byItem[itemID(item)]
		}
		global := tw.MapToGlobalWithQPoint(pos)
		showTreeMenu(tw, &cfgPtr, cfgPath, reload, uuid, global.X(), global.Y(),
			byItem, func(u string, e *config.Environment, cl string) {
				launchConnection(u, e, cfgPtr, cl)
			})
	})

	// слева: группированная боковая панель Connections/Favourites/History/Clusters
	// (#1) вместо одиночного дерева; справа: серая область под будущие вкладки.
	buttons := buildTreeButtons(tw, &cfgPtr, &byItem, cfgPath, reload)
	left := buildSidePanel(cfg, tw, buttons, appHistory, func() []string { return clusters.clusters() },
		func(uuid string) {
			env, ok := cfg.Environments[uuid]
			if !ok || env.IsGroup == 1 {
				return
			}
			launchConnection(uuid, &env, cfg, firstCluster(env.Cluster))
		},
		func(name string) {
			for uuid, env := range cfg.Environments {
				if env.IsGroup != 1 && clusterHas(env.Cluster, name) {
					e := env
					launchConnection(uuid, &e, cfg, name)
				}
			}
		})

	// M39-fullscreen: ссылка на боковую панель нужна полноэкранному режиму
	// встроенного RDP/VNC (спрятать/вернуть chrome, не трогая layout вкладок).
	sidePanel = left

	// M12: вкладки терминалов — правая область окна (легаси notebook);
	// пустые вкладки при старте — как пустое поле в оригинале.
	mainTabs = qt.NewQTabWidget2()
	mainTabs.SetTabsClosable(true)
	mainTabs.SetMovable(true)
	// Batch C «last» (Ctrl+Tab): переключение на ПОСЛЕДНЮЮФОКУСИРОВАННУЮ вкладку
	// (MRU-toggle, как легаси _PREVTAB PACMain.pm:2263/2324), а не на правую.
	var curTabIdx, lastTabIdx int
	mainTabs.OnCurrentChanged(func(index int) {
		lastTabIdx = curTabIdx
		curTabIdx = index
		// M39-fullscreen: режим привязан к КОНКРЕТНОЙ вкладке. Если текущей
		// стала другая (IPC/API, добавление вкладки), полноэкранное окно
		// показывало бы чужой виджет, а Esc уходил бы не в канвас — выходим.
		if id := embedFullID.Load(); id != 0 {
			tabMu.Lock()
			full := tabID[int(id)]
			tabMu.Unlock()
			if full == nil || mainTabs.IndexOf(full) != index {
				exitEmbedFullscreen()
			}
		}
	})
	// M34/пользователь: «обрамление/полоска фоновой подкладки» над подключением —
	// это фоновая полоса QTabWidget за вкладками. DocumentMode делает панель
	// вкладок компактной (без этой подкладки), ближе к легаси GtkNotebook.
	mainTabs.SetDocumentMode(true)
	// user-тест #1: вкладки копятся «от середины», а не от левого края —
	// отключаем расширение tab-bar (иначе вкладки растягиваются/центрируются).
	mainTabs.TabBar().SetExpanding(false)
	mainTabs.OnTabCloseRequested(func(index int) {
		closeTab(index) // крестик вкладки → disconnect + удаление
	})

	// QSplitter — аналог легаси HPaned (PACMain.pm:471-495): левая панель
	// узкая и фиксированная (~200-240px), терминалы растягиваются справа
	// (дизайн-аудит P1: веса 1:2 раздували дерево до ~630px на 1920э)
	left.SetMinimumWidth(200)
	left.SetMaximumWidth(400)
	split := qt.NewQSplitter3(qt.Horizontal)
	split.SetChildrenCollapsible(false)
	split.AddWidget(left)
	split.AddWidget(mainTabs.QWidget)
	split.SetStretchFactor(0, 0)
	split.SetStretchFactor(1, 1)
	split.SetSizes([]int{240, 660})
	mainSplit = split // M39-fullscreen: вернуть ширину панелей после выхода
	w.SetCentralWidget(split.QWidget)

	// ТРАССИРОВКА (M34-диагностика бага #4): схлопывание дерева при alt+tab
	// после ошибки. Активна только при ASBRU_TRACE=1 (иначе инертно). Логируем
	// windowActivate/Deactivate/Focus + splitterMoved + ширину левой панели и
	// размеры сплиттера — чтобы поймать МОМЕНТ схлопывания.
	if traceOn {
		split.OnSplitterMoved(func(pos, index int) {
			slog.Info("trace splitterMoved", "pos", pos, "idx", index,
				"leftW", left.Width(), "sizes", split.Sizes())
		})
		w.OnEvent(func(super func(event *qt.QEvent) bool, event *qt.QEvent) bool {
			switch event.Type() {
			case qt.QEvent__WindowActivate, qt.QEvent__WindowDeactivate,
				qt.QEvent__FocusIn, qt.QEvent__FocusOut, qt.QEvent__Show, qt.QEvent__Hide:
				slog.Info("trace winFocus",
					"type", int32(event.Type()),
					"leftW", left.Width(), "leftVisible", left.IsVisible(),
					"winW", w.Width(), "winH", w.Height(),
					"sizes", split.Sizes())
			}
			return super(event)
		})
	}

	// M21: горячие клавиши (легаси PACKeyBindings.pm): bindings из defaults
	// + встроенные Alt+1..9. Actions — на Qt-нити (QShortcut активируется там).
	// Batch A (дерево): хоткеи поверх выбранного узла. save/selUUID — общие.
	save := func() {
		if err := saveCfgUI(cfg, cfgPath, tw.QWidget); err != nil {
			// диалог уже показан (ErrReadOnly) или ошибка в логе
		}
		reload()
	}
	selUUID := func() (string, bool) {
		item := tw.CurrentItem()
		if item == nil {
			return "", false
		}
		uuid, ok := byItem[itemID(item)]
		if !ok {
			return "", false
		}
		if _, ok := cfg.Environments[uuid]; !ok { // guard: byItem ⊆ cfg.Environments (M5)
			return "", false
		}
		return uuid, true
	}
	curTab := func() (int, bool) { // бэклог Batch B: id текущей вкладки терминала
		idx := mainTabs.CurrentIndex()
		if idx < 0 {
			return 0, false
		}
		focus := qt.QApplication_FocusWidget()
		tabMu.Lock()
		defer tabMu.Unlock()
		for id, wrap := range tabID {
			if mainTabs.IndexOf(wrap) == idx {
				return id, true
			}
		}
		// M36 Split: страница mainTabs — QSplitter, а не wrap члена, поэтому
		// IndexOf(wrap) = -1 и все «текущие» хоткеи молча не работали. Резолвим
		// split-страницу в её активный член (по фокусу).
		for id, sp := range splitTop {
			if sp == nil || mainTabs.IndexOf(sp.QWidget) != idx {
				continue
			}
			peer, ok := splitPeer[id]
			if !ok || peer == id {
				return id, true
			}
			return pickSplitMember(id, peer,
				splitMemberHasFocus(tabID[id], focus),
				splitMemberHasFocus(tabID[peer], focus)), true
		}
		return 0, false
	}
	hotkeyActions := map[string]func(){
		"showconnections": func() {
			if traceOn {
				slog.Info("trace showconnections (показ/скрытие дерева)", "beforeLeftW", left.Width(), "beforeVisible", left.IsVisible())
			}
			left.SetVisible(!left.IsVisible()) // легаси Alt+n showconnections
		},
		"edit_node": func() {
			item := tw.CurrentItem()
			uuid, ok := byItem[itemID(item)]
			if !ok {
				return
			}
			env := cfg.Environments[uuid]
			if env.IsGroup == 1 {
				return
			}
			editConnection(tw.QWidget, &env, uuid, func(updated *config.Environment) {
				cfg.Environments[uuid] = *updated
				if err := saveCfgUI(cfg, cfgPath, tw.QWidget); err != nil {
					// диалог уже показан (ErrReadOnly) или ошибка в логе
				}
				reload() // ревью M21 P2: метка дерева не устаревает (как кнопка/--edit-uuid)
			})
		},
		"connect_node": func() {
			item := tw.CurrentItem()
			uuid, ok := byItem[itemID(item)]
			if !ok {
				return
			}
			env := cfg.Environments[uuid]
			if env.IsGroup == 1 {
				return
			}
			launchConnection(uuid, &env, cfg, firstCluster(env.Cluster))
		},
		// Batch A (дерево).
		"rename": func() {
			uuid, ok := selUUID()
			if !ok {
				return
			}
			e := cfg.Environments[uuid]
			if isProtected(&e) {
				showErr(tw.QWidget, i18n.Tr("Tree", "Узел защищён: переименование запрещено"))
				return
			}
			if newName := inputText(tw.QWidget, e.Name, i18n.Tr("Tree", "Новое имя"), e.Name); newName != "" {
				e.Name = newName
				cfg.Environments[uuid] = e
				save()
			}
		},
		"clone": func() {
			uuid, ok := selUUID()
			if !ok {
				return
			}
			if _, err := cloneEnv(cfg, uuid); err != nil {
				slog.Error("клонирование", "err", err)
				return
			}
			save()
		},
		"protection": func() {
			uuid, ok := selUUID()
			if !ok {
				return
			}
			toggleProtect(cfg, uuid, save)
		},
		"start": func() {
			uuid, ok := selUUID()
			if !ok {
				return
			}
			e := cfg.Environments[uuid]
			if e.IsGroup == 1 {
				for _, u := range cfg.GroupConnections(uuid) {
					if ev, okc := cfg.Environments[u]; okc {
						launchConnection(u, &ev, cfg, firstCluster(ev.Cluster))
					}
				}
				return
			}
			launchConnection(uuid, &e, cfg, firstCluster(e.Cluster))
		},
		"expand_all":  func() { tw.ExpandAll() },
		"collaps_all": func() { tw.CollapseAll() },
		// Batch B (терминал): по текущей вкладке/сессии.
		"close": func() {
			if id, ok := curTab(); ok {
				closeTabForID(id)
			}
		},
		"closealltabs": func() { closeAllTabs() },
		"disconnect": func() {
			if id, ok := curTab(); ok {
				disconnectTerm(id)
			}
		},
		"duplicate": func() {
			if id, ok := curTab(); ok {
				duplicateTerm(id)
			}
		},
		"restart": func() {
			if id, ok := curTab(); ok {
				restartTerm(id)
			}
		},
		"hostname": func() {
			if id, ok := curTab(); ok {
				setTermTitle(id)
			}
		},
		"sftp": func() {
			// бэклог Batch B: SFTP только для SSH-вкладки (как меню; открытие SFTP
			// по не-SSH хосту дало бы мусорное подключение — ревью M1).
			if id, ok := curTab(); ok && isSSHConn(id) {
				openSFTP(id)
			}
		},
		"find": func() {
			if id, ok := curTab(); ok {
				wgt.Find(id)
			}
		},
		"paste": func() {
			if id, ok := curTab(); ok {
				wgt.Paste(id)
			}
		},
		"copy": func() {
			if id, ok := curTab(); ok {
				wgt.Copy(id)
			}
		},
		// Batch C (глобальные; легаси PACKeyBindings.pm:494-522).
		"quit": func() {
			quitting.Store(true) // минуем close-to-tray (иначе «Выход» спрячет в трей)
			mainWindow.Close()   // quitOnLastWindowClosed → выход из Ásbrú
		},
		"fullscreen": func() {
			// M39-fullscreen: для встроенной RDP/VNC-вкладки хоткей управляет
			// полноэкранным режимом КАНВАСА (тот же, что Esc-выход), а не всем
			// окном: иначе «полный экран» показывал бы прячущуюся боковую панель.
			if id, ok := curTab(); ok {
				tabMu.Lock()
				kind := tabEmbed[id]
				tabMu.Unlock()
				if kind == EmbedRDP || kind == EmbedVNC {
					toggleEmbedFullscreen(id)
					return
				}
			}
			if mainWindow.IsFullScreen() {
				mainWindow.ShowNormal()
			} else {
				mainWindow.ShowFullScreen()
			}
		},
		"next": func() {
			n := mainTabs.Count()
			if n > 0 && mainTabs.CurrentIndex() < n-1 {
				mainTabs.SetCurrentIndex(mainTabs.CurrentIndex() + 1)
			}
		},
		"previous": func() {
			if mainTabs.CurrentIndex() > 0 {
				mainTabs.SetCurrentIndex(mainTabs.CurrentIndex() - 1)
			}
		},
		"last": func() {
			if n := mainTabs.Count(); n > 0 && lastTabIdx < n {
				mainTabs.SetCurrentIndex(lastTabIdx)
			}
		},
		"infotab": func() {
			// «Show the Info tab» — в Go-порте нет отдельного Info-таба в панели;
			// инфо целевого соединения открывается диалогом (info.go). Приоритет —
			// активная вкладка терминала (как легаси focused-terminal), иначе
			// выделение в дереве.
			var env *config.Environment
			if id, ok := curTab(); ok {
				tabMu.Lock()
				u, ok2 := tabUUID[id] // ревью Batch C: чтение под tabMu (data race с removeTab)
				tabMu.Unlock()
				if ok2 {
					if e, ok3 := cfg.Environments[u]; ok3 {
						env = &e
					}
				}
			}
			if env == nil {
				if uuid, ok := selUUID(); ok {
					if e, ok2 := cfg.Environments[uuid]; ok2 {
						env = &e
					}
				}
			}
			if env != nil && env.IsGroup != 1 {
				showInfo(env, mainWindow.QWidget)
			}
		},
		"localshell": func() {
			env := &config.Environment{Name: "Local Shell", Method: "Local Shell"}
			launchConnection("__PAC_SHELL__", env, cfg, "")
		},
		"showkeybindings": func() { showKeybindings() },
	}
	installHotkeys(w, cfg, hotkeyActions)
	// Правка привязок в редакторе должна пересобирать QShortcut сразу —
	// installHotkeys вызывается здесь один раз при старте.
	hotkeyReinstall = func() { installHotkeys(w, cfg, hotkeyActions) }

	// M25: --iconified — старт в трей без показа окна (легаси
	// PACMain.pm:236-239: _CMDLINETRAY); если трея нет — показываем окно,
	// иначе приложение было бы недостижимо. M29: уважаем defaults 'start iconified'.
	if !start.Iconified && !startIconifiedDefault(&cfg.Defaults) {
		w.Show()
	}

	applyStartup(cfg, cfgPath, tw, reload, startOpts...) // M17: --start-uuid/--edit-uuid/...

	// M18: SNI-трей (P1-11); без реестра Plasma — warn, GUI живёт без трея.
	// бэклог «tray-подменю»: подменю «Группы» — запуск группы из трея.
	groupMenu := make([]tray.LayoutItem, 0)
	for gid, g := range cfg.Environments {
		if g.IsGroup != 1 {
			continue
		}
		gid, g := gid, g
		groupMenu = append(groupMenu, tray.LayoutItem{
			Label: g.Name,
			On: func() {
				mainthreadW(func() {
					for _, u := range cfg.GroupConnections(gid) {
						if e, okc := cfg.Environments[u]; okc {
							launchConnection(u, &e, cfg, firstCluster(e.Cluster))
						}
					}
				})
			},
		})
	}
	sort.Slice(groupMenu, func(a, b int) bool { return groupMenu[a].Label < groupMenu[b].Label })
	trayItems := []tray.LayoutItem{
		{Label: i18n.Tr("Tray", "Показать"), On: func() {
			mainthreadW(func() {
				// M19-ревью: use gui password tray — гейт восстановления
				// из трея (легаси PACMain.pm:2214/2347).
				if guiPassTrayRequired(&cfg.Defaults) && !guiPassGate(cfg, mainWindow.QWidget, "") {
					return
				}
				mainWindow.Show()
				mainWindow.Raise()
			})
		}},
		{Label: i18n.Tr("Tray", "Выход"), On: func() {
			quitting.Store(true)                       // минуем close-to-tray (иначе «Выход» спрячет в трей)
			mainthreadW(func() { mainWindow.Close() }) // quitOnLastWindowClosed
		}},
	}
	if len(groupMenu) > 0 {
		trayItems = append(trayItems, tray.LayoutItem{Label: i18n.Tr("Tray", "Группы"), Submenu: groupMenu})
	}
	var trayOK bool
	if tr, err := tray.Start(trayItems); err != nil {
		slog.Info("трей не зарегистрирован (нет Plasma-реестра?)", "err", err)
		if start.Iconified || startIconifiedDefault(&cfg.Defaults) {
			w.Show() // M25/M29: без трея скрытое окно недостижимо — показываем
		}
	} else {
		trayOK = true
		slog.Debug("SNI-трей зарегистрирован", "name", tr.Name())
	}
	// M29: поведение закрытия главного окна (close-to-tray / confirm-exit /
	// save-on-exit). Вызывается после tray.Start (нужен trayOK).
	wireWindowClose(w, cfg, cfgPath, trayOK)

	// M26: окно готово — диспетчеризуем IPC-действия (устанавливаем
	// обратный вызов; ранние сообщения из буфера flush-ятся).
	// M26-ревью: НЕ блокируем ответ D-Bus на завершение обработки (напр.
	// edit-uuid открывает модальный диалог — клиент ждал бы закрытия и мог
	// упасть по таймауту 3s). Контракт клиента — ДОСТАВКА, не выполнение:
	// mainthread.Start кладёт замыкание в Qt-очередь и возвращается сразу,
	// Forward отвечает мгновенно, клиент выходит 0. Буфер ipcQ покрывает
	// окно до setIpcDispatch; порядок сообщений в Qt-очереди сохраняется.
	setIpcDispatch(func(msg string) {
		mainthread.Start(func() { applyIpcMsg(cfg, cfgPath, tw, reload, msg) })
	})

	qt.QApplication_Exec()
	saveStatsHistory(cfgPath) // M10: персист на выходе (легаси saveStats)
	return nil
}

// applyStartup — стартовые действия CLI-флагов (M17; Qt-нить).
// reload — замыкание из Run (перестраивает дерево И переустанавливает
// byItem — иначе после сохранения остаётся старая map на удалённые
// item'ы: двойной клик/меню мертвы до рестарта; ревью M17 H2).
func applyStartup(cfg *config.Config, cfgPath string, tw *qt.QTreeWidget,
	reload func(), startOpts ...Startup) {
	var start Startup
	if len(startOpts) > 0 {
		start = startOpts[0]
	}
	if start.StartShell {
		env := &config.Environment{Name: "Local Shell", Method: "Local Shell"}
		launchConnection("__PAC_SHELL__", env, cfg, "")
	}
	if start.StartUUID != "" {
		// M25: кластер-суффикс :cluster (легаси asbru-cm:42)
		uuid, cluster := splitClusterSuffix(start.StartUUID)
		if env, ok := cfg.Environments[uuid]; ok && env.IsGroup != 1 {
			launchConnection(uuid, &env, cfg, cluster)
		} else {
			slog.Warn("стартовое действие не выполнено: uuid не найден/группа", "uuid", uuid)
		}
	}
	if start.QuickConn {
		quickConnect(tw.QWidget, cfg)
	}
	// M25: --preferences — открыть Preferences при старте (легаси PACMain)
	if start.Prefs {
		showPreferences(tw.QWidget, cfg, cfgPath)
	}
	// M25/M32: --start-script — выполнить пользовательский скрипт по имени
	// (scripts.Lookup + Run); --scripts — окно скриптов (GUI-задел M32).
	if start.StartScript != "" {
		// cfgPath — это asbru.yml; каталог скриптов — рядом с ним (ре-ревью M32).
		sdir := scripts.ScriptsDir(filepath.Dir(cfgPath))
		if path, ok := scripts.Lookup(sdir, start.StartScript); ok {
			// M32-ревью #1: НЕ блокировать Qt-главный поток — запускаем в
			// горутине с таймаутом (иначе зависший скрипт замораживает старт).
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				if out, err := scripts.Run(ctx, path, nil); err != nil {
					slog.Error("start-script: ошибка выполнения", "script", start.StartScript, "err", err, "out", out)
				} else {
					slog.Info("start-script: выполнен", "script", start.StartScript, "out", out)
				}
			}()
		} else {
			slog.Warn("start-script: скрипт не найден", "script", start.StartScript, "dir", sdir)
		}
	}
	if start.Scripts {
		// M32 PACScripts GUI: окно пользовательских скриптов (список + запуск + вывод).
		mainthreadW(func() { showScriptsWindow(cfg, cfgPath, mainWindow.QWidget) })
	}
	if start.EditUUID != "" {
		env, ok := cfg.Environments[start.EditUUID]
		if !ok {
			slog.Warn("стартовое действие не выполнено: uuid не найден", "uuid", start.EditUUID)
		} else if env.IsGroup == 1 {
			slog.Warn("редактирование группы через --edit-uuid не поддерживается", "uuid", start.EditUUID)
		} else {
			editConnection(tw.QWidget, &env, start.EditUUID, func(updated *config.Environment) {
				cfg.Environments[start.EditUUID] = *updated
				if err := saveCfgUI(cfg, cfgPath, tw.QWidget); err != nil {
					// диалог уже показан (ErrReadOnly) или ошибка в логе
				}
				reload() // P2-1: дерево + byItem (иначе UI-операции мертвы)
			})
		}
	}
}

// quickConnect — быстрый запуск SSH-соединения по хосту (легаси
// __PAC__QUICK__CONNECT__). В конфиг НЕ сохраняется — эфемерное соединение.
func quickConnect(parent *qt.QWidget, cfg *config.Config) {
	// L2-5-ревью: [user@]host[:port] разделяется; ipv4-форма (ipv6-литералы
	// с ':' — известное ограничение, задокументировано)
	host := strings.TrimSpace(inputText(parent, i18n.Tr("Dialog", "Quick Connect"), i18n.Tr("Dialog", "Хост"), ""))
	if host == "" {
		return
	}
	user, port := "", ""
	if i := strings.LastIndex(host, "@"); i >= 0 {
		user, host = host[:i], host[i+1:]
	}
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host, port = host[:i], host[i+1:]
	}
	if host == "" {
		return
	}
	env := &config.Environment{Name: host, IP: host, User: user, Port: port, Method: "SSH"}
	launchConnection(newUUID(), env, cfg, "") // неперсистентно: эфемерное соединение
}

// escapeOSC — убирает управляющие символы из названия (OSC-титул).
func escapeOSC(title string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, title)
}

// unsplitOnClose — при закрытии одной из split-вкладок вернуть НАПАРНИКА в
// отдельную вкладку (динамическая split-вкладка не может жить одна).
func unsplitOnClose(id int, sp *qt.QSplitter) {
	mainthreadW(func() {
		tabMu.Lock()
		peer, ok := splitPeer[id]
		spc := splitTop[id]
		if spc == nil {
			spc = sp
		}
		delete(splitTop, id)
		delete(splitTop, peer)
		delete(splitPeer, id)
		delete(splitPeer, peer)
		w2, o2 := tabID[peer]
		n2 := tabName[peer]
		tabMu.Unlock()
		// M36-ревью MAJOR: ВСЕГДА убрать объединённую «A + B» вкладку (splitter —
		// страница mainTabs), даже если peer уже удалён (!ok) — иначе ghost-вкладка.
		if spc != nil {
			if i := mainTabs.IndexOf(spc.QWidget); i >= 0 {
				mainTabs.RemoveTab(i)
			}
			spc.DeleteLater()
		}
		if !ok {
			return
		}
		if o2 && w2 != nil {
			w2.SetParent(mainTabs.QWidget)
			w2.Show()
			mainTabs.AddTab(w2, n2)
			mainTabs.SetCurrentWidget(w2)
		}
	})
}

// removeTab — удаление вкладки по wgt-id (без вкладки Destroy не вызываем:
// Destroy уже убирает виджет; Qt-нить).
func removeTab(id int) {
	if traceOn {
		slog.Info("trace removeTab (вкладка закрывается)", "id", id)
	}
	tabMu.Lock()
	wrap, ok := tabID[id]
	if !ok {
		tabMu.Unlock()
		return
	}
	kind := tabEmbed[id]
	delete(tabID, id)
	delete(tabSess, id)
	delete(tabUUID, id)
	delete(tabEmbed, id)
	delete(tabName, id)
	delete(tabStatus, id) // бэклог: label строки состояния терминала
	delete(tabConn, id)   // A2: последнее состояние/текст статуса
	delete(tabLog, id)
	tn := tabTunnel[id]
	delete(tabTunnel, id)
	if ch, ok := tabIn[id]; ok {
		delete(tabIn, id)
		if clu := tabClu[id]; clu != "" {
			clusters.remove(clu, ch)
		}
	}
	delete(tabClu, id)
	delete(tabEmbedDone, id) // M37
	dw := detachTop[id]
	delete(detachTop, id)
	_, split := splitPeer[id]
	sp := splitTop[id]
	tabMu.Unlock()
	// бэклог: убить живой туннель RDP/VNC (вне tabMu — может блокироваться на
	// ssh -O exit / сигнале; не держим мьютекс).
	if tn != nil {
		if err := tn.Kill(); err != nil {
			slog.Warn("туннель: Kill", "id", id, "err", err)
		}
	}
	if uiLive.Load() {
		mainthreadW(func() {
			// M39-fullscreen: закрывается развёрнутая вкладка — сперва вернуть
			// обычную раскладку (иначе окно осталось бы fullscreen с пустым
			// экраном после удаления канваса).
			if embedFullID.Load() == int64(id) {
				exitEmbedFullscreen()
			}
			if split {
				unsplitOnClose(id, sp) // вернуть напарника в отдельную вкладку
			}
			if dw != nil {
				dw.DeleteLater() // отцепленная вкладка закрывается — убрать окно
			}
			if i := mainTabs.IndexOf(wrap); i >= 0 {
				mainTabs.RemoveTab(i)
			}
			if kind == EmbedNone {
				wgtDestroy(id) // контракт: Qt-нить (ревью C1c)
			} else {
				destroyEmbedFn(id, kind) // M33: встроенный RDP/VNC — stop+join+deleteLater
			}
			// бэклог: RemoveTab не удаляет страницу — убрать контейнер (терминал +
			// панель макросов + label), иначе утечка QWidget на каждую закрытую
			// вкладку. M28/#4-ревью: контейнер создаётся всегда при uiLive (и при
			// выключенном статус-баре), поэтому убираем его для всех EmbedNone —
			// а не только при наличии label (раньше текла при statusbar off).
			if kind == EmbedNone {
				wrap.DeleteLater()
			}
		})
	} else {
		if kind == EmbedNone {
			wgtDestroy(id) // тесты без Qt-петли: Destroy напрямую
		} else {
			destroyEmbedFn(id, kind)
		}
	}
}

// detachDeadSession — сессия вкладки завершилась, но вкладку оставляем жить
// (ошибка ДО CONNECTED / обрыв при 'close terminal on disconnect'=off). Убираем
// мёртвую Session из карт и из PCC-кластера: иначе Restart — no-op (сообщение
// некому читать), ввод уходит в никуда, а count(cluster) завышен. tabID/tabUUID/
// tabName не трогаем — пользователь читает причину и может перезапустить.
// Вызывается из goroutine сессии — все карты строго под tabMu.
func detachDeadSession(id int) {
	tabMu.Lock()
	delete(tabSess, id)
	if ch, ok := tabIn[id]; ok {
		delete(tabIn, id)
		if clu := tabClu[id]; clu != "" {
			clusters.remove(clu, ch)
		}
	}
	delete(tabClu, id)
	tabMu.Unlock()
}

// destroyEmbed — убирает встроенный RDP/VNC-канвас (Qt-нить).
func destroyEmbed(id int, kind EmbedKind) {
	switch kind {
	case EmbedRDP:
		rdpview.Destroy(id)
	case EmbedVNC:
		vncview.Destroy(id)
	case EmbedPVE:
		// У PVE нет C++-канваса: остановка — pveShutdown (cancel ctx владельца
		// клиента + DeleteLater wrap, идемпотентен). Добирает сюда путь
		// removeTab → destroyEmbedFn (в т.ч. после stopEmbed-ветки ниже —
		// там pveShutdown уже отработал, здесь сработает как no-op).
		pveShutdown(id)
	}
}

// stopEmbed — просит встроенный клиент отключиться (без Qt). Используется
// крестиком вкладки: дальше вкладку уберёт колбэк Done (ok=true) → removeTab.
// Исключение — EmbedPVE: C++-колбэка Done у PVE нет, вкладку убирает сам stop
// (ветка ниже) — Done-путь для PVE не срабатывает никогда.
func stopEmbed(id int, kind EmbedKind) {
	switch kind {
	case EmbedRDP:
		rdpview.Stop(id)
	case EmbedVNC:
		vncview.Stop(id)
	case EmbedPVE:
		// Осознанное отличие от RDP/VNC: у PVE нет C++-колбэка Done, поэтому
		// embedDone вкладку никогда не уберёт — если ждать его, вкладка станет
		// зомби. Убирает её сам stop: pveShutdown гасит владельца клиента и
		// wrap, removeTab (на Qt-нити, через postMain) чистит общие карты
		// вкладок; tabCloseReq сбрасываем сразу — ждать Done некому.
		pveShutdown(id)
		tabMu.Lock()
		delete(tabCloseReq, id)
		tabMu.Unlock()
		postMain(func() { removeTab(id) })
	}
}

// shouldCloseOnDisconnect — закрыть ли вкладку терминала при завершении сессии
// (бэклог «close terminal on disconnect»): ошибку от библиотеки протокола
// (EventFailed) считаем нештатным разрывом → не закрываем (оставляем с причиной),
// иначе — по настройке closeOn.
func shouldCloseOnDisconnect(closeOn bool, kind session.EventKind) bool {
	if kind == session.EventFailed {
		return false
	}
	return closeOn
}

// connStateIcons — кэш иконок-индикаторов (по одной на состояние). Заполняется
// только на Qt-нити: setConnStatus всегда исполняет применение через mainthreadW.
// Кэш, чтобы не аллоцировать QPixmap/QPainter на каждое обновление статуса.
var connStateIcons = map[connState]*qt.QIcon{}

// connStateIcon — кружок цвета состояния для иконки вкладки (A2): 🟢 подключено,
// 🟡 попытка подключения, 🔴 ошибка/обрыв. connNone даёт пустую иконку (индикатора
// нет). Вызывать только на Qt-нити.
func connStateIcon(st connState) *qt.QIcon {
	if ic, ok := connStateIcons[st]; ok {
		return ic
	}
	var ic *qt.QIcon
	if r, g, b, has := connStateRGB(st); has {
		// 16×16 — штатный размер иконки вкладки (PM_TabBarIconSize у большинства
		// стилей); сам кружок — 10 px по центру, вровень со строчными буквами
		// подписи. На HiDPI рисуем физически крупнее и помечаем пиксмап
		// масштабированным (SetDevicePixelRatio) — иначе Qt растянул бы
		// единственный 16-пиксельный пиксмап и кружок мылился.
		dpr := 1.0
		if sc := qt.QGuiApplication_PrimaryScreen(); sc != nil {
			if v := sc.DevicePixelRatio(); v > 0 {
				dpr = v
			}
		}
		const logical, dot = 16, 10
		sz := int(float64(logical)*dpr + 0.5)
		pm := qt.NewQPixmap2(sz, sz)
		// Помечаем пиксмап масштабированным ДО рисования: QPainter сам применит
		// scale(dpr) к координатам, поэтому круг рисуется в ЛОГИЧЕСКИХ единицах
		// (иначе на DPR≠1 он уедет в device-пиксели и обрежется краем пиксмапа —
		// ревью A2 MAJOR-1, воспроизведено пробником на Qt6).
		pm.SetDevicePixelRatio(dpr)
		bg := qt.NewQColor11(0, 0, 0, 0) // прозрачный фон
		pm.FillWithFillColor(bg)
		bg.Delete()
		p := qt.NewQPainter2(pm.QPaintDevice)
		p.SetRenderHint(qt.QPainter__Antialiasing)
		p.SetPenWithStyle(qt.NoPen)
		col := qt.NewQColor3(r, g, b)
		br := qt.NewQBrush3(col)
		p.SetBrush(br)
		p.DrawEllipse2((logical-dot)/2, (logical-dot)/2, dot, dot) // логические px
		p.End()
		ic = qt.NewQIcon2(pm) // QIcon хранит копию (неявное разделение) — исходники можно удалять
		br.Delete()
		col.Delete()
		p.Delete()
		pm.Delete()
	}
	if ic == nil {
		ic = qt.NewQIcon()
	}
	connStateIcons[st] = ic
	return ic
}

// setConnStatus — состояние подключения вкладки (А2): показывается индикатором
// (см. connStateIcon), полный текст уходит в подсказку вкладки и в строку
// состояния в футере. Раньше текст дописывался в заголовок («имя — подключено»)
// и сильно удлинял название; в легаси состояние тоже было иконкой, а не текстом
// (PACTerminal.pm:2624 — gtk-connect/gtk-disconnect).
func setConnStatus(id int, st connState, status string) {
	if !uiLive.Load() {
		return
	}
	// Читаем tabID/tabName под tabMu (иначе data race с removeTab на
	// worker/goroutine нитях); в closure НЕ лезем в map.
	tabMu.Lock()
	name := tabName[id]
	wrap, ok := tabID[id]
	statusLabel := tabStatus[id] // бэклог: строка состояния (может быть nil)
	if ok {
		// A2: для reattach/setTermTitle. Только для живой вкладки — иначе запись
		// осталась бы в карте навсегда (removeTab её уже не увидит).
		tabConn[id] = connInfo{st: st, text: status}
	}
	tabMu.Unlock()
	if !ok {
		return
	}
	mainthreadW(func() {
		// бэклог (ревью Find-1): вкладка могла закрыться до исполнения — не трогаем
		// уже удалённый label/вкладку. Перепроверяем под tabMu.
		tabMu.Lock()
		_, alive := tabID[id]
		tabMu.Unlock()
		if !alive {
			return
		}
		p := presentTab(name, status, st)
		if i := mainTabs.IndexOf(wrap); i >= 0 {
			// A2: заголовок — только имя; состояние показывает индикатор, текст
			// статуса доступен в подсказке вкладки и в строке состояния в футере.
			mainTabs.SetTabText(i, p.Title)
			mainTabs.SetTabToolTip(i, p.Tooltip)
			mainTabs.SetTabIcon(i, connStateIcon(p.State))
		}
		if statusLabel != nil {
			if status != "" {
				statusLabel.SetText(status)
			} else {
				statusLabel.SetText(name)
			}
		}
	})
}

// closeTab — крестик вкладки: отключение сессии (вкладку убирает EventClosed).
func closeTab(index int) {
	// отслеживание: все вызовы Qt в mainthread (сигнал уже на Qt-нити)
	tabMu.Lock()
	var id int
	for i, wrap := range tabID {
		if mainTabs.IndexOf(wrap) == index {
			id = i
			break
		}
	}
	// M36 Split: вкладка-страница — QSplitter (sp), не wrap её членов.
	// Находим split-членов по splitTop и отключаем ОБА.
	splitIDs := make([]int, 0, 2)
	if id == 0 {
		for tid, sp := range splitTop {
			if mainTabs.IndexOf(sp.QWidget) == index {
				splitIDs = append(splitIDs, tid)
				if p, ok := splitPeer[tid]; ok && p != tid {
					splitIDs = append(splitIDs, p)
				}
				break
			}
		}
	}
	sess := tabSess[id]
	kind := tabEmbed[id]
	tabMu.Unlock()
	if len(splitIDs) > 0 {
		// закрыть обе split-вкладки (removeTab уберёт и unsplitOnClose).
		seen := map[int]bool{}
		for _, sid := range splitIDs {
			if seen[sid] {
				continue
			}
			seen[sid] = true
			closeTabForID(sid)
		}
		return
	}
	if kind != EmbedNone {
		closeEmbedTab(id)
		return
	}
	if sess != nil {
		select {
		case sess.Control <- session.ControlMsg{Kind: session.ControlDisconnect}:
		default:
		}
	}
	// Очный дебаг issue 4: после таймаута пароля сессия уже мертва — ControlDisconnect
	// теряется (default), а вкладка остаётся висеть (не реагирует на крестик).
	// Убираем вкладку напрямую (removeTab идемпотентен; goroutine вывода завершится
	// по guard tabID[id] выше — не кормит уничтоженный виджет).
	removeTab(id)
}

// closeEmbedTab — закрытие вкладки встроенного RDP/VNC-клиента (M37).
// Клиент, УЖЕ отчитавшийся Done (типично — неудачное подключение), повторного
// колбэка не даст (одноразовый m_reported в C++), поэтому вкладку убираем сами:
// иначе она не закрывается вообще — ни крестиком, ни чем-либо ещё.
// Живого клиента только просим остановиться: вкладку уберёт embedDone.
func closeEmbedTab(id int) {
	tabMu.Lock()
	finished := tabEmbedDone[id]
	kind, alive := tabEmbed[id]
	if alive && !finished {
		tabCloseReq[id] = true // M33-г: убрать вкладку в embedDone
	}
	tabMu.Unlock()
	if !alive {
		return
	}
	if finished {
		removeTab(id)
		return
	}
	stopEmbedFn(id, kind) // M33: со встроенным RDP/VNC — отключить клиент
}

// askViaDialog — диалог ответа на EventAsk (пароль/sudo/hostkey): строго на
// Qt-нити; пароль — скрытый ввод (легаси wEnterValue).
var sudoShowPassword bool // defaults.sudo show password (M10-ревью L3)

func askViaDialog(prompt string, meta any) (string, bool) {
	var answer string
	var ok bool
	mainthreadW(func() {
		d := qt.NewQInputDialog2()
		d.SetLabelText(prompt)
		switch meta {
		case session.MetaAskPassword:
			d.SetTextEchoMode(qt.QLineEdit__Password) // sudo показывается по настройке
		case session.MetaAskSudo:
			if !sudoShowPassword {
				d.SetTextEchoMode(qt.QLineEdit__Password)
			}
		}
		if mainWindow != nil {
			d.SetParent(mainWindow.QWidget)
		}
		ok = d.Exec() == int(qt.QDialog__Accepted)
		if ok {
			answer = d.TextValue()
		}
	})
	return answer, ok
}

// promptsFromCfg — промпты подключения из defaults (после Sanitize они
// гарантированно заполнены легаси-дефолтами).
func promptsFromCfg(cfg *config.Config) session.Prompts {
	d := cfg.Defaults
	// M30-ревью #7: SudoPrompt с дефолтом — тот же, что у newBuildEnv
	// (launcher.go), иначе sudo -p и sudoBlock разошлись бы на несанитизированном.
	sudoPrompt := d.SudoPrompt
	if sudoPrompt == "" {
		sudoPrompt = config.DefaultSudoPrompt
	}
	return session.Prompts{
		Command:           d.CommandPrompt,
		Username:          d.UsernamePrompt,
		Password:          d.PasswordPrompt,
		HostkeyChanged:    d.HostkeyChangedPrompt,
		AnyKey:            d.PressAnyKeyPrompt,
		RemoteHostChanged: d.RemoteHostChangedPrompt,
		SudoPrompt:        sudoPrompt,
		SudoPassword:      d.SudoPassword,
		AutoAcceptKey:     d.AutoAcceptKey == 1,
	}
}

// loadDecision — решение по результату config.LoadConfig (чистая, тестируемая).
// Файла нет — первый запуск: пустой конфиг. Любая другая ошибка (битый YAML,
// нет прав на чтение) — НЕ отдаём пустой конфиг: GUI продолжил бы работу, и
// следующее сохранение (edit/add/prefs/import) атомарно переименовало бы
// свежий конфиг поверх asbru.yml, уничтожив все соединения. Ошибку пробрасываем,
// чтобы вызывающий отказался от запуска.
func loadDecision(cfg *config.Config, err error) (*config.Config, error) {
	if err == nil {
		return cfg, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return &config.Config{Environments: map[string]config.Environment{}}, nil
	}
	return nil, err
}

// loadCfg: конфиг по умолчанию; отсутствие файла — пустой конфиг (первый запуск),
// реальная ошибка чтения/разбора — (nil, err) без права сохранять.
func loadCfg(path string) (*config.Config, error) {
	cfg, err := loadDecision(config.LoadConfig(path, config.NewCBC()))
	if err != nil {
		return nil, err
	}
	config.Sanitize(cfg)
	return cfg, nil
}

// Инжектируемые границы: регрессионные тесты lifecycle (ревью: C1-фикс без теста).
var (
	uiLive      atomic.Bool    // Qt-цикл запущен (тесты идут без него)
	mainTabs    *qt.QTabWidget // вкладки сессий (M12; на Qt-нити)
	tabMu       sync.Mutex
	tabID       = map[int]*qt.QWidget{} // wgt-id → обёртка miqt (вкладка)
	tabSess     = map[int]*session.Session{}
	tabUUID     = map[int]string{}          // wgt-id → uuid
	tabIn       = map[int]chan<- []byte{}   // wgt-id → in-канал сессии (PCC)
	tabClu      = map[int]string{}          // wgt-id → имя кластера (PCC)
	tabEmbed    = map[int]EmbedKind{}       // M33: id → встроенный RDP/VNC (иначе терминал)
	tabName     = map[int]string{}          // wgt-id → имя соединения (для статуса вкладки)
	tabStatus   = map[int]*qt.QLabel{}      // бэклог: wgt-id → label строки состояния терминала
	tabConn     = map[int]connInfo{}        // A2: wgt-id → последнее состояние/текст статуса
	tabLog      = map[int]*sessionLogBuf{}  // M36: wgt-id → буфер вывода (сохранить лог)
	tabTunnel   = map[int]*session.Tunnel{} // бэклог: wgt-id → живой SOCKS/jump-туннель (RDP/VNC)
	tabCloseReq = map[int]bool{}            // M33-г: вкладку попросили закрыть (крестик) → убрать при Done
	// M37: клиент уже отчитался Done (одноразовый m_reported в C++ — повторного
	// колбэка НЕ будет, поэтому крестик обязан убирать вкладку сам).
	tabEmbedDone = map[int]bool{}
	detachTop    = map[int]*qt.QDialog{}   // M36: wgt-id → top-level окно отцепленной вкладки
	splitPeer    = map[int]int{}           // M36: wgt-id → пара в split-вкладке (для dissolve)
	splitTop     = map[int]*qt.QSplitter{} // M36: wgt-id → QSplitter-контейнер split-вкладки
	clusters     = &clusterRegistry{byCluster: map[string][]chan<- []byte{}}

	wgtCreate  = wgt.Create
	wgtShow    = wgt.Show
	wgtDestroy = wgt.Destroy
	sessionNew = session.New
	// M37: швы встроенного RDP/VNC-клиента (тесты идут без Qt/cgo).
	stopEmbedFn    = stopEmbed
	destroyEmbedFn = destroyEmbed
	mainthreadW    = mainthread.Wait
	// postMain — НЕблокирующая доставка замыкания на Qt-нить (работа из worker-
	// горутины; mainthread.Start кладёт замыкание в очередь и возвращается).
	// В юнит-тестах (uiLive=false, Qt-петли нет) подменяется на прямой вызов.
	postMain    = mainthread.Start
	keepassOpen = keepass.Open
	mainWindow  *qt.QMainWindow // родитель модальных диалогов (M10-ревью L4)
)

// EmbedKind — тип встроенного клиента во вкладке (M33).
type EmbedKind int

const (
	EmbedNone EmbedKind = iota // обычный терминал (нет записи — можно и так)
	EmbedRDP
	EmbedVNC
	// EmbedPVE — вкладка управления Proxmox VE (виджет Qt, pvewidget.go):
	// launchEmbedded пускает её через launchPveTab (без cgo-канваса),
	// stop/destroy-ветки — через pveShutdown.
	EmbedPVE
)

// appVault — vault, открытый один раз в Run() (кэш на время приложения).
var appVault atomic.Pointer[keepass.Vault]

// embeddedKind — рисовать ли соединение встроенным клиентом. Решение о
// «встроенности» — ДАННЫЕ дескриптора (method.SpecFor), а не свитч по именам
// (M37 п.4: ревизия маппинга «метод → клиент»); здесь остаётся только выбор
// виджета под конкретный встроенный диалект.
func embeddedKind(name string) (EmbedKind, bool) {
	d, ok := method.FromDisplay(name)
	if !ok || method.SpecFor(d).Client != method.ClientEmbedded {
		return EmbedNone, false
	}
	if d == method.PVE {
		return EmbedPVE, true
	}
	if d == method.VNC {
		return EmbedVNC, true
	}
	return EmbedRDP, true
}

// launchEmbedded — подключение RDP/VNC встроенным клиентом внутри вкладки.
func launchEmbedded(uuid string, env *config.Environment, kind EmbedKind) {
	var (
		id  int
		err error
	)
	// колбэки (worker-нить) → Qt-нить (mainthreadW); id захвачен по ссылке.
	var connectedOnce sync.Once
	switch kind {
	case EmbedPVE:
		// PVE — чистый miqt-виджет (pvewidget.go): launchPveTab сам собирает
		// wrap, регистрирует карты вкладок и добавляет вкладку — здесь ни
		// Create, ни UnsafeNewQWidget/embedNative не нужны (без ветки путь
		// провалился бы в EmbedRDP с его cgo-канвасом). В тестах без Qt — no-op.
		launchPveTab(uuid, env)
		return
	case EmbedRDP:
		id, err = rdpview.Create(&rdpview.Callback{
			Frame: func(w, h int) {
				connectedOnce.Do(func() {
					setConnStatus(id, connConnected, i18n.Tr("Status", "подключено"))
					appHistory.Push(uuid, time.Now())
				}) // А2 #1: историю
			},
			Done: func(ok bool, reason string) {
				embedDone(uuid, env.TitleOrName(), id, ok, reason)
			},
			// M39-fullscreen: Esc в полноэкранном режиме — вернуть вкладку.
			Fullscreen: func() { exitEmbedFullscreenFor(id) },
		})
	case EmbedVNC:
		id, err = vncview.Create(&vncview.Callback{
			Frame: func(w, h int) {
				connectedOnce.Do(func() {
					setConnStatus(id, connConnected, i18n.Tr("Status", "подключено"))
					appHistory.Push(uuid, time.Now())
				}) // А2 #1: историю
			},
			Done: func(ok bool, reason string) {
				embedDone(uuid, env.TitleOrName(), id, ok, reason)
			},
			// M39-fullscreen: Esc в полноэкранном режиме — вернуть вкладку.
			Fullscreen: func() { exitEmbedFullscreenFor(id) },
		})
	}
	if err != nil {
		slog.Error("встроенный клиент: создать не удалось", "err", err)
		return
	}
	// M39-fullscreen: режим запрашивают легаси-токены (/f, -fullscreen, …).
	// Входим ДО embedStart, чтобы RDP-сессия открылась по размеру экрана
	// (аналог xfreerdp /f), а не по размеру вкладки.
	full := embedWantsFullscreen(kind, env)
	wrap := qt.UnsafeNewQWidget(embedNative(kind, id))
	tabMu.Lock()
	tabID[id] = wrap
	tabUUID[id] = uuid
	tabEmbed[id] = kind
	tabName[id] = env.TitleOrName()
	tabLog[id] = newSessionLogBuf()
	tabMu.Unlock()
	if uiLive.Load() {
		mainthreadW(func() {
			if embedFullID.Load() != 0 {
				exitEmbedFullscreen() // новая вкладка — выйти из чужого режима
			}
			mainTabs.AddTab(wrap, env.TitleOrName())
			mainTabs.SetCurrentWidget(wrap)
			// M39-ревью MAJOR-1: для встроенной сессии `wrap` — это САМ канвас
			// (UnsafeNewQWidget(embedNative)), поэтому фокус ставим прямо на него.
			// Без этого запуск из дерева оставлял фокус вне сессии: ни
			// ShortcutOverride, ни нажатия до канваса не доходили — F2 уходил в
			// дерево («переименовать»), Ctrl+V не попадал в сессию.
			wrap.SetFocus()
			if full {
				enterEmbedFullscreen(id)
			}
		})
	}
	// А2: «подключение…» ДО embedStart (до старта worker) — иначе мгновенное
	// «подключено» (первый Frame) может быть перезаписано статусом позже.
	setConnStatus(id, connConnecting, i18n.Tr("Status", "подключение…"))
	embedStart(kind, id, env)
}

// embedNative — нативный QWidget* клиента.
func embedNative(kind EmbedKind, id int) unsafe.Pointer {
	switch kind {
	case EmbedRDP:
		return rdpview.Native(id)
	case EmbedVNC:
		return vncview.Native(id)
	}
	return nil
}

// embedStart — запуск клиента с параметрами из окружения (M33-в).
func embedStart(kind EmbedKind, id int, env *config.Environment) {
	// uuid/tmpDir для туннеля (имя ctl/conf/pipe + password-файл прокси).
	tabMu.Lock()
	uuid := tabUUID[id]
	tabMu.Unlock()
	tmp, tmpErr := tmpDir()
	if tmpErr != nil {
		slog.Warn("встроенный клиент: tmp недоступен для туннеля", "err", tmpErr)
		tmp = ""
	}

	switch kind {
	case EmbedRDP:
		p := embed.RDPParamsFrom(env, env.Options)
		if len(p.Unknown) > 0 {
			slog.Warn("RDP: нераспознанные опции (показ. UI, не краш)",
				"name", env.TitleOrName(), "unknown", p.Unknown)
		}
		// M39-fullscreen: в полноэкранном режиме без явного /size сессия
		// открывается по размеру ЭКРАНА (аналог `xfreerdp /f`), а не по размеру
		// вкладки — иначе «во весь экран» показывал бы растянутый маленький
		// десктоп. Явный /size (и /size:workarea|fullscreen) по-прежнему
		// приоритетен (RDPParamsFrom оставит Width/Height).
		if p.Fullscreen && p.Width == 0 && p.Height == 0 && uiLive.Load() {
			var w, h int
			mainthreadW(func() { w, h = embedScreenSize() })
			p.Width, p.Height = w, h
		}
		// Очный дебаг «разрешение не то»: НЕ подставляем фиксированный размер
		// (1920x1080/экран) — сессия открывается в РАЗМЕР КАНВАСА (rdpview берет
		// width()/height() при пустом /size), как Remmina: десктоп заполняет окно
		// 1:1. Явный /size в опциях приоритетен (RDPParamsFrom его читает).
		host, port, tn, terr := maybeTunnel(env, uuid, tmp, p.Host, p.Port)
		if terr != nil {
			slog.Error("RDP: туннель не поднят", "name", env.TitleOrName(), "err", terr)
			removeTab(id)
			return
		}
		if tn != nil {
			p.Host, p.Port = host, port
			tabMu.Lock()
			tabTunnel[id] = tn
			tabMu.Unlock()
		}
		// M37: галки безопасности/производительности редактора доезжают до
		// libfreerdp3 (Flags — битмаска, значения сверены тестом с bridge.h).
		// M39: диски (/drive:) и звук (/sound:) — из существующего поля options.
		// Звук: p.Sound.ChannelArg() == "" (токена нет/off/remote) — без звука;
		// "sys:<backend>" — C++ регистрирует реальную подсистему rdpsnd, а при
		// её недоступности откатывается на дефолтный sys:fake, не валя connect.
		drives := make([]rdpview.Drive, len(p.Drives))
		for i, d := range p.Drives {
			drives[i] = rdpview.Drive{Share: d.Share, Path: d.Path}
		}
		rdpview.Start(id, p.Host, p.Port, p.Username, p.Domain, p.Password,
			env.TitleOrName(), p.Width, p.Height, p.BPP, p.Clipboard,
			p.Flags(), p.TLSSecLevel, p.Kbd, p.Sound.ChannelArg(), drives)
	case EmbedVNC:
		p := embed.VNCParamsFrom(env)
		host, port, tn, terr := maybeTunnel(env, uuid, tmp, p.Host, p.Port)
		if terr != nil {
			slog.Error("VNC: туннель не поднят", "name", env.TitleOrName(), "err", terr)
			removeTab(id)
			return
		}
		if tn != nil {
			p.Host, p.Port = host, port
			tabMu.Lock()
			tabTunnel[id] = tn
			tabMu.Unlock()
		}
		vncview.Start(id, p.Host, p.Port, p.Username, p.Password, env.TitleOrName(), p.Security)
	}
}

// embedDone — завершение встроенного клиента (worker-нить).
// ok=true — клиент отработал (был подключён) → закрываем вкладку.
// ok=false — не подключился. Если вкладку попросили закрыть (крестик, tabCloseReq) —
// убираем её независимо от ok (иначе прерванный по стоп connect оставлял бы вкладку
// с ошибкой). Иначе — причина рисуется на канвасе (M33-г, вместо QMessageBox), вкладка
// остаётся открытой; здесь лишь логируем и вешаем статус.
func embedDone(uuid, name string, id int, ok bool, reason string) {
	tabMu.Lock()
	userClose := tabCloseReq[id]
	delete(tabCloseReq, id)
	if _, alive := tabID[id]; alive {
		tabEmbedDone[id] = true // M37: клиент отработал — повторного Done не будет
	}
	tabMu.Unlock()
	if ok || userClose {
		removeTab(id)
		return
	}
	slog.Warn("встроенный клиент не подключился", "name", name, "uuid", uuid, "reason", reason)
	setConnStatus(id, connError, i18n.Tr("Status", "ошибка: ")+reason) // А2: индикатор вкладки
	// Аудит #8: уведомление об ошибке подключения (нативно к KDE) — по флагу
	// defaults 'show notifications'. curCfg задаётся один раз при старте
	// (не мутируется конкурентно) → читаем без мьютекса. Инжектируемый notifySend.
	if currentConfig() != nil && currentConfig().Defaults.ShowNotifications() {
		notifySend(i18n.Tr("Status", "Ошибка подключения: ")+name, reason, -1)
	}
}

// notifyMu/notifyLastID — закрыть предыдущее уведомление перед новым (иначе
// копятся; ревью-подмечено как мёртвый notify.Close — теперь используется).
var (
	notifyMu     sync.Mutex
	notifyLastID uint32
)

// notifySend — инжектируемая отправка уведомления (тесты подменяют).
var notifySend = func(summary, body string, timeoutMs int) {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	if notifyLastID != 0 {
		_ = notify.Close(notifyLastID) // закрыть предыдущее
		notifyLastID = 0
	}
	notifyLastID, _ = notify.Send(summary, body, timeoutMs)
}

// showEmbedError — удалено в M33-г: причина ошибки рисуется прямо на канвасе
// встроенного клиента (rdpview/vncview), а не в модальном окне.

// resolveKPXPassword — SearchPassOnKPX (бэклог #6): пароль соединения берётся из
// KeePass-базы (appVault) по KPXTitleRegexp против title записи; если базы нет,
// совпадения нет, пароль пуст или regexp невалиден — оставляем сохранённый пароль.
// Возвращает копию env с подставленным паролем (или тот же env).
func resolveKPXPassword(env *config.Environment) *config.Environment {
	if env.SearchPassOnKPX != 1 {
		return env
	}
	v := appVault.Load()
	if v == nil {
		slog.Warn("SearchPassOnKPX: KeePass-база не открыта — использую сохранённый пароль",
			"name", env.TitleOrName())
		return env
	}
	pattern := env.KPXTitleRegexp
	if pattern == "" {
		base := env.TitleOrName() // title с fallback на name (иначе .*.* — любая запись)
		if base == "" {
			slog.Warn("SearchPassOnKPX: нет title/name для дефолт-регекспа — использую сохранённый пароль",
				"name", env.TitleOrName())
			return env
		}
		pattern = ".*" + regexp.QuoteMeta(base) + ".*" // легаси-дефолт PACUtils.pm:2314
	}
	pw, err := v.SearchPassword(pattern)
	if err != nil || pw == "" {
		slog.Warn("SearchPassOnKPX: не найдено/пароль пуст — использую сохранённый пароль",
			"name", env.TitleOrName(), "err", err)
		return env
	}
	e := *env
	e.Pass = pw
	return &e
}

// pendingResize — буфер стартового размера терминала: show→ResizeEvent может
// прийти раньше создания сессии; накопленный размер пересылается после старта
// (M1-ревью). Наследуется в продолжение launchAfterPing (MINOR-4).
type pendingResize struct {
	mu   sync.Mutex
	has  bool
	cols int
	rows int
}

// launchConnection — подключение соединения из дерева: сессия (реальный
// PTY) в телетайп-терминале; нажатия виджета уходят в канал сессии.
func launchConnection(uuid string, env *config.Environment, cfg *config.Config, cluster string) {
	// Очный дебаг «двойной клик — вылетает без ошибки»: любая паника в пути
	// подключения (resolve/embed/rdpview) логируется в crash-файл И НЕ роняет
	// приложение (стек не донести при GUI-лаунчере). По стеку — диагноз.
	defer crashRecover("launchConnection")
	env, ssResolved := resolveSecretServiceCreds(env, uuid) // бэклог #7-x: пароль + пароль-фраза + прокси/jump из SecretService
	if !ssResolved {
		env, kpResolved := resolveKPXByUUID(env) // бэклог #7-продолжение: KeePass-ссылка по Uuid (точечно)
		if !kpResolved {
			env = resolveKPXPassword(env) // бэклог #6: SearchPassOnKPX — пароль из KeePass (title-regexp)
		}
	}
	// M33: RDP/VNC рисуем ВНУТРИ вкладки (встроенный клиент через libfreerdp3 /
	// libvncclient), а не запускаем внешнее окно (user-тест #4: «двойное окно»/
	// мигание). Едино для X11 и Wayland.
	if kind, ok := embeddedKind(env.Method); ok {
		launchEmbedded(uuid, env, kind)
		return
	}
	// размер до первого показа (show→ResizeEvent может прийти раньше
	// создания сессии) буферизуется и пересылается после старта (M1-ревью)
	pending := &pendingResize{}
	cb := &wgt.Callback{Title: func(string) {}, Closed: func() {}}
	cb.Resize = func(cols, rows int) {
		pending.mu.Lock()
		pending.has, pending.cols, pending.rows = true, cols, rows
		pending.mu.Unlock()
	}
	id, err := wgtCreate(cb)
	if err != nil {
		slog.Error("терминал: создать не удалось", "err", err)
		return
	}
	// M27: применить Look&Feel терминала (defaults + per-connection override);
	// цветовая схема — в генерируемый .colorscheme (QTermWidget API).
	wgt.Configure(id, termConfig(cfg, env))
	// M28: контекстное меню терминала (правый клик → Copy/Paste/Keybindings).
	wgt.SetContextMenuCB(id, func(x, y int) { termContextMenu(id, x, y) })

	be, err := newBuildEnv(uuid, env, &cfg.Defaults)
	if err != nil {
		slog.Error("окружение сессии", "err", err)
		wgtDestroy(id)
		return
	}
	// бэклог M29 «ping port before connect»: перед запуском проверить доступность
	// host:port. Настройка выкл ИЛИ пустой IP/порт (SSH-дефолт 22, Local Shell) —
	// пропускаем (иначе reachPort("",…) == false ложно блокировал бы подключение).
	// MINOR-4: Проверка выполняется НЕ на Qt-нити (иначе до 3с фриз UI), а на
	// worker-горутине; продолжение возвращается на Qt-нить через postMain.
	if rawInt(&cfg.Defaults, "ping port before connect") == 1 && env.IP != "" && env.Port != "" {
		tmo := time.Duration(rawInt(&cfg.Defaults, "ping port timeout")) * time.Second
		if tmo <= 0 {
			tmo = 3 * time.Second
		}
		// Фиксируем проверку ДО запуска горутины: pingPort — инжектируемый
		// глобал (тесты подменяют), чтение на месте вызова исключает гонку.
		pp := pingPort
		rest := func() { launchAfterPing(uuid, env, cfg, cluster, id, be, pending, cb) }
		go func() {
			if !pp(env.IP, env.Port, tmo) {
				postMain(func() {
					if uiLive.Load() { // тесты без Qt-окна — без модального диалога
						showErr(mainWindow.QWidget, i18n.Tr("Dialog", "Хост/порт недоступен: ")+env.IP+":"+env.Port)
					}
					wgtDestroy(id)
				})
				return
			}
			postMain(rest)
		}()
		return
	}
	launchAfterPing(uuid, env, cfg, cluster, id, be, pending, cb)
}

// launchAfterPing — продолжение launchConnection после успешной (или не нужной)
// проверки порта: подстановки/промпты, создание сессии, регистрация вкладки,
// откачка вывода. Вызывается на Qt-нити (в производстве — через postMain).
func launchAfterPing(uuid string, env *config.Environment, cfg *config.Config, cluster string, id int, be session.BuildEnv, pending *pendingResize, cb *wgt.Callback) {
	// подстановки + промпты из defaults (аудит-риск M10: Resolver был нулевым)
	res := session.ResolverFromConfig(be, cfg, env)
	opts := []session.Option{
		session.WithSubstitute(res),
		session.WithPrompts(promptsFromCfg(cfg)),
		// M24: лимит автопереподключений (легаси defaults 'max retry on
		// disconnect', PACUtils.pm:2053; дефолт 50).
		session.WithRetries(cfg.Defaults.MaxRetryOnDisconnect()),
		// M32: статистика подключений (глобальный store; OnStart/OnStop).
		session.WithStats(appStats),
		// M31 #4: defaults-fallback (save session logs/startup из global defaults).
		session.WithDefaults(&cfg.Defaults),
	}
	// KeePass-маски <field|uuid>: единое хранилище на процесс (M2-ревью:
	// per-click открытие на Qt-нити останавливает UI и дублирует декод).
	if appVault.Load() != nil {
		opts = append(opts, session.WithKeepassResolver(appVault.Load()))
	}
	// M23: локальные команды «before» — блокируют подключение (легаси
	// _wPrePostExec 'local before', PACTerminal.pm:3446); execute detached.
	runWhenLocal(env, "local before", res)
	ctx, cancel := context.WithCancel(context.Background()) // не defer: живёт дольше вызова
	s, err := sessionNew(ctx, be, env, opts...)
	if err != nil {
		slog.Error("сессия соединения", "uuid", uuid, "err", err)
		cancel()
		wgtDestroy(id) // окно-терминал тоже убираем (ревью C1b)
		return
	}
	// вкладка ТОЛЬКО после успешной сессии (M12-ревью H1/H2: крестик должен
	// срабатывать, а error-пути не оставляют вкладку-зомби)
	appHistory.Push(uuid, time.Now()) // #1: панель History — последние подключения
	var statusLabel *qt.QLabel
	var wrap *qt.QWidget
	// focusW — виджет, которому отдаём клавиатурный фокус после открытия вкладки.
	// Это ИМЕННО терминал, а не страница-контейнер: Qt отдаёт клавиши виджету
	// фокуса и вверх по родителям, а не вниз к детям, поэтому фокус на контейнер
	// оставил бы терминал без ввода (ревью M39 MAJOR-1).
	var focusW *qt.QWidget
	// бэклог «статус-бар терминала»: контейнер = [панель макросов, терминал,
	// label]. Только при живом Qt (uiLive): в тестах без Qt-петли создание
	// QWidget падает (SIGABRT), поэтому там — голый терминал (как раньше).
	// M28/#4: панель макросов (комбобокс/кнопки) — поверх терминала.
	if uiLive.Load() {
		container := qt.NewQWidget2()
		lay := qt.NewQVBoxLayout2()
		if mode := macroBarMode(&cfg.Defaults); mode != 0 {
			if items := macroBarList(cfg, env); len(items) > 0 {
				lay.AddWidget(buildMacroBar(id, mode, items))
			}
		}
		terminal := qt.UnsafeNewQWidget(wgt.Native(id))
		focusW = terminal
		// Очный дебаг «экран половинит, внизу лейбл»: терминал должен забирать
		// ВСЁ свободное место (stretch=1), макросы/статус-лейбл — по размеру
		// контента (stretch=0). Без этого QVBoxLayout делил место пополам.
		lay.AddWidget2(terminal, 1)
		if cfg.Defaults.ShowStatusBar() {
			statusLabel = qt.NewQLabel3(env.TitleOrName())
			lay.AddWidget(statusLabel.QWidget)
		}
		container.SetLayout(lay.QLayout)
		wrap = container
	} else {
		wrap = qt.UnsafeNewQWidget(wgt.Native(id))
		focusW = wrap
	}
	tabMu.Lock()
	tabID[id] = wrap
	if statusLabel != nil {
		tabStatus[id] = statusLabel
	}
	tabSess[id] = s
	tabUUID[id] = uuid
	tabName[id] = env.TitleOrName() // А2: для статуса вкладки
	tabIn[id] = s.In
	tabLog[id] = newSessionLogBuf()
	if cluster != "" {
		tabClu[id] = cluster
	}
	tabMu.Unlock()
	// M20: терминалы кластера живут в реестре PCC (broadcast-ввод).
	if cluster != "" {
		clusters.add(cluster, s.In)
	}
	if uiLive.Load() {
		mainthreadW(func() {
			mainTabs.AddTab(wrap, env.TitleOrName())
			mainTabs.SetCurrentWidget(wrap)
			// M39-ревью MAJOR-1: Qt отдаёт фокус новой странице только если он был
			// в прежней странице вкладок, а запуск идёт из дерева/сайдпанели — без
			// этой строки фокус оставался вне сессии, и ни ShortcutOverride, ни
			// нажатия до канваса не доходили (тот самый «F2 переименовывает»).
			if focusW != nil {
				focusW.SetFocus()
			}
		})
		// A2: жёлтый индикатор на время рукопожатия (у терминала первый статус
		// раньше приходил только зелёным на CONNECTED — всю фазу попытки, до
		// timeout connect, вкладка была без индикатора; у embed этот статус был).
		setConnStatus(id, connConnecting, i18n.Tr("Status", "подключение…"))
	}
	// Неблокирующие отправки: слоты вызываются из Qt-нити (ревью C2).
	cb.Data = func(b []byte) {
		select {
		case s.In <- b:
		default:
			slog.Warn("ввод отброшен: канал сессии переполнен", "uuid", uuid)
		}
	}
	cb.Closed = func() {
		select {
		case s.Control <- session.ControlMsg{Kind: session.ControlDisconnect}:
		default:
		}
	}
	// буфер стартового размера → TIOCSWINSZ в ssh-процесс (M11): сессия
	// создана, откачиваем накопленный размер и продолжаем новыми событиями
	pending.mu.Lock()
	if pending.has {
		select {
		case s.Control <- session.ControlMsg{Kind: session.ControlResize, Cols: pending.cols, Rows: pending.rows}:
		default:
		}
		pending.has = false
	}
	pending.mu.Unlock()
	cb.Resize = func(cols, rows int) {
		select {
		case s.Control <- session.ControlMsg{Kind: session.ControlResize, Cols: cols, Rows: rows}:
		default:
		}
	}
	// бэклог «close on disconnect»: читаем ДО горутины (иначе гонка с prefs-диалогом,
	// пис. Raw на Qt-нити; как ShowStatusBar/MaxRetryOnDisconnect при launch).
	closeOn := cfg.Defaults.CloseTerminalOnDisconnect()
	go func() {
		connected := false // соединение было установлено (user-тест #2/#4)
		for e := range s.Out {
			// Очный дебаг issue 4: вкладку могли закрыть крестиком (closeTab
			// removeTab) до конца сессии — не кормить уничтоженный виджет.
			tabMu.Lock()
			_, alive := tabID[id]
			tabMu.Unlock()
			if !alive {
				cancel()
				return
			}
			switch e.Kind {
			case session.EventOutput:
				wgt.Feed(id, []byte(e.Text))
				// M36: буфер вывода для «Сохранить лог сессии».
				tabMu.Lock()
				if lb, ok := tabLog[id]; ok {
					lb.append([]byte(e.Text))
				}
				tabMu.Unlock()
			case session.EventAsk:
				// вопрос пользователю (пароль/sudo/hostkey): диалог на Qt-нити,
				// ответ в канал (session.ask блокируется до ответа)
				text, ok := askViaDialog(e.Text, e.Meta)
				s.Control <- session.ControlMsg{Kind: session.ControlAnswer, Text: text, Cancel: !ok}
			case session.EventStatusConnected:
				connected = true
				setConnStatus(id, connConnected, i18n.Tr("Status", "подключено")) // А2: индикатор вкладки
				// M23: локальные команды «connected» (после CONNECTED)
				runWhenLocal(env, "local connected", res)
				// название вкладки/окна: OSC-титул в эмулятор (легаси title);
				// управляющие байты вырезаются — нет терминального экшейта (L1)
				wgt.Feed(id, []byte("\x1b]2;"+escapeOSC(env.TitleOrName())+"\x1b\\"))
			case session.EventClosed, session.EventFailed:
				slog.Info("сессия завершена", "uuid", uuid, "kind", e.Kind, "text", e.Text)
				runWhenLocal(env, "local after", res) // M23: «после» при выходе
				if !connected {
					// Ревью user-теста #2/#4: соединение НЕ установилось (напр.
					// ошибка RDP) — окно не закрываем, показываем причину и оставляем
					// вкладку (в оригинале окно оставалось; пользователь читает и
					// закрывает сам). Потерянный вывод уже пришёл через EventOutput.
					if e.Text != "" {
						wgt.Feed(id, []byte("\r\n[*] "+e.Text+"\r\n"))
					}
					// A2: вкладка остаётся жить, но соединения нет — красный индикатор.
					// Раньше состояние тут вообще не обновлялось: вкладка оставалась
					// без индикатора до самого закрытия (баг, найденный при A2).
					setConnStatus(id, connError, endStatusText(e.Kind == session.EventFailed, false, e.Text))
					// M8: упавший ДО CONNECTED член кластера не должен оставаться в
					// PCC-реестре (иначе count(cluster) завышен, send пишет в буфер без
					// читающего). detachDeadSession чистит tabIn/tabClu/tabSess строго
					// под tabMu (гонка с removeTab на Qt-нити — finding 1).
					detachDeadSession(id)
					cancel()
					return // не удаляем вкладку (auto-close по событиям не трогаем)
				}
				// бэклог «close terminal on disconnect»: ошибку от библиотеки (EventFailed)
				// или настройку-выкл — оставляем вкладку с причиной; иначе закрываем.
				if shouldCloseOnDisconnect(closeOn, e.Kind) {
					removeTab(id) // контракт: Qt-нить (C1c) + закрытие вкладки (M12)
					cancel()
					return
				}
				if e.Text != "" {
					wgt.Feed(id, []byte("\r\n[*] "+e.Text+"\r\n"))
				}
				// A2: сессия отключилась, вкладку оставляем (closeOn=нет) — красный
				// индикатор вместо «подключено», которое тут оставалось навсегда.
				setConnStatus(id, connError, endStatusText(e.Kind == session.EventFailed, true, e.Text))
				detachDeadSession(id) // сессия мертва: Restart должен переподключить
				cancel()
				return
			}
		}
		// канал закрыт: reader-страховка (ревью M13 H1) — даже если
		// EventClosed был потерян, вкладка не остаётся зомби.
		if connected {
			removeTab(id)
		} else {
			// A2: вкладка остаётся жить без соединения — красный индикатор (сейчас
			// ветка недостижима: Closed/Failed эмитятся до exitHalt, но состояние
			// должно быть согласовано с комментарием «вкладка не остаётся зомби»).
			setConnStatus(id, connError, endStatusText(false, false, ""))
			detachDeadSession(id) // Restart на мёртвой сессии должен работать
		}
		cancel()
	}()
}

// dropInsideDraggedSubtree — точка дропа лежит на перетаскиваемом узле или
// внутри его поддерева (подъём по цепочке Parent от цели дропа).
func dropInsideDraggedSubtree(tw *qt.QTreeWidget, event *qt.QDropEvent) bool {
	dragged := tw.CurrentItem() // InternalMove: текущий и есть перетаскиваемый
	if dragged == nil {
		return false
	}
	it := tw.ItemAt(event.Pos())
	for ; it != nil; it = it.Parent() {
		if itemID(it) == itemID(dragged) { // по C-хэндлу, не по пересозданной обёртке
			return true
		}
	}
	return false
}
