package ui

// PVE-виджет (вкладка управления Proxmox VE, miqt/Qt6; диалект
// «Proxmox VE (PVE)»). Виджет тонкий: данные и правила — в internal/pve и
// pvemodel.go (PveTreeRows/PveMenuAllowed/PveConfirmCount), здесь только
// отрисовка, обновление и раздача действий.
//
// Потоки: виджет собирается на Qt-нити; обращения к pve.Client идут
// ПОСЛЕДОВАТЕЛЬНО из одной горутины-владельца (pveLoop — контракт
// потокобезопасности internal/pve.Client); любые возвраты в Qt — только
// через postMain. Индикатор состояния вкладки (A2) — setConnStatus:
// connConnecting до первого успешного Resources, дальше connConnected /
// connError по исходу каждого обновления.

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/method"
	"github.com/DunkanAidaho/asbru-go/internal/pve"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
	qt "github.com/mappu/miqt/qt6"
)

// ---- чистые функции (тестируются без Qt — pvewidget_test.go) ----

// pveTlsVerify — проверять ли TLS-сертификат PVE: токен «verify-tls» в опциях
// (галка редактора, method.TogglePresent-семантика). По умолчанию — НЕ
// проверять: у большинства инсталляций PVE self-signed сертификат.
func pveTlsVerify(options string) bool {
	return slices.Contains(method.SplitArgs(options), "verify-tls")
}

// pvePort — порт API PVE из окружения (строка; пусто/невалид → 8006).
func pvePort(port string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(port)); err == nil && n > 0 && n < 65536 {
		return n
	}
	return 8006
}

// pveBase — базовый URL API PVE (всегда https: plain http у PVE только
// редиректит).
func pveBase(host string, port int) string {
	// IPv6-литерал без скобок даёт невалидный URL (ревью 2, Minor).
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("https://%s:%d", host, port)
}

// pveRowNode — имя ноды строки из ParentKey («node:pve1» → «pve1»). У строки
// стора ParentKey — «storagehdr», поэтому нода берётся из Key
// («storage:<node>:<name>»). У корней (нода, storagehdr) — пусто.
func pveRowNode(row PveRow) string {
	if n, ok := strings.CutPrefix(row.ParentKey, "node:"); ok {
		return n
	}
	if row.Kind == "storage" {
		if rest, ok := strings.CutPrefix(row.Key, "storage:"); ok {
			if n, _, found := strings.Cut(rest, ":"); found {
				return n
			}
		}
	}
	return ""
}

// pveIPKey — ключ кэша IP: нода + vmid. VMID может совпасть на разных нодах
// (импорт конфигов), поэтому ключи строк дерева тоже включают ноду.
func pveIPKey(node string, vmid int) string {
	return fmt.Sprintf("%s:%d", node, vmid)
}

// pveCacheIPs — занесение GuestAgentIPs в кэш по ключу: набор ЗАМЕЩАЕТСЯ
// (сменившийся по DHCP адрес не висит рядом со старым); пустой список
// (гостевой агент не стоит — частый случай) кэш не трогает.
func pveCacheIPs(cache map[string][]string, key string, ips []string) {
	if cache == nil || len(ips) == 0 {
		return
	}
	cache[key] = ips
}

// pveGuestCol — колонка «Гость»: нода/стор — имя как есть, гость —
// «имя (vmid)».
func pveGuestCol(r PveRow) string {
	if r.VMID > 0 {
		return fmt.Sprintf("%s (%d)", r.Name, r.VMID)
	}
	return r.Name
}

// pveActionLabel — подпись мутации для подтверждений и статуса.
func pveActionLabel(action string) string {
	switch action {
	case "start":
		return i18n.Tr("PVE", "запуск")
	case "stop":
		return i18n.Tr("PVE", "остановка")
	case "reboot":
		return i18n.Tr("PVE", "перезагрузка")
	case "shutdown":
		return i18n.Tr("PVE", "завершение работы")
	// Ревью A (Important): сырые коды в подтверждениях опасных операций —
	// «Выполнить «snapRollback»?» вместо читаемого текста (требование спеки п.1).
	case "snapCreate":
		return i18n.Tr("PVE", "создание снапшота")
	case "snapRollback":
		return i18n.Tr("PVE", "откат на снапшот")
	case "snapDelete":
		return i18n.Tr("PVE", "удаление снапшота")
	case "backup":
		return i18n.Tr("PVE", "резервное копирование")
	}
	return action
}

// pveConfigText — конфиг гостя (GuestConfig) → сортированный текст
// «ключ: значение» для диалога «Показать конфигурацию». Значения — как
// отдал JSON (%v), разбора в типы нет.
func pveConfigText(cfg map[string]any) string {
	keys := make([]string, 0, len(cfg))
	for k := range cfg {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %v\n", k, cfg[k])
	}
	return b.String()
}

// pveSnapshotTime — время снапшота для колонки диалога (unix → локальное;
// 0 — время не задано).
func pveSnapshotTime(t int64) string {
	if t == 0 {
		return ""
	}
	return time.Unix(t, 0).Format("02.01.2006 15:04:05")
}

// pveStatusRGB — семантический цвет текста колонки «Статус» (A2):
// running/online — зелёный, stopped/offline — серый, прочее — янтарный;
// false — не красить (пустой статус у строки-заголовка секции storagehdr).
func pveStatusRGB(status string) (r, g, b int, ok bool) {
	switch status {
	case "running", "online":
		return 32, 160, 64, true
	case "stopped", "offline":
		return 128, 128, 128, true
	case "":
		return 0, 0, 0, false
	}
	return 200, 140, 32, true
}

// ---- виджет ----

const (
	pveTimeout = 3 * time.Second // таймаут HTTP-клиента PVE (в т.ч. GuestAgentIPs)
	pvePoll    = time.Second     // опрос WaitTask и тик прогресса
	pveBaseID  = 3000000         // нижняя граница id вкладок PVE
)

// pveIDSeq — счётчик id вкладок PVE от pveBaseID: cgo-виджеты телетайма/RDP
// занимают 1M+, VNC — 2M+; PVE-вкладка — чистый Qt без cgo-id.
var pveIDSeq atomic.Int64

// nextPveID — следующий id вкладки PVE.
func nextPveID() int { return pveBaseID + int(pveIDSeq.Add(1)) }

// newPveClient — шов сборки клиента (тесты и будущее подменяют).
var newPveClient = func(base string, tlsSkip bool, timeout time.Duration) *pve.Client {
	return pve.NewClient(base, tlsSkip, timeout)
}

// closePveClient — освобождение idle-соединений клиента. Шов для тестов:
// снаружи Close не наблюдаем, а вызываться обязан на выходе pveLoop
// (pveShutdown → cancel) и в конце короткоживущего клиента trackTask.
var closePveClient = func(c *pve.Client) {
	c.Close()
}

// pveOp — единица работы с клиентом; исполняется горутиной-владельцем.
type pveOp func(c *pve.Client)

// pveWidget — вкладка управления PVE.
type pveWidget struct {
	id     int
	uuid   string
	env    *config.Environment
	client *pve.Client
	ctx    context.Context
	cancel context.CancelFunc

	ops chan pveOp // очередь операций клиента (см. pveLoop)

	wrap    *qt.QWidget     // страница вкладки (тулбар + дерево)
	tree    *qt.QTreeWidget // Гость/Статус/IP/Теги
	statusL *qt.QLabel      // статус обновлений/операций в тулбаре вкладки

	// Ниже — только на Qt-нити (fill, меню, postMain-замыкания).
	rows     []PveRow
	items    map[string]*qt.QTreeWidgetItem // Key → строка (живёт до следующего fill)
	byItem   map[unsafe.Pointer]string
	expanded map[string]bool // память раскрытости нод/секций между обновлениями
	selected string          // Key выбранной строки

	mu  sync.Mutex          // охраняет ips (владелец пишет, fill читает)
	ips map[string][]string // кэш IP: «нода:vmid» → адреса GuestAgentIPs

	loginMu sync.Mutex // охраняет loginOK (владелец)
	loginOK bool       // тикет получен; неудача НЕ кэшируется — следующее обновление повторит

	refreshing atomic.Bool // одно обновление дерева за раз
}

// pveTabs — живые PVE-вкладки (id → виджет) для pveShutdown.
var (
	pveTabsMu sync.Mutex
	pveTabs   = map[int]*pveWidget{}
)

// launchPveTab — создать вкладку управления PVE и вернуть её wgt-id
// (вызывается из launchEmbedded в задаче подключения вкладок; вызов — на
// Qt-нити).
func launchPveTab(uuid string, env *config.Environment) int {
	if env == nil || !uiLive.Load() {
		return 0 // тесты без Qt-петли: виджет не создаётся
	}
	return newPveTab(uuid, env).id
}

// newPveTab — сборка виджета: тулбар («Обновить» + статус) + QTreeWidget;
// регистрация в картах вкладок под tabMu.
func newPveTab(uuid string, env *config.Environment) *pveWidget {
	ctx, cancel := context.WithCancel(context.Background())
	w := &pveWidget{
		id:     nextPveID(),
		uuid:   uuid,
		env:    env,
		client: newPveClient(pveBase(env.IP, pvePort(env.Port)), !pveTlsVerify(env.Options), pveTimeout),
		ctx:    ctx,
		cancel: cancel,
		ops:    make(chan pveOp, 16),
		ips:    map[string][]string{},
		items:  map[string]*qt.QTreeWidgetItem{},
		byItem: map[unsafe.Pointer]string{},
	}
	w.expanded = map[string]bool{}

	wrap := qt.NewQWidget2()
	lay := qt.NewQVBoxLayout2()

	bar := qt.NewQHBoxLayout2()
	refreshBtn := qt.NewQPushButton3(i18n.Tr("PVE", "Обновить"))
	refreshBtn.OnClicked(w.refresh)
	bar.AddWidget(refreshBtn.QWidget)
	bar.AddStretch()
	w.statusL = qt.NewQLabel3(i18n.Tr("PVE", "подключение…"))
	bar.AddWidget(w.statusL.QWidget)

	tree := qt.NewQTreeWidget2()
	tree.SetColumnCount(4)
	tree.SetHeaderLabels([]string{
		i18n.Tr("PVE", "Гость"), i18n.Tr("PVE", "Статус"), i18n.Tr("PVE", "IP"), i18n.Tr("PVE", "Теги"),
	})
	tree.SetColumnWidth(0, 240)
	tree.SetColumnWidth(1, 100)
	tree.SetColumnWidth(2, 180)
	tree.SetColumnWidth(3, 140)
	tree.SetContextMenuPolicy(qt.CustomContextMenu)
	tree.OnCustomContextMenuRequested(w.onMenu)
	// Ревью A (Б2): запоминать выбор — иначе после «Обновить» выделение и
	// клавиатурная позиция сбрасывались в начало (восстанавливалась только
	// раскрытость). Запись/чтение w.selected — только Qt-нить.
	tree.OnCurrentItemChanged(func(cur, _ *qt.QTreeWidgetItem) {
		if cur != nil {
			w.selected = w.byItem[itemID(cur)]
		}
	})

	lay.AddLayout(bar.QLayout)
	lay.AddWidget2(tree.QWidget, 1)
	wrap.SetLayout(lay.QLayout)
	w.wrap, w.tree = wrap, tree

	name := env.TitleOrName()
	tabMu.Lock()
	tabID[w.id] = wrap
	tabUUID[w.id] = uuid
	tabName[w.id] = name
	tabEmbed[w.id] = EmbedPVE // ветки stop/destroy для типа — в задаче подключения вкладок
	tabMu.Unlock()
	pveTabsMu.Lock()
	pveTabs[w.id] = w
	pveTabsMu.Unlock()

	mainTabs.AddTab(wrap, name)
	mainTabs.SetCurrentWidget(wrap)
	// A2: жёлтый индикатор, пока первый Resources не прошёл.
	setConnStatus(w.id, connConnecting, i18n.Tr("Status", "подключение…"))

	go w.pveLoop()
	w.refresh()
	return w
}

// pveShutdown — остановка PVE-вкладки (крестик/выход; вызывается из
// stop/destroy путей вкладок в задаче подключения вкладок): отмена контекста
// (горутина-владелец клиента и WaitTask завершаются) и снятие с реестра
// pveTabs. Общие карты вкладок (tabID/tabUUID/tabName/tabEmbed) не трогает —
// ими владеет removeTab.
//
// Фикс-раунд 1 (утечка): wrap удаляем здесь — removeTab стирает tabID ДО
// destroy-фазы и для embedded-типов страницу не удаляет (только при
// EmbedNone), так что к моменту destroy-пути единственный держатель wrap —
// виджет. DeleteLater потокобезопасен (QObject ставит DeferredDelete в
// очередь нити объекта) — можно из любой нити; повторный вызов идемпотентен:
// после первого реестр пуст, дальше — no-op.
func pveShutdown(id int) {
	pveTabsMu.Lock()
	w := pveTabs[id]
	delete(pveTabs, id)
	pveTabsMu.Unlock()
	if w == nil {
		return
	}
	w.cancel()
	if w.wrap != nil {
		w.wrap.DeleteLater()
	}
}

// pveLoop — горутина-владелец клиента: единственное место обращений к
// pve.Client (контракт потокобезопасности internal/pve). Операции ставятся в
// w.ops; отмена ctx (pveShutdown) завершает цикл.
func (w *pveWidget) pveLoop() {
	defer closePveClient(w.client)
	for {
		select {
		case <-w.ctx.Done():
			return
		case op := <-w.ops:
			if w.ctx.Err() != nil {
				// Ревью A (Б3): после cancel оп мог уже лежать в очереди —
				// исполнение среди зомби-горутин ломает контракт «один
				// владелец клиента» (релогин-гонка на тикете).
				return
			}
			op(w.client)
		}
	}
}

// enqueue — поставить операцию клиента в очередь; после pveShutdown
// отправка отменяется (не висит навсегда).
func (w *pveWidget) enqueue(op pveOp) {
	go func() {
		select {
		case w.ops <- op:
		case <-w.ctx.Done():
		}
	}()
}

// alive — жива ли вкладка (не убрана из карт removeTab'ом).
func (w *pveWidget) alive() bool {
	tabMu.Lock()
	_, ok := tabID[w.id]
	tabMu.Unlock()
	return ok
}

// setStatus — текст статуса в тулбаре вкладки (пост на Qt-нить).
func (w *pveWidget) setStatus(text string) {
	postMain(func() {
		if !w.alive() {
			return
		}
		w.statusL.SetText(text)
	})
}

// updatedText — финальный статус успешного обновления.
func (w *pveWidget) updatedText() string {
	return i18n.Tr("PVE", "обновлено ") + time.Now().Format("15:04:05")
}

// fail — неудача обновления: индикатор вкладки — connError, текст в статус.
func (w *pveWidget) fail(err error) {
	slog.Warn("pve: обновление не удалось", "id", w.id, "err", err)
	setConnStatus(w.id, connError, i18n.Tr("Status", "ошибка: ")+err.Error())
	w.setStatus(i18n.Tr("PVE", "ошибка: ") + err.Error())
}

// opFail — неудача операции (не обновления): индикатор вкладки не трогаем.
func (w *pveWidget) opFail(what string, err error) {
	slog.Warn("pve: операция не удалась", "what", what, "err", err)
	w.setStatus(i18n.Tr("PVE", "ошибка: ") + what + ": " + err.Error())
}

// refresh — запрос обновления (кнопка/меню/после действий). На Qt-нити не
// блокируется: операция уходит в очередь владельца клиента.
func (w *pveWidget) refresh() {
	if !w.refreshing.CompareAndSwap(false, true) {
		w.setStatus(i18n.Tr("PVE", "обновление уже идёт…"))
		return
	}
	w.setStatus(i18n.Tr("PVE", "обновление…"))
	w.enqueue(func(c *pve.Client) {
		defer w.refreshing.Store(false)
		w.doRefresh(c)
	})
}

// doRefresh — тело обновления (в горутине-владельце): Login → Resources →
// пост перестройки дерева → последовательный опрос IP RUNNING qemu.
func (w *pveWidget) doRefresh(c *pve.Client) {
	if err := w.ensureLogin(c); err != nil {
		w.fail(err)
		return
	}
	rs, err := c.Resources(w.ctx)
	if err != nil {
		w.fail(err)
		return
	}
	setConnStatus(w.id, connConnected, i18n.Tr("Status", "подключено"))
	rows := PveTreeRows(rs)
	postMain(func() {
		if !w.alive() {
			return
		}
		w.rows = rows
		w.fill()
	})
	if ipErr := w.fetchIPs(c, rows); ipErr != nil {
		// обновление состоялось, но часть IP не опросилась — показать и это
		w.setStatus(w.updatedText() + i18n.Tr("PVE", " (IP: ") + ipErr.Error() + ")")
		return
	}
	w.setStatus(w.updatedText())
}

// ensureLogin — тикет PVE: успешный логин — один раз на вкладку; неудача
// (транзиентная сеть) НЕ запоминается — следующее «Обновить» повторит
// попытку. Дальше клиент молча перелогинивается сам при протухшем тикете
// (pve.Client.raw).
func (w *pveWidget) ensureLogin(c *pve.Client) error {
	w.loginMu.Lock()
	defer w.loginMu.Unlock()
	if w.loginOK {
		return nil
	}
	if err := c.Login(w.env.User, w.env.Pass); err != nil {
		return err // loginOK не ставим — ретрай при следующем обновлении
	}
	w.loginOK = true
	return nil
}

// fetchIPs — колонка «IP»: для RUNNING qemu последовательно GuestAgentIPs
// (таймаут — клиента, 3с). Отсутствие гостевого агента — пустой список, не
// сбой (pve.Client); ошибка опроса → «—» в колонке и текст в статус вкладки.
// Секвенция осознанная: кластерного агент-эндпоинта у PVE нет, идём по
// гостям по одному. Возвращает последнюю ошибку опроса (nil — все ок).
func (w *pveWidget) fetchIPs(c *pve.Client, rows []PveRow) error {
	var lastErr error
	for _, r := range rows {
		if w.ctx.Err() != nil {
			return lastErr // вкладка закрывается — не дотягиваем до конца
		}
		if r.Kind != "qemu" || r.Status != "running" {
			continue
		}
		ips, err := c.GuestAgentIPs(w.ctx, pveRowNode(r), r.VMID)
		if err != nil {
			lastErr = err
			slog.Warn("pve: guest-agent IP", "vmid", r.VMID, "err", err)
		}
		ipKey := pveIPKey(pveRowNode(r), r.VMID)
		w.mu.Lock()
		pveCacheIPs(w.ips, ipKey, ips)
		text := PveIPsText(w.ips[ipKey])
		w.mu.Unlock()
		if err != nil {
			text = "—"
		}
		key := r.Key
		postMain(func() {
			if !w.alive() {
				return
			}
			// строку ищем в ТЕКУЩЕМ дереве: между опросами мог произойти fill —
			// тогда указатель старого item сюда просто не попадёт
			if it, ok := w.items[key]; ok {
				it.SetText(2, text)
			}
		})
	}
	return lastErr
}

// fill — перестройка дерева по w.rows: секция «Ноды» (дети — гости) и
// «Сторажи» (storagehdr + сторы); раскрытость нод и выбор сохраняются между
// обновлениями. Qt-нить.
func (w *pveWidget) fill() {
	present := make(map[string]bool, len(w.rows))
	for _, r := range w.rows {
		present[r.Key] = true
	}
	// раскрытость: что раскрыто сейчас + строки, которых в новом наборе нет
	// (свернули ноду →Collapsed; исчезнувшая и вернувшаяся — восстановится)
	expanded := map[string]bool{}
	for key, it := range w.items {
		if it.IsExpanded() {
			expanded[key] = true
		}
	}
	for key := range w.expanded {
		if !present[key] {
			expanded[key] = true
		}
	}
	w.expanded = expanded
	if !present[w.selected] {
		w.selected = ""
	}

	w.tree.Clear()
	w.items = map[string]*qt.QTreeWidgetItem{}
	w.byItem = map[unsafe.Pointer]string{}

	tops := map[string]*qt.QTreeWidgetItem{} // ParentKey → верхний item
	for _, r := range w.rows {
		it := qt.NewQTreeWidgetItem2([]string{pveGuestCol(r), r.Status, w.ipCol(r), r.Tags})
		// цветной статус (A2) — семантический цвет текста, не стиль темы;
		// storagehdr (пустой статус) остаётся цветом по умолчанию
		if cr, cg, cb, ok := pveStatusRGB(r.Status); ok {
			col := qt.NewQColor3(cr, cg, cb)
			br := qt.NewQBrush3(col)
			it.SetForeground(1, br) // QTreeWidgetItem хранит копию — исходники можно удалять
			br.Delete()
			col.Delete()
		}
		w.items[r.Key] = it
		w.byItem[itemID(it)] = r.Key
		if r.ParentKey == "" {
			w.tree.AddTopLevelItem(it)
			tops[r.Key] = it
		} else if p, ok := tops[r.ParentKey]; ok {
			p.AddChild(it)
		}
		if w.expanded[r.Key] {
			it.SetExpanded(true)
		}
		if r.Key == w.selected {
			w.tree.SetCurrentItem(it)
		}
	}
}

// ipCol — колонка «IP» из кэша: пусто (нет данных/агента) → «—». Qt-нить.
func (w *pveWidget) ipCol(r PveRow) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t := PveIPsText(w.ips[pveIPKey(pveRowNode(r), r.VMID)]); t != "" {
		return t
	}
	return "—"
}

// onMenu — контекстное меню строки дерева: пункты по PveMenuAllowed. Qt-нить.
func (w *pveWidget) onMenu(pos *qt.QPoint) {
	item := w.tree.ItemAt(pos)
	if item == nil {
		return
	}
	key, ok := w.byItem[itemID(item)]
	if !ok {
		return
	}
	var row PveRow
	for _, r := range w.rows {
		if r.Key == key {
			row = r
			break
		}
	}
	if row.Key == "" {
		return
	}
	global := w.tree.MapToGlobalWithQPoint(pos)

	menu := wgt.NewMenu()
	defer menu.Free() // иначе каждый клик утекает native QMenu (как tree_menu)

	type entry struct {
		ptr unsafe.Pointer
		on  func()
	}
	var entries []entry
	add := func(label string, on func()) unsafe.Pointer {
		p := menu.Item(label)
		entries = append(entries, entry{p, on})
		return p
	}

	node := pveRowNode(row)
	if PveMenuAllowed(row, "console") {
		add(i18n.Tr("PVE", "Консоль"), func() { pveOpenConsole(w.uuid, w.env, node, row.Kind, row.VMID, row.Name) })
	}
	if PveMenuAllowed(row, "start") {
		add(i18n.Tr("PVE", "Запустить"), func() { w.guestAction(row, "start") })
	}
	if PveMenuAllowed(row, "stop") {
		add(i18n.Tr("PVE", "Остановить"), func() { w.guestAction(row, "stop") })
	}
	if PveMenuAllowed(row, "reboot") {
		add(i18n.Tr("PVE", "Перезагрузить"), func() { w.guestAction(row, "reboot") })
	}
	if PveMenuAllowed(row, "shutdown") {
		add(i18n.Tr("PVE", "Завершить работу"), func() { w.guestAction(row, "shutdown") })
	}
	if PveMenuAllowed(row, "snapshots") {
		add(i18n.Tr("PVE", "Снапшоты…"), func() { w.snapshotsDialog(row) })
	}
	if PveMenuAllowed(row, "backup") {
		add(i18n.Tr("PVE", "Бэкап…"), func() { w.backupDialog(row) })
	}
	if PveMenuAllowed(row, "config") {
		add(i18n.Tr("PVE", "Показать конфигурацию"), func() { w.configDialog(row) })
	}
	if len(entries) == 0 {
		return
	}
	chosen := menu.Check(global.X(), global.Y())
	for _, e := range entries {
		if e.ptr == chosen {
			e.on()
			return
		}
	}
}

// confirmAction — подтверждения мутации (PveConfirmCount): 1 — обычный
// вопрос, 2 — двойное подтверждение (второй текст «Действие необратимо…»).
// Qt-нить.
func (w *pveWidget) confirmAction(row PveRow, action string) bool {
	for i := 0; i < PveConfirmCount(action); i++ {
		text := fmt.Sprintf(i18n.Tr("PVE", "Выполнить «%s» для %s (%d)?"),
			pveActionLabel(action), row.Name, row.VMID)
		if i == 1 {
			text = i18n.Tr("PVE", "Действие необратимо для несохранённых данных. Продолжить?")
		}
		if qt.QMessageBox_Question(w.tree.QWidget, i18n.Tr("PVE", "Подтверждение"), text) != qt.QMessageBox__Yes {
			return false
		}
	}
	return true
}

// guestAction — подтверждённая мутация гостя (start/stop/reboot/shutdown):
// GuestAction → WaitTask (опрос 1с, прогресс в статус вкладки) →
// переобновление дерева.
func (w *pveWidget) guestAction(row PveRow, action string) {
	if !w.confirmAction(row, action) {
		return
	}
	what := pveActionLabel(action) + " " + strconv.Itoa(row.VMID)
	node, kind, vmid := pveRowNode(row), row.Kind, row.VMID
	w.enqueue(func(c *pve.Client) {
		w.setStatus(what + "…")
		upid, err := c.GuestAction(w.ctx, node, kind, vmid, action)
		if err != nil {
			w.opFail(what, err)
			return
		}
		// Ревью A (улучшение 1): shutdown/reboot идут минуты — ждать их на
		// владельце значило заблокировать «Обновить» и все прочие действия.
		w.trackTask(upid, what, w.refresh)
	})
}

// trackTask — ожидание задачи PVE ВНЕ горутины-владельца. Клиент
// непотокобезопасен → короткоживущий СВОЙ (прецедент — pveOpenConsole),
// тикет получает сам. По завершении (успех/ошибка/отмена) зовёт after —
// обновление дерева/списка.
func (w *pveWidget) trackTask(upid, what string, after func()) {
	go func() {
		if after != nil {
			defer after()
		}
		c := newPveClient(pveBase(w.env.IP, pvePort(w.env.Port)), !pveTlsVerify(w.env.Options), pveTimeout)
		defer closePveClient(c)
		if err := c.Login(w.env.User, w.env.Pass); err != nil {
			w.opFail(what, err)
			return
		}
		w.waitTask(c, upid, what)
	}()
}

// waitTask — ожидание задачи PVE (WaitTask, опрос раз в pvePoll) с
// прогрессом «выполняется (N с)…» в статус вкладки. Вызывается из горутины,
// владеющей клиентом (владелец для быстрых опов, trackTask — для задач).
func (w *pveWidget) waitTask(c *pve.Client, upid, what string) {
	done := make(chan error, 1)
	go func() { done <- c.WaitTask(w.ctx, upid, pvePoll) }()
	n := 0
	tick := time.NewTicker(pvePoll)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				w.opFail(what, err)
			} else {
				w.setStatus(what + i18n.Tr("PVE", ": готово"))
			}
			return
		case <-tick.C:
			n++
			w.setStatus(fmt.Sprintf("%s%s (%d с)…", what, i18n.Tr("PVE", ": выполняется"), n))
		case <-w.ctx.Done():
			return
		}
	}
}

// snapshotsDialog — «Снапшоты…»: список (имя/описание/время) + Создать
// (ввод имени) / Откатить / Удалить (PveConfirmCount). Мутации — в очередь
// владельца (WaitTask); список перечитывается после каждой. Qt-нить.
func (w *pveWidget) snapshotsDialog(row PveRow) {
	node, kind, vmid := pveRowNode(row), row.Kind, row.VMID
	title := i18n.Tr("PVE", "Снапшоты: ") + fmt.Sprintf("%s (%d)", row.Name, row.VMID)

	d := qt.NewQDialog(w.tree.QWidget)
	d.SetWindowTitle(title)
	d.SetMinimumSize(qt.NewQSize2(480, 340))

	list := qt.NewQTreeWidget2()
	list.SetColumnCount(3)
	list.SetHeaderLabels([]string{
		i18n.Tr("PVE", "Имя"), i18n.Tr("PVE", "Описание"), i18n.Tr("PVE", "Время"),
	})
	list.SetColumnWidth(0, 140)
	list.SetColumnWidth(1, 200)

	// Ревью 2/3 (Important): alive() — живость ВКЛАДКИ, не диалога. После
	// закрытия диалога (DeleteLater) отложенные postMain-замыкания трогали бы
	// освобождённые list/кнопки — UB. dlgClosed гасит их все.
	dlgClosed := new(atomic.Bool)
	reload := func() {
		w.enqueue(func(c *pve.Client) {
			ss, err := c.Snapshots(w.ctx, node, kind, vmid)
			if err != nil {
				w.opFail(i18n.Tr("PVE", "снапшоты"), err)
				return
			}
			postMain(func() {
				if dlgClosed.Load() || !w.alive() {
					return
				}
				list.Clear()
				for _, s := range ss {
					list.AddTopLevelItem(qt.NewQTreeWidgetItem2([]string{
						s.Name, s.Description, pveSnapshotTime(s.Time),
					}))
				}
			})
		})
	}
	selected := func() string {
		if it := list.CurrentItem(); it != nil {
			return it.Text(0)
		}
		return ""
	}
	// setBtns присваивается после создания кнопок (замыкание ловит переменную).
	var setBtns func(bool)
	snapOp := func(action, name, what string) {
		// Ревью A (улучшение 2а): кнопки остаются активными — второй клик
		// ставил ВТОРУЮ задачу (двойной откат). Гасим до завершения.
		if setBtns != nil {
			setBtns(false)
		}
		w.enqueue(func(c *pve.Client) {
			w.setStatus(what + "…")
			var (
				upid string
				err  error
			)
			switch action {
			case "snapCreate":
				upid, err = c.SnapshotCreate(w.ctx, node, kind, vmid, name, "")
			case "snapRollback":
				upid, err = c.SnapshotRollback(w.ctx, node, kind, vmid, name)
			case "snapDelete":
				upid, err = c.SnapshotDelete(w.ctx, node, kind, vmid, name)
			}
			if err != nil {
				w.opFail(what, err)
				postMain(func() {
					if !dlgClosed.Load() && w.alive() && setBtns != nil {
						setBtns(true)
					}
				})
				return
			}
			w.trackTask(upid, what, func() {
				postMain(func() {
					if !dlgClosed.Load() && w.alive() && setBtns != nil {
						setBtns(true)
					}
				})
				reload() // перечитать список после мутации
			})
		})
	}

	createBtn := qt.NewQPushButton3(i18n.Tr("PVE", "Создать"))
	createBtn.OnClicked(func() {
		name, ok := inputTextOK(list.QWidget, i18n.Tr("PVE", "Новый снапшот"), i18n.Tr("PVE", "Имя снапшота"), "")
		if !ok || strings.TrimSpace(name) == "" {
			return
		}
		if !w.confirmAction(row, "snapCreate") {
			return
		}
		snapOp("snapCreate", strings.TrimSpace(name), i18n.Tr("PVE", "создание снапшота ")+strings.TrimSpace(name))
	})
	rollbackBtn := qt.NewQPushButton3(i18n.Tr("PVE", "Откатить"))
	rollbackBtn.OnClicked(func() {
		name := selected()
		if name == "" {
			w.setStatus(i18n.Tr("PVE", "снапшот не выбран"))
			return
		}
		if !w.confirmAction(row, "snapRollback") {
			return
		}
		snapOp("snapRollback", name, i18n.Tr("PVE", "откат на снапшот ")+name)
	})
	deleteBtn := qt.NewQPushButton3(i18n.Tr("PVE", "Удалить"))
	deleteBtn.OnClicked(func() {
		name := selected()
		if name == "" {
			w.setStatus(i18n.Tr("PVE", "снапшот не выбран"))
			return
		}
		if !w.confirmAction(row, "snapDelete") {
			return
		}
		snapOp("snapDelete", name, i18n.Tr("PVE", "удаление снапшота ")+name)
	})
	closeBtn := qt.NewQPushButton3(i18n.Tr("Dialog", "Закрыть"))
	closeBtn.OnClicked(d.Accept)

	setBtns = func(on bool) {
		createBtn.SetEnabled(on)
		rollbackBtn.SetEnabled(on)
		deleteBtn.SetEnabled(on)
	}
	btns := qt.NewQHBoxLayout2()
	btns.AddWidget(createBtn.QWidget)
	btns.AddWidget(rollbackBtn.QWidget)
	btns.AddWidget(deleteBtn.QWidget)
	btns.AddStretch()
	btns.AddWidget(closeBtn.QWidget)

	box := qt.NewQVBoxLayout2()
	box.AddWidget(list.QWidget)
	box.AddLayout(btns.QLayout)
	d.SetLayout(box.QLayout)

	reload()
	d.Exec()
	dlgClosed.Store(true)
	d.DeleteLater() // ревью A: parented QDialog после Exec живёт скрытым до закрытия вкладки
}

// configDialog — «Показать конфигурацию»: GuestConfig → сортированный текст
// в диалоге только для чтения. Qt-нить (диалог открывается из postMain).
func (w *pveWidget) configDialog(row PveRow) {
	node, kind, vmid := pveRowNode(row), row.Kind, row.VMID
	w.enqueue(func(c *pve.Client) {
		cfg, err := c.GuestConfig(w.ctx, node, kind, vmid)
		if err != nil {
			w.opFail(i18n.Tr("PVE", "конфигурация"), err)
			return
		}
		text := pveConfigText(cfg)
		postMain(func() {
			if !w.alive() {
				return
			}
			d := qt.NewQDialog(w.tree.QWidget)
			d.SetWindowTitle(i18n.Tr("PVE", "Конфигурация: ") + fmt.Sprintf("%s (%d)", row.Name, row.VMID))
			d.SetMinimumSize(qt.NewQSize2(520, 420))
			view := qt.NewQTextEdit2()
			view.SetReadOnly(true)
			view.SetPlainText(text)
			closeBtn := qt.NewQPushButton3(i18n.Tr("Dialog", "Закрыть"))
			closeBtn.OnClicked(d.Accept)
			btns := qt.NewQHBoxLayout2()
			btns.AddStretch()
			btns.AddWidget(closeBtn.QWidget)
			box := qt.NewQVBoxLayout2()
			box.AddWidget(view.QWidget)
			box.AddLayout(btns.QLayout)
			d.SetLayout(box.QLayout)
			d.Exec()
			d.DeleteLater() // parented QDialog после Exec живёт скрытым до закрытия вкладки
		})
	})
}

// backupDialog — «Бэкап…» (vzdump, M45-остаток): хранилище ноды (комбо из
// последнего Resources), режим snapshot (ВМ не останавливается), zstd.
// Подтверждение (PveConfirmCount=1) → Backup → trackTask → обновление.
func (w *pveWidget) backupDialog(row PveRow) {
	node := pveRowNode(row)
	var storages []string
	for _, r := range w.rows {
		if r.Kind == "storage" && pveRowNode(r) == node {
			storages = append(storages, r.Name)
		}
	}
	if len(storages) == 0 {
		w.setStatus(i18n.Tr("PVE", "на ноде нет хранилищ для бэкапа"))
		return
	}
	d := qt.NewQDialog(w.tree.QWidget)
	d.SetWindowTitle(i18n.Tr("PVE", "Бэкап: ") + fmt.Sprintf("%s (%d)", row.Name, row.VMID))
	d.SetMinimumSize(qt.NewQSize2(420, 140))

	form := qt.NewQFormLayout2()
	combo := qt.NewQComboBox2()
	for _, st := range storages {
		combo.AddItem(st)
	}
	form.AddRow3(i18n.Tr("PVE", "Хранилище"), combo.QWidget)
	note := qt.NewQLabel3(i18n.Tr("PVE", "Режим snapshot (гость не останавливается), сжатие zstd. Прогресс — в статусе вкладки."))

	box := qt.NewQVBoxLayout2()
	box.AddLayout(form.QLayout)
	box.AddWidget(note.QWidget)
	dialogButtons(box, d, i18n.Tr("PVE", "Бэкапировать"), func() {
		storage := combo.CurrentText()
		d.Accept()
		if !w.confirmAction(row, "backup") {
			return
		}
		what := pveActionLabel("backup") + " " + strconv.Itoa(row.VMID)
		w.enqueue(func(c *pve.Client) {
			w.setStatus(what + "…")
			upid, err := c.Backup(w.ctx, node, row.VMID, storage, "snapshot")
			if err != nil {
				w.opFail(what, err)
				return
			}
			w.trackTask(upid, what, w.refresh)
		})
	})
	d.SetLayout(box.QLayout)
	d.Exec()
	d.DeleteLater()
}
