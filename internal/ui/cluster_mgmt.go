package ui

// M32: PACCluster — управление членством соединений в кластерах (config-level).
// Добавление/удаление соединения в кластер, перечень кластеров. Qt-диалог —
// тонкая плёнка поверх этих чистых функций.

import (
	"sort"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// allClusters — отсортированные имена кластеров из конфига.
func allClusters(cfg *config.Config) []string {
	set := map[string]bool{}
	for _, e := range cfg.Environments {
		for _, c := range e.Cluster {
			if c != "" {
				set[c] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// addClusterMember — добавляет uuid в кластер name (дедуп; группы не членят,
// неизвестный uuid игнорируется).
func addClusterMember(cfg *config.Config, uuid, name string) {
	if name == "" {
		return
	}
	e, ok := cfg.Environments[uuid]
	if !ok || e.IsGroup == 1 {
		return
	}
	for _, c := range e.Cluster {
		if c == name {
			return
		}
	}
	e.Cluster = append(e.Cluster, name)
	cfg.Environments[uuid] = e
}

// removeClusterMember — убирает uuid из кластера name (no-op при отсутствии;
// группы не членят — как в addClusterMember, M32-ревью симметрия).
func removeClusterMember(cfg *config.Config, uuid, name string) {
	e, ok := cfg.Environments[uuid]
	if !ok || e.IsGroup == 1 {
		return
	}
	var out config.ClusterNames
	for _, c := range e.Cluster {
		if c != name {
			out = append(out, c)
		}
	}
	e.Cluster = out
	cfg.Environments[uuid] = e
}
