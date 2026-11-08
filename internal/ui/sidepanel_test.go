package ui

import (
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/history"
)

// #1: historyPanelEntries — uuid→имя, новейшие сверху; nil store → nil.
func TestHistoryPanelEntries(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"u1": {Name: "web"},
		"u2": {Name: "db"},
	}}
	store := history.NewStore(100)
	store.Push("u1", time.Unix(100, 0))
	store.Push("u2", time.Unix(200, 0)) // новее — сверху
	got := historyPanelEntries(store, cfg)
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].UUID != "u2" || got[0].Name != "db" {
		t.Fatalf("первая (новейшая) = %+v", got[0])
	}
	if got[1].UUID != "u1" || got[1].Name != "web" {
		t.Fatalf("вторая = %+v", got[1])
	}
	// nil store → nil
	if historyPanelEntries(nil, cfg) != nil {
		t.Fatal("nil store → должны быть nil")
	}
	// cfg nil → имя = uuid
	got2 := historyPanelEntries(store, nil)
	if len(got2) != 2 || got2[0].Name != "u2" {
		t.Fatalf("cfg nil: %+v", got2)
	}
	// пустое хранилище → nil
	if historyPanelEntries(history.NewStore(10), cfg) != nil {
		t.Fatal("пустое хранилище → nil")
	}
}

// #1: clusterPanelEntries — кластеры + участники из конфига; пустые отбрасываются.
func TestClusterPanelEntries(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"c1": {Name: "web1", Cluster: config.ClusterNames{"prod"}},
		"c2": {Name: "web2", Cluster: config.ClusterNames{"prod"}},
		"g1": {Name: "group", IsGroup: 1, Cluster: config.ClusterNames{"prod"}}, // группа — не участник
		"x1": {Name: "web3", Cluster: config.ClusterNames{"dev"}},
	}}
	got := clusterPanelEntries(cfg, []string{"prod", "dev", ""})
	if len(got) != 2 {
		t.Fatalf("len = %d, got %+v", len(got), got)
	}
	if got[0].Name != "prod" || got[0].Count != 2 {
		t.Fatalf("prod: %+v", got[0])
	}
	if got[1].Name != "dev" || got[1].Count != 1 {
		t.Fatalf("dev: %+v", got[1])
	}
	// nil cfg → участников нет → кластер отбрасывается (Count==0 skip).
	if got2 := clusterPanelEntries(nil, []string{"prod"}); len(got2) != 0 {
		t.Fatalf("nil cfg: %+v", got2)
	}
}
