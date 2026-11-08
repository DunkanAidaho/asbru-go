package ui

// Консоль гостя PVE (Task 10): «Консоль» меню дерева PVE-вкладки (только
// RUNNING qemu — lxc через vncproxy консоли не имеет, см. PveMenuAllowed) →
// POST /nodes/{node}/qemu/{vmid}/vncproxy → вкладка vncview, как обычное
// VNC-подключение, но порт и пароль — из одноразового ответа vncproxy.
//
// Потоки: pveOpenConsole зовётся из меню дерева (Qt-нить), сетевой шаг уходит
// в горутину; возврат в UI — только через postMain; vncview.Start (НЕ
// Qt-нить) порождается горутиной ИЗ postMain-замыкания, чтобы Create
// гарантированно выполнился раньше старта клиента.
//
// РИСК (честная пометка из плана, проверяется живым сервером): security
// "none" здесь — запрет VeNCrypt, при этом включается КЛАССИЧЕСКАЯ
// парольная аутентификация VNC-протокола, а она обрезает пароль до 8 байт
// (vncauth DES), тикет vncproxy длиннее. Работоспособность входа по такому
// тикету НЕ гарантирована без живого сервера; если при живой проверке сервер
// отвергнет авторизацию — fallback: websocket-транспорт noVNC (задел в
// BACKLOG). Проверит пользователь на тестовой ВМ.

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/pve"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt/vncview"
	qt "github.com/mappu/miqt/qt6"
)

// Швы для тестов (по образцу stopEmbedFn/destroyEmbedFn в ui.go): тесты идут
// без Qt/cgo и без живых серверов.
var (
	// pveVncProxyFn — сетевой шаг ЦЕЛИКОМ (логин + vncproxy): свежему клиенту
	// нужен явный Login — авто-релогин клиента срабатывает только ПОСЛЕ
	// первого входа (пустым user/pass перелогиниться нельзя).
	pveVncProxyFn = func(ctx context.Context, c *pve.Client, user, pass, node string, vmid int) (pve.VNCProxy, error) {
		if err := c.Login(user, pass); err != nil {
			return pve.VNCProxy{}, err
		}
		return c.VNCProxy(ctx, node, vmid)
	}
	// pveVncCreateFn/pveVncStartFn — cgo-мост vncview (в тестах — фейки).
	pveVncCreateFn = vncview.Create
	pveVncStartFn  = vncview.Start
	// pveTermProxyFn — LXC-консоль (termproxy): тот же контракт {port, ticket}.
	pveTermProxyFn = func(ctx context.Context, c *pve.Client, user, pass, node string, vmid int) (pve.VNCProxy, error) {
		if err := c.LoginCtx(ctx, user, pass); err != nil {
			return pve.VNCProxy{}, err
		}
		return c.TermProxy(ctx, node, vmid)
	}
	// pveSetConnStatus — шов статуса: при uiLive=false настоящий
	// setConnStatus уходит в ранний return, и тест его не увидит.
	pveSetConnStatus = setConnStatus
)

// pveOpenConsole — консоль RUNNING qemu: vncproxy → вкладка vncview.
// Ошибка vncproxy — красный индикатор на вкладке управления PVE, вкладка
// консоли не создаётся (нечего показывать без порта/тикета).
func pveOpenConsole(uuid string, env *config.Environment, node, kind string, vmid int, guest string) {
	if env == nil {
		return
	}
	go func() {
		client := newPveClient(pveBase(env.IP, pvePort(env.Port)), !pveTlsVerify(env.Options), pveTimeout)
		defer client.Close() // ревью 3: транспорт не копится
		ctx, cancel := context.WithTimeout(context.Background(), pveTimeout)
		defer cancel()
		var (
			p   pve.VNCProxy
			err error
		)
		if kind == "lxc" {
			// M45-остаток: терминал LXC PVE оборачивает в VNC (termproxy) —
			// тот же канвас, ticket = VNC-пароль.
			p, err = pveTermProxyFn(ctx, client, env.User, env.Pass, node, vmid)
		} else {
			p, err = pveVncProxyFn(ctx, client, env.User, env.Pass, node, vmid)
		}
		if err != nil {
			slog.Warn("pve: консоль гостя", "kind", kind, "vmid", vmid, "node", node, "err", err)
			postMain(func() {
				if id := pveMgmtTabID(uuid); id != 0 {
					pveSetConnStatus(id, connError, i18n.Tr("Status", "ошибка: ")+err.Error())
				}
			})
			return
		}
		openPveConsoleTab(uuid, guest, node, p)
	}()
}

// pveMgmtTabID — id PVE-вкладки управления соединения (куда выводить ошибку
// консоли); 0 — вкладка уже закрыта (статус некуда выводить).
func pveMgmtTabID(uuid string) int {
	tabMu.Lock()
	defer tabMu.Unlock()
	best := 0
	for id, kind := range tabEmbed {
		if kind == EmbedPVE && tabUUID[id] == uuid {
			// Ревью 3 (Minor): итерация карты недетерминирована — при двух
			// вкладках одного uuid берём меньший id (первая открытая).
			if best == 0 || id < best {
				best = id
			}
		}
	}
	return best
}

// openPveConsoleTab — вкладка с канвасом гостя: Create (Qt-нить) →
// регистрация карт → вкладка/фокус → Start. Done — штатный embedDone: он же
// убирает вкладку и по крестику (tabCloseReq), и при неудачном подключении
// клиент завершается сам (без отдельной stop-ветки — в отличие от PVE-виджета,
// у vncview C++-Done есть).
func openPveConsoleTab(uuid, guest, host string, p pve.VNCProxy) {
	title := guest + i18n.Tr("PVE", " — консоль")
	var (
		id  int
		err error
	)
	var connectedOnce sync.Once
	postMain(func() {
		id, err = pveVncCreateFn(&vncview.Callback{
			Frame: func(w, h int) {
				connectedOnce.Do(func() {
					pveSetConnStatus(id, connConnected, i18n.Tr("Status", "подключено"))
					appHistory.Push(uuid, time.Now())
				}) // А2 #1: историю
			},
			Done: func(ok bool, reason string) {
				embedDone(uuid, title, id, ok, reason)
			},
		})
		if err != nil {
			// Ревью 2 (Minor): провал Create молчал — консоль «не открылась»
			// без сигнала (в отличие от ошибки vncproxy).
			slog.Error("pve: vncview.Create", "err", err)
			postMain(func() {
				if id := pveMgmtTabID(uuid); id != 0 {
					pveSetConnStatus(id, connError, i18n.Tr("Status", "ошибка: ")+err.Error())
				}
			})
			return
		}
		var wrap *qt.QWidget
		if uiLive.Load() {
			// как в launchEmbedded: wrap — САМ канвас; в тестах без Qt — nil
			// (lifecycle-тесты кладут nil и не трогают Qt).
			wrap = qt.UnsafeNewQWidget(vncview.Native(id))
		}
		tabMu.Lock()
		tabID[id] = wrap
		tabUUID[id] = uuid
		tabEmbed[id] = EmbedVNC
		tabName[id] = title
		tabLog[id] = newSessionLogBuf()
		tabMu.Unlock()
		if uiLive.Load() {
			mainTabs.AddTab(wrap, title)
			mainTabs.SetCurrentWidget(wrap)
			wrap.SetFocus() // фокус сразу в консоль (M39-ревью MAJOR-1)
		}
		pveSetConnStatus(id, connConnecting, i18n.Tr("Status", "подключение…"))
		// Start — не Qt-нить: уходим горутиной из замыкания (Create к этому
		// моменту уже выполнен и колбэки зарегистрированы).
		go pveVncStartFn(id, host, p.Port, "", p.Ticket, title, "none")
	})
}
