package pve

import (
	"context"
	"net/http"
	"testing"
)

func TestResourcesParse(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/resources" {
			t.Errorf("неожиданный путь %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":[
			{"id":"node/pve1","type":"node","node":"pve1","status":"online"},
			{"id":"qemu/101","type":"qemu","node":"pve1","vmid":101,"name":"web1","status":"running","tags":"prod;web"},
			{"id":"lxc/200","type":"lxc","node":"pve1","vmid":"200","name":"dns","status":"stopped"},
			{"id":"storage/pve1/local","type":"storage","node":"pve1","id":"storage/pve1/local","storage":"local","status":"available","plugtype":"dir"}
		]}`))
	})
	c := newTestClient(t, srv)
	rs, err := c.Resources(context.Background())
	if err != nil {
		t.Fatalf("Resources: %v", err)
	}
	if len(rs) != 4 {
		t.Fatalf("получено %d ресурсов", len(rs))
	}
	if g := Guests(rs); len(g) != 2 || g[0].VMID != 101 || g[0].Tags != "prod;web" {
		t.Fatalf("гости: %+v", g)
	}
	if n := Nodes(rs); len(n) != 1 || n[0].Name != "pve1" || !n[0].Running() {
		t.Fatalf("ноды: %+v", n)
	}
	// имя ноды выводится из node, name отсутствует
	if s := Storages(rs); len(s) != 1 {
		t.Fatalf("сторы: %+v", s)
	}
	// vmid как строка ("200") парсится
	var lxc []Resource
	for _, g := range Guests(rs) {
		if g.NodeType == "lxc" {
			lxc = append(lxc, g)
		}
	}
	if len(lxc) != 1 || lxc[0].VMID != 200 || lxc[0].Running() {
		t.Fatalf("lxc: %+v", lxc)
	}
}

// Стороны КОНКРЕТНОЙ ноды (диалог бэкапа предлагает только её хранилища).
func TestStoragesOn(t *testing.T) {
	rs := []Resource{
		{NodeType: "storage", Node: "pve1", Name: "local"},
		{NodeType: "storage", Node: "pve2", Name: "nfs-big"},
		{NodeType: "qemu", Node: "pve1", Name: "web1", VMID: 101},
	}
	got := StoragesOn(rs, "pve1")
	if len(got) != 1 || got[0].Name != "local" {
		t.Fatalf("storages on pve1: %+v", got)
	}
}
