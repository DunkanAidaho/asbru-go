package pve

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestSnapshotsListDropsCurrent(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api2/json/nodes/pve1/qemu/101/snapshot" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":[
			{"name":"current","description":"You are here!","snaptime":0},
			{"name":"base","description":"чистая система","snaptime":1700000000}
		]}`))
	})
	c := newTestClient(t, srv)
	sn, err := c.Snapshots(context.Background(), "pve1", "qemu", 101)
	if err != nil {
		t.Fatalf("Snapshots: %v", err)
	}
	if len(sn) != 1 {
		t.Fatalf("ожидался 1 снапшот без current: %+v", sn)
	}
	if sn[0].Name != "base" || sn[0].Description != "чистая система" || sn[0].Time != 1700000000 {
		t.Fatalf("снапшот: %+v", sn[0])
	}
}

func TestSnapshotCreateFormAndUPID(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/lxc/200/snapshot" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil ||
			r.PostForm.Get("snapname") != "snap1" ||
			r.PostForm.Get("description") != "тест" {
			t.Errorf("форма snapname/description: %v (%v)", r.PostForm, err)
		}
		w.Write([]byte(`{"data":"UPID:pve1:0000ABCD:hex:lxc:snapshot:root@pam:"}`))
	})
	c := newTestClient(t, srv)
	upid, err := c.SnapshotCreate(context.Background(), "pve1", "lxc", 200, "snap1", "тест")
	if err != nil || !strings.HasPrefix(upid, "UPID:") {
		t.Fatalf("upid=%q err=%v", upid, err)
	}
}

func TestSnapshotRollbackPathAndUPID(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/qemu/101/snapshot/snap1/rollback" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":"UPID:pve1:0000ABCD:hex:qemu:rollback:root@pam:"}`))
	})
	c := newTestClient(t, srv)
	upid, err := c.SnapshotRollback(context.Background(), "pve1", "qemu", 101, "snap1")
	if err != nil || !strings.HasPrefix(upid, "UPID:") {
		t.Fatalf("upid=%q err=%v", upid, err)
	}
}

func TestSnapshotDeleteIsDELETE(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api2/json/nodes/pve1/qemu/101/snapshot/snap1" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":"UPID:pve1:0000ABCD:hex:qemu:del:root@pam:"}`))
	})
	c := newTestClient(t, srv)
	upid, err := c.SnapshotDelete(context.Background(), "pve1", "qemu", 101, "snap1")
	if err != nil || !strings.HasPrefix(upid, "UPID:") {
		t.Fatalf("upid=%q err=%v", upid, err)
	}
}
