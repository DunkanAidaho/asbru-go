package ui

// M39-fullscreen: «во весь экран» для ВСТРОЕННЫХ RDP/VNC-клиентов.
//
// Легаси-токены (`/f`/`-f` для RDP, `-fullscreen`/`-FullScreen=N` для VNC)
// исторически разворачивали ВНЕШНИЙ клиент. Встроенный клиент (M33) рисует
// сессию Qt-канвасом внутри вкладки, поэтому токен раньше был мёртвым. Здесь он
// получает смысл: на весь экран разворачивается САМА ВКЛАДКА — главное окно
// уходит в ShowFullScreen, боковая панель и полоса вкладок прячутся, канвас
// занимает экран. Канвас при этом ОСТАЁТСЯ страницей QTabWidget (ничего не
// пере-парентим), поэтому resizeEvent/DISP (динамическое разрешение) и вся
// раскладка вкладок продолжают работать.
//
// Выход — одиночный Esc: C++-канвас в полноэкранном режиме не отправляет его
// серверу, а вызывает Callback.Fullscreen → exitEmbedFullscreen. Тот же режим
// переключает действие хоткея «fullscreen» для текущей встроенной вкладки
// (см. hotkeys), так что «существующий хоткей» тоже работает — когда фокус не
// в канвасе (канвас по устройству M33 съедает привязки окна, см. rdpview.cpp).

import (
	"sync/atomic"

	qt "github.com/mappu/miqt/qt6"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/embed"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt/rdpview"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt/vncview"
)

// sidePanel/mainSplit — боковая панель и сплиттер главного окна (ставятся в
// Run). Нужны, чтобы спрятать/вернуть chrome вокруг полноэкранного канваса,
// не перестраивая раскладку вкладок.
var (
	sidePanel *qt.QWidget
	mainSplit *qt.QSplitter
)

// embedFullID — id вкладки, развёрнутой на весь экран (0 — режим выключен).
// atomic: вход/выход делает Qt-нить, а removeTab может прийти и из worker-нити
// клиента (embedDone) — проверка id должна быть потокобезопасной.
var embedFullID atomic.Int64

// embedFullPrev — состояние chrome до входа в полноэкранный режим. Пишется на
// Qt-нити ДО Store(embedFullID), читается ПОСЛЕ Swap(0) — тоже на Qt-нити,
// поэтому отдельной блокировки не нужно (видимость обеспечивает atomic).
var embedFullPrev embedPrevChrome

// embedPrevChrome — что вернуть при выходе из полноэкранного режима.
type embedPrevChrome struct {
	leftVisible   bool
	tabBarVisible bool
	wasFullscreen bool
	wasMaximized  bool
	splitSizes    []int
}

// embedWantsFullscreen — просит ли соединение полноэкранный режим. Решение
// принимается по существующим легаси-токенам (RDP: «/f», «/fullscreen», «-f»;
// VNC: «-fullscreen», «-FullScreen=N»), новых токенов не вводим.
func embedWantsFullscreen(kind EmbedKind, env *config.Environment) bool {
	if env == nil {
		return false
	}
	switch kind {
	case EmbedRDP:
		return embed.RDPParamsFrom(env, env.Options).Fullscreen
	case EmbedVNC:
		return embed.VNCParamsFrom(env).Fullscreen
	}
	return false
}

// setEmbedEsc — сообщить C++-канвасу, что вкладка в полноэкранном режиме: пока
// включено, одиночный Esc — локальный выход, а не клавиша сессии. Только Qt-нить.
func setEmbedEsc(kind EmbedKind, id int, on bool) {
	switch kind {
	case EmbedRDP:
		rdpview.SetFullscreen(id, on)
	case EmbedVNC:
		vncview.SetFullscreen(id, on)
	}
}

// enterEmbedFullscreen разворачивает вкладку id на весь экран (Qt-нить).
// Идемпотентно: если полноэкранная вкладка уже есть — no-op (вызывающий сперва
// закрывает чужой режим через exitEmbedFullscreen).
func enterEmbedFullscreen(id int) {
	if mainWindow == nil || !uiLive.Load() {
		return
	}
	if embedFullID.Load() != 0 {
		return
	}
	tabMu.Lock()
	wrap, ok := tabID[id]
	kind := tabEmbed[id]
	tabMu.Unlock()
	if !ok || wrap == nil || (kind != EmbedRDP && kind != EmbedVNC) {
		return // PVE/терминал полноэкранного режима канваса не имеют
	}
	prev := embedPrevChrome{
		// IsHidden(), а не IsVisible(): окно может быть свёрнуто/не показано в
		// момент запуска, и IsVisible() ложно вернул бы «панель скрыта» — после
		// выхода она осталась бы спрятанной. IsHidden() — это ЯВНОЕ скрытие.
		leftVisible:   sidePanel == nil || !sidePanel.IsHidden(),
		tabBarVisible: mainTabs == nil || !mainTabs.TabBar().IsHidden(),
		wasFullscreen: mainWindow.IsFullScreen(),
		wasMaximized:  mainWindow.IsMaximized(),
	}
	if mainSplit != nil {
		prev.splitSizes = mainSplit.Sizes()
	}
	// Порядок важен: сперва запоминаем состояние, затем объявляем режим, чтобы
	// removeTab/выход не увидели «id есть, prev пуст».
	embedFullPrev = prev
	embedFullID.Store(int64(id))
	setEmbedEsc(kind, id, true)
	if sidePanel != nil {
		sidePanel.SetVisible(false)
	}
	if mainTabs != nil {
		mainTabs.TabBar().SetVisible(false)
	}
	mainWindow.ShowFullScreen()
	wrap.SetFocus()
}

// exitEmbedFullscreen возвращает обычную раскладку (Qt-нить). No-op, если режим
// не активен. Вызывается по Esc, хоткею и при закрытии вкладки.
func exitEmbedFullscreen() {
	id := int(embedFullID.Swap(0))
	if id == 0 {
		return
	}
	prev := embedFullPrev
	tabMu.Lock()
	wrap := tabID[id]
	kind := tabEmbed[id]
	tabMu.Unlock()
	if wrap != nil {
		setEmbedEsc(kind, id, false)
	}
	if mainWindow != nil {
		switch {
		case prev.wasFullscreen:
			mainWindow.ShowFullScreen()
		case prev.wasMaximized:
			mainWindow.ShowMaximized()
		default:
			mainWindow.ShowNormal()
		}
	}
	if sidePanel != nil {
		sidePanel.SetVisible(prev.leftVisible)
	}
	if mainTabs != nil {
		mainTabs.TabBar().SetVisible(prev.tabBarVisible)
	}
	if mainSplit != nil && len(prev.splitSizes) == 2 {
		mainSplit.SetSizes(prev.splitSizes)
	}
	if wrap != nil {
		wrap.SetFocus()
	}
}

// toggleEmbedFullscreen — переключатель для хоткея «fullscreen» и вызова из
// меню (Qt-нить). Если развёрнута ДРУГАЯ вкладка — сперва выходим из неё.
func toggleEmbedFullscreen(id int) {
	if embedFullID.Load() == int64(id) {
		exitEmbedFullscreen()
		return
	}
	if embedFullID.Load() != 0 {
		exitEmbedFullscreen()
	}
	enterEmbedFullscreen(id)
}

// exitEmbedFullscreenFor — выход по Esc из канваса (callback C++). Приходит на
// Qt-нити, но остаётся безопасным из любой (mainthreadW сам разбирается).
func exitEmbedFullscreenFor(id int) {
	if !uiLive.Load() {
		return
	}
	mainthreadW(func() {
		if embedFullID.Load() == int64(id) {
			exitEmbedFullscreen()
		}
	})
}

// embedMaxSidePx — предел стороны фреймбуфера встроенного клиента (совпадает с
// kMaxSidePx в rdpview.cpp). Экран крупнее не должен давать стартовый размер,
// который клиент потом молча отбросит как «слишком большой кадр».
const embedMaxSidePx = 8192

// embedScreenSize — физический размер экрана (логический geometry × DPR).
// Это стартовое разрешение RDP-сессии в полноэкранном режиме — аналог
// `xfreerdp /f` (сессия открывается по размеру экрана, а не по размеру
// вкладки). Только Qt-нить; (0,0) — размер неизвестен (без экрана/в тестах).
func embedScreenSize() (int, int) {
	if mainWindow == nil {
		return 0, 0
	}
	sc := mainWindow.Screen()
	if sc == nil {
		return 0, 0
	}
	g := sc.Geometry()
	dpr := sc.DevicePixelRatio()
	if dpr <= 0 {
		dpr = 1
	}
	w := int(float64(g.Width())*dpr + 0.5)
	h := int(float64(g.Height())*dpr + 0.5)
	if w > embedMaxSidePx {
		w = embedMaxSidePx
	}
	if h > embedMaxSidePx {
		h = embedMaxSidePx
	}
	return w, h
}
