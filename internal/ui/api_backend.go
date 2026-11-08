package ui

// M32 PACScripts API-бэкенд: реализация scripts.API поверх живых сессий
// (Connect/StopConn/Send/OpenConn). Действия, затрагивающие Qt-виджеты /
// запуск соединений, диспатчатся на Qt-нить (mainthreadW); Send — канал
// сессии (потокобезопасен) без Qt.

import (
	"errors"
	"log/slog"
	"sort"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// apiBackend — привязка scripts.API к живым сессиям из UI-слоя.
type apiBackend struct {
	cfg *config.Config
}

// tabIDsForUUID — id вкладок, чья сессия соответствует uuid (под tabMu),
// детерминированно (по id).
func tabIDsForUUID(uuid string) []int {
	tabMu.Lock()
	defer tabMu.Unlock()
	var ids []int
	for id, u := range tabUUID {
		if u == uuid {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// Connect — подключить соединение по UUID (Qt-нить). Чтение cfg.Environments
// выполняется ВНУТРИ mainthreadW: карту мутирует UI на Qt-нити, чтение из
// горутины диспетчера — гонка (как в OpenConn).
func (a *apiBackend) Connect(uuid string) error {
	if a == nil || a.cfg == nil {
		return errors.New("ui: нет конфигурации")
	}
	var err error
	mainthreadW(func() {
		env, ok := a.cfg.Environments[uuid]
		if !ok || env.IsGroup == 1 {
			err = errors.New("ui: соединение не найдено")
			return
		}
		launchConnection(uuid, &env, a.cfg, firstCluster(env.Cluster))
	})
	return err
}

// StopConn — закрыть все вкладки сессии этого соединения (Qt-нить).
func (a *apiBackend) StopConn(uuid string) error {
	ids := tabIDsForUUID(uuid)
	if len(ids) == 0 {
		return errors.New("ui: нет активной сессии")
	}
	mainthreadW(func() {
		for _, id := range ids {
			disconnectTerm(id)
		}
	})
	return nil
}

// Send — отправить текст в активную вкладку сессии (канал, без Qt).
func (a *apiBackend) Send(uuid, text string) error {
	ids := tabIDsForUUID(uuid)
	if len(ids) == 0 {
		return errors.New("ui: нет активной сессии")
	}
	// активная вкладка определяется НА Qt-нити (mainTabs — Qt-виджет),
	// т.к. Send вызывается из горутины-диспетчера (off-Qt). Петля свободна
	// (RunAPI — в горутине), поэтому mainthreadW не дедлочит.
	var id int
	mainthreadW(func() { id = activeTabAmong(ids) })
	tabMu.Lock()
	s := tabSess[id]
	tabMu.Unlock()
	if s == nil || s.In == nil {
		// nil-канал в select с default всегда даёт default → скрипт получил бы
		// ложное «отправлено». Явно сообщаем, что сессия не готова (BACKLOG:35).
		return errors.New("ui: сессия не готова")
	}
	select {
	case s.In <- []byte(text):
		return nil
	default:
		// как cb.Data: переполненный канал — лог-дроп, не ошибка скрипту.
		slog.Warn("PACScripts: ввод отброшен (канал сессии переполнен)", "uuid", uuid)
		return nil
	}
}

// activeTabAmong — активная (текущая вкладка mainTabs) среди ids, иначе первая.
// Вызывается ТОЛЬКО на Qt-ните (из Send через mainthreadW). tabID читается под
// tabMu; w может быть nil (вкладка закрыта) — null-deref guard.
func activeTabAmong(ids []int) int {
	if mainTabs == nil {
		return ids[0]
	}
	idx := mainTabs.CurrentIndex()
	for _, id := range ids {
		tabMu.Lock()
		w := tabID[id]
		ci := -1
		if w != nil {
			ci = mainTabs.IndexOf(w)
		}
		tabMu.Unlock()
		if ci == idx {
			return id
		}
	}
	return ids[0]
}

// OpenConn — открыть все избранные соединения (Qt-нить).
func (a *apiBackend) OpenConn() error {
	if a == nil || a.cfg == nil {
		return errors.New("ui: нет конфигурации")
	}
	mainthreadW(func() {
		for _, f := range favouriteEntries(a.cfg.Environments) {
			env := a.cfg.Environments[f.UUID]
			if env.IsGroup != 1 {
				launchConnection(f.UUID, &env, a.cfg, "")
			}
		}
	})
	return nil
}
