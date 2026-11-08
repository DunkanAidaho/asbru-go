package ui

// M32: панель статистики — форматирование накопленных stats.Store + Qt-виджет.

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/DunkanAidaho/asbru-go/internal/history"
	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/stats"
	qt "github.com/mappu/miqt/qt6"
)

// appStats — глобальная статистика подключений (M32): передаётся сессиям через
// session.WithStats(appStats) в launchConnection и отображается панелью.
var appStats = stats.NewStore()

// appHistory — глобальная история последних подключений (#1, панель History).
// Push-ится в launchConnection при успешном старте сессии.
var appHistory = history.NewStore(200)

// M10: персист статистики/истории (легаси PACStatistics readStats/saveStats —
// nfreeze; здесь JSON). Файлы лежат в каталоге конфига рядом с asbru.yml.
func statsDir(cfgPath string) string    { return filepath.Dir(cfgPath) }
func statsPath(cfgPath string) string   { return filepath.Join(statsDir(cfgPath), "stats.json") }
func historyPath(cfgPath string) string { return filepath.Join(statsDir(cfgPath), "history.json") }

// loadStatsHistory — чтение накопленной статистики/истории при старте (M10).
func loadStatsHistory(cfgPath string) {
	if err := appStats.Load(statsPath(cfgPath)); err != nil {
		slog.Warn("статистика: не загружена", "err", err)
	}
	if err := appHistory.Load(historyPath(cfgPath)); err != nil {
		slog.Warn("история: не загружена", "err", err)
	}
}

// saveStatsHistory — запись статистики/истории при выходе (M10). Ошибки не
// роняют приложение (каталог может быть readonly при --readonly).
func saveStatsHistory(cfgPath string) {
	if err := appStats.Save(statsPath(cfgPath)); err != nil {
		slog.Warn("статистика: не сохранена", "err", err)
	}
	if err := appHistory.Save(historyPath(cfgPath)); err != nil {
		slog.Warn("история: не сохранена", "err", err)
	}
}

// formatDuration — секунды → "1d 1h" / "1h 1m" / "1m 30s" / "Ns".
// formatDuration — секунды → "1d 1h" / "1h 1m" / "1m 30s" / "Ns"; младший
// нулевой разряд опускается (86400 → "1d", 3600 → "1h").
func formatDuration(sec int) string {
	if sec < 0 {
		sec = 0
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	switch {
	case d > 0:
		if h > 0 {
			return fmt.Sprintf("%dd %dh", d, h)
		}
		return fmt.Sprintf("%dd", d)
	case h > 0:
		if m > 0 {
			return fmt.Sprintf("%dh %dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	case m > 0:
		if s > 0 {
			return fmt.Sprintf("%dm %ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// statsLine — строка статистики соединения.
func statsLine(c stats.ConnStats) string {
	return fmt.Sprintf(i18n.Tr("Stats", "Подключений: %d, Время: %s"), c.TotalConnections, formatDuration(int(c.TotalTime)))
}

// showStats — панель статистики: список uuid → Подключений/Время.
func showStats(st *stats.Store, parent *qt.QWidget) {
	d := qt.NewQDialog2()
	d.SetWindowTitle(i18n.Tr("Stats", "Статистика соединений"))
	d.SetMinimumWidth(420)

	tree := qt.NewQTreeWidget2()
	tree.SetHeaderLabel(i18n.Tr("Stats", "Соединение"))

	snap := st.Snapshot()
	ids := make([]string, 0, len(snap))
	for id := range snap {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		it := qt.NewQTreeWidgetItem()
		it.SetText(0, id+"  —  "+statsLine(snap[id]))
		tree.AddTopLevelItem(it)
	}

	close := qt.NewQPushButton3(i18n.Tr("Stats", "Закрыть"))
	close.OnClicked(d.Reject)
	btnRow := qt.NewQHBoxLayout2()
	btnRow.AddStretch()
	btnRow.AddWidget(close.QWidget)

	box := qt.NewQVBoxLayout2()
	box.AddWidget(tree.QWidget)
	box.AddLayout(btnRow.QLayout)
	d.SetLayout(box.QLayout)
	d.Exec()
}
