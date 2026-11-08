package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M20: состав кластера выводится из конфига — environments{uuid}{cluster} =
// список имён кластеров (легаси PACUtils.pm:2393, PACCluster.pm:703-710);
// группы не входят.
func TestClusterIDs(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		"c1": {Name: "web1", Method: "SSH", Cluster: []string{"prod"}},
		"c2": {Name: "web2", Method: "SSH", Cluster: []string{"prod", "staging"}},
		"g1": {Name: "Веб-серверы", IsGroup: 1, Cluster: []string{"prod"}},
		"c3": {Name: "db", Method: "SSH"},
	}}
	if got := clusterIDs(cfg, "prod"); len(got) != 2 || got[0] != "c1" || got[1] != "c2" {
		t.Fatalf("prod: %v", got)
	}
	if got := clusterIDs(cfg, "staging"); len(got) != 1 || got[0] != "c2" {
		t.Fatalf("staging: %v", got)
	}
	if got := clusterIDs(cfg, "nope"); len(got) != 0 {
		t.Fatalf("nope: %v", got)
	}
}

// M20: fan-вывод клавиш в N терминалов (ядро PCC).
func TestFanSendsAll(t *testing.T) {
	a, b := make(chan []byte, 1), make(chan []byte, 1)
	if dropped := fanIn([]chan<- []byte{a, b}, []byte("whoami")); dropped != 0 {
		t.Fatalf("dropped = %d", dropped)
	}
	if got := string(<-a); got != "whoami" {
		t.Fatalf("a: %q", got)
	}
	if got := string(<-b); got != "whoami" {
		t.Fatalf("b: %q", got)
	}
}

// M20: неблокирующая отправка — полный буфер терминала дропается,
// живые получают (PCC не вешается на троттлинг).
func TestFanDropOnFull(t *testing.T) {
	a := make(chan []byte) // без буфера: никто не читает
	b := make(chan []byte, 1)
	if dropped := fanIn([]chan<- []byte{a, b}, []byte("x")); dropped != 1 {
		t.Fatalf("dropped = %d", dropped)
	}
	if got := string(<-b); got != "x" {
		t.Fatalf("b: %q", got)
	}
}

// M20: реестр живых терминалов PCC.
func TestClusterRegistry(t *testing.T) {
	r := &clusterRegistry{byCluster: map[string][]chan<- []byte{}}
	ca, cb := make(chan []byte, 1), make(chan []byte, 1)
	r.add("prod", ca)
	r.add("prod", cb)
	r.add("staging", ca)

	if got := r.clusters(); len(got) != 2 || got[0] != "prod" || got[1] != "staging" {
		t.Fatalf("clusters: %v", got)
	}
	if dropped := r.send("prod", []byte("ps")); dropped != 0 {
		t.Fatalf("send: %d", dropped)
	}
	if string(<-ca) != "ps" || string(<-cb) != "ps" {
		t.Fatal("не всем доставлено")
	}
	r.remove("prod", ca)
	if dropped := r.send("prod", []byte("ps")); dropped != 0 {
		t.Fatalf("после remove: %d", dropped)
	}
	if string(<-cb) != "ps" {
		t.Fatalf("cb: %q", string(<-cb))
	}
	select {
	case <-ca:
		t.Fatal("удалённый получил ввод")
	default:
	}
}

// M20: неудаление не ломает (idempotent remove).
func TestClusterRegistryRemoveMissing(t *testing.T) {
	r := &clusterRegistry{byCluster: map[string][]chan<- []byte{}}
	r.remove("nope", make(chan []byte))
	if len(r.clusters()) != 0 {
		t.Fatal("дырка после удаления несуществующего")
	}
}
