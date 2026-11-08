package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/session"
	"github.com/DunkanAidaho/asbru-go/internal/ui/wgt"
	qt "github.com/mappu/miqt/qt6"
)

// Regression-тест lifecycle (ревью C1): при ошибке session.New виджет
// обязан быть уничтожен и cancel не должен оставаться в реестре — было:
// defer cancel() в области вызова убивал живые сессии мгновенно.
// Инжектируемые границы: история через замыкания, без реального Qt.
func TestLaunchSessionErrorDestroys(t *testing.T) {
	destroyed := 0
	restore := patchLaunch(func(*wgt.Callback) (int, error) { return 42, nil },
		func(int) {},
		func(int) { destroyed++ },
		func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error) {
			return nil, errors.New("boom")
		})
	defer restore()

	launchConnection("u1", &config.Environment{Name: "t"}, &config.Config{}, "")

	if destroyed != 1 {
		t.Fatalf("Destroy вызван %d раз, хотим 1", destroyed)
	}
	// вкладка добавляется только после успешной сессии (M12-ревью H2) —
	// error-путь не оставляет записей
	tabMu.Lock()
	defer tabMu.Unlock()
	if _, leaked := tabID[42]; leaked {
		t.Fatal("tabID не очищен после ошибки сессии")
	}
}

// Успешный запуск: cancel регистрируется и живёт дольше функции —
// регрессия на точный класс бага C1 (cancel в области вызова).
func TestLaunchSessionKeepsCancel(t *testing.T) {
	restore := patchLaunch(func(*wgt.Callback) (int, error) { return 7, nil },
		func(int) {},
		func(int) {},
		func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error) {
			return &session.Session{
				Out:     make(chan session.Event),
				In:      make(chan []byte, 1),
				Control: make(chan session.ControlMsg, 1),
			}, nil
		})
	defer restore()

	launchConnection("u2", &config.Environment{Name: "t"}, &config.Config{}, "")

	// сессия успешно создана → вкладка зарегистрирована (M12-ревью H1)
	tabMu.Lock()
	defer tabMu.Unlock()
	if _, ok := tabSess[7]; !ok {
		t.Fatal("tabSess не записан для живой сессии")
	}
}

// patchLaunch — временная замена инжектируемых границ запуска.
func patchLaunch(create func(*wgt.Callback) (int, error), show func(int), destroy func(int),
	newS func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error)) func() {
	oc, os, od, on := wgtCreate, wgtShow, wgtDestroy, sessionNew
	wgtCreate, wgtShow, wgtDestroy, sessionNew = create, show, destroy, newS
	return func() { wgtCreate, wgtShow, wgtDestroy, sessionNew = oc, os, od, on }
}

// M12-ревью (минимум покрытия): removeTab идемпотентен — двойной вызов
// чистит карты и Destroy вызывается один раз.
func TestRemoveTabIdempotent(t *testing.T) {
	destroyed := 0
	restore := patchLaunch(func(*wgt.Callback) (int, error) { return 9, nil },
		func(int) {},
		func(int) { destroyed++ },
		func(context.Context, session.BuildEnv, *config.Environment, ...session.Option) (*session.Session, error) {
			return &session.Session{}, nil
		})
	defer restore()

	// чистим глобальные карты от других тестов (запускаются статeльно пакетом)
	tabMu.Lock()
	clear(tabID)
	clear(tabSess)
	clear(tabUUID)
	tabID[9] = qt.UnsafeNewQWidget(nil)
	tabSess[9] = &session.Session{}
	tabMu.Unlock()

	removeTab(9)
	removeTab(9) // второй вызов — no-op
	if destroyed != 1 {
		t.Fatalf("Destroy: %d раз, хотим 1 (идемпотентность)", destroyed)
	}
	tabMu.Lock()
	defer tabMu.Unlock()
	if len(tabID) != 0 || len(tabSess) != 0 {
		t.Fatalf("карты не очищены: %v", tabID)
	}
}

// M29-ревью I2: shell binary/options из defaults попадают в BuildEnv, иначе
// Local Shell игнорировал настроенный shell (cmdline.go:102-110).
func TestNewBuildEnvShellWiring(t *testing.T) {
	d := &config.Defaults{Raw: map[string]any{
		"shell binary":  "/usr/bin/zsh",
		"shell options": "-login",
	}}
	be, err := newBuildEnv("u1", &config.Environment{Name: "s"}, d)
	if err != nil {
		t.Fatal(err)
	}
	if be.ShellBinary != "/usr/bin/zsh" || be.ShellOptions != "-login" {
		t.Fatalf("BuildEnv shell = %q/%q, want /usr/bin/zsh / -login", be.ShellBinary, be.ShellOptions)
	}
	// пустые defaults → fallback (cmdline сам подставит $SHELL//bin/bash).
	be, err = newBuildEnv("u1", &config.Environment{Name: "s"}, &config.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if be.ShellBinary != "" || be.ShellOptions != "" {
		t.Fatalf("BuildEnv shell = %q/%q, want пусто", be.ShellBinary, be.ShellOptions)
	}
	// nil defaults → не падаем.
	if _, err := newBuildEnv("u1", &config.Environment{Name: "s"}, nil); err != nil {
		t.Fatalf("nil defaults: %v", err)
	}
}

// M30: SudoPrompt в BuildEnv берётся из defaults 'sudo prompt', с дефолтом.
func TestNewBuildEnvSudoPrompt(t *testing.T) {
	d := &config.Defaults{SudoPrompt: "[my-sudo]"}
	be, err := newBuildEnv("u1", &config.Environment{Name: "s"}, d)
	if err != nil {
		t.Fatal(err)
	}
	if be.SudoPrompt != "[my-sudo]" {
		t.Fatalf("SudoPrompt = %q, want [my-sudo]", be.SudoPrompt)
	}
	// пустой defaults → дефолт из config.DefaultSudoPrompt.
	be, err = newBuildEnv("u1", &config.Environment{Name: "s"}, &config.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if be.SudoPrompt != config.DefaultSudoPrompt {
		t.Fatalf("SudoPrompt = %q, want %q", be.SudoPrompt, config.DefaultSudoPrompt)
	}
}

// detachDeadSession — завершившаяся сессия убирается из карт, но вкладка
// (tabID/tabUUID) остаётся: пользователь читает причину и может перезапустить.
func TestDetachDeadSessionClearsSessionMaps(t *testing.T) {
	ch := make(chan []byte, 1)
	tabMu.Lock()
	tabID[401] = nil
	tabUUID[401] = "u1"
	tabSess[401] = &session.Session{}
	tabIn[401] = ch
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabID, 401)
		delete(tabUUID, 401)
		delete(tabSess, 401)
		delete(tabIn, 401)
		delete(tabClu, 401)
		tabMu.Unlock()
	}()

	detachDeadSession(401)

	tabMu.Lock()
	_, sess := tabSess[401]
	_, in := tabIn[401]
	_, clu := tabClu[401]
	_, tab := tabID[401]
	uuid, uok := tabUUID[401]
	tabMu.Unlock()
	if sess || in || clu {
		t.Fatal("мёртвая сессия осталась в tabSess/tabIn/tabClu")
	}
	if !tab || uuid != "u1" || !uok {
		t.Fatal("вкладка (tabID/tabUUID) должна остаться для перезапуска")
	}
}

// Restart на мёртвой сессии (pre-CONNECT ошибка / обрыв при closeOn=off):
// зомби-вкладка закрывается, соединение запускается заново.
func TestRestartTermRelaunchesDeadSession(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"u1": {Name: "c", Method: "SSH"},
	}}
	oldCfg, oldPath := curCfg, curCfgPath
	curCfg, curCfgPath = cfg, "/tmp/gt-test-config.yml"
	defer func() { curCfg, curCfgPath = oldCfg, oldPath }()

	created := 0
	restore := patchLaunch(func(*wgt.Callback) (int, error) {
		created++
		return 0, errors.New("тест: без Qt")
	}, func(int) {}, func(int) {}, nil)
	defer restore()

	tabMu.Lock()
	tabID[402] = nil
	tabUUID[402] = "u1"
	delete(tabSess, 402)
	delete(tabIn, 402)
	delete(tabClu, 402)
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabID, 402)
		delete(tabUUID, 402)
		delete(tabSess, 402)
		tabMu.Unlock()
	}()

	restartTerm(402)

	tabMu.Lock()
	_, alive := tabID[402]
	tabMu.Unlock()
	if alive {
		t.Fatal("зомби-вкладка не закрыта при перезапуске")
	}
	if created != 1 {
		t.Fatalf("соединение не перезапущено: wgtCreate вызван %d раз", created)
	}
}

// Restart на живой сессии — по-прежнему ControlRestart (без нового запуска).
func TestRestartTermLiveSessionUsesControl(t *testing.T) {
	ch := make(chan session.ControlMsg, 1)
	tabMu.Lock()
	tabID[403] = nil
	tabSess[403] = &session.Session{Control: ch}
	tabMu.Unlock()
	defer func() {
		tabMu.Lock()
		delete(tabID, 403)
		delete(tabSess, 403)
		tabMu.Unlock()
	}()

	restartTerm(403)
	select {
	case msg := <-ch:
		if msg.Kind != session.ControlRestart {
			t.Fatalf("получено %v, хотим ControlRestart", msg.Kind)
		}
	default:
		t.Fatal("ControlRestart не отправлен живой сессии")
	}
}
