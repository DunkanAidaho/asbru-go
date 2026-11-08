package ui

// M32: PACCluster — управление членством соединений в кластерах (config-level).

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestClusterMembershipEdit(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"a": {Name: "web", Cluster: config.ClusterNames{}},
		"b": {Name: "db", IsGroup: 1, Cluster: config.ClusterNames{}},
	}}
	addClusterMember(cfg, "a", "prod")
	if !clusterHas(cfg.Environments["a"].Cluster, "prod") {
		t.Fatal("не добавилось в prod")
	}
	addClusterMember(cfg, "a", "prod") // дедуп
	if len(cfg.Environments["a"].Cluster) != 1 {
		t.Fatalf("дубликат: %v", cfg.Environments["a"].Cluster)
	}
	addClusterMember(cfg, "b", "prod") // группы не членят кластеров
	if len(cfg.Environments["b"].Cluster) != 0 {
		t.Fatalf("группа добавлена: %v", cfg.Environments["b"].Cluster)
	}
	// неизвестный uuid
	addClusterMember(cfg, "ghost", "prod")
	if _, ok := cfg.Environments["ghost"]; ok {
		t.Fatal("ghost добавлен")
	}

	removeClusterMember(cfg, "a", "prod")
	if len(cfg.Environments["a"].Cluster) != 0 {
		t.Fatalf("не удалилось: %v", cfg.Environments["a"].Cluster)
	}
	removeClusterMember(cfg, "a", "prod") // no-op
}

func TestAllClusters(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"a": {Cluster: config.ClusterNames{"prod", "staging"}},
		"b": {Cluster: config.ClusterNames{"prod"}, IsGroup: 0},
		"c": {Name: "none", IsGroup: 0},
	}}
	if got := allClusters(cfg); !reflect.DeepEqual(got, []string{"prod", "staging"}) {
		t.Fatalf("allClusters=%v want [prod staging]", got)
	}
}
