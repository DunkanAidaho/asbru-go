package pve

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGuestActionReturnsUPID(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/qemu/101/status/start" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":"UPID:pve1:0000ABCD:hex:qemu:start:root@pam:"}`))
	})
	c := newTestClient(t, srv)
	upid, err := c.GuestAction(context.Background(), "pve1", "qemu", 101, "start")
	if err != nil || !strings.HasPrefix(upid, "UPID:") {
		t.Fatalf("upid=%q err=%v", upid, err)
	}
}

func TestTaskStatusRunning(t *testing.T) {
	// Статус задачи — GET /nodes/{node}/tasks/{upid}/status (PVE/API2/Tasks.pm
	// `{upid}/status`); /cluster/tasks/{upid} не существует (там только list).
	// ':' и '@' — RFC 3986 pchar, url.PathEscape оставляет их как есть.
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/nodes/pve1/tasks/UPID:pve1:x:x:qemu:start:root@pam:/status" {
			t.Errorf("неожиданный путь %s", r.URL.Path)
		}
		w.Write([]byte(`{"data":{"status":"running","exitstatus":""}}`))
	})
	c := newTestClient(t, srv)
	running, exit, err := c.TaskStatus(context.Background(), "UPID:pve1:x:x:qemu:start:root@pam:")
	if err != nil || !running || exit != "" {
		t.Fatalf("running=%v exit=%q err=%v", running, exit, err)
	}
}

// Нода берётся из UPID; мусорный UPID — ошибка без сетевого запроса.
func TestTaskStatusRejectsMalformedUPID(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("сетевой запрос при некорректном UPID: %s", r.URL.Path)
	})
	c := newTestClient(t, srv)
	if _, _, err := c.TaskStatus(context.Background(), "not-a-upid"); err == nil {
		t.Fatal("ожидали ошибку для некорректного UPID")
	}
}

func TestWaitTaskFailsOnBadExit(t *testing.T) {
	// первый опрос — running, второй — stopped/exit≠OK
	calls := 0
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		st := "running"
		ex := ""
		if calls > 1 {
			st, ex = "stopped", "TASK ERROR: qm start failed"
		}
		w.Write([]byte(fmt.Sprintf(`{"data":{"status":%q,"exitstatus":%q}}`, st, ex)))
	})
	c := newTestClient(t, srv)
	err := c.WaitTask(context.Background(), "UPID:pve1:x:x:qemu:start:root@pam:", 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "TASK ERROR") {
		t.Fatalf("err=%v", err)
	}
}

func TestGuestConfig(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api2/json/nodes/pve1/lxc/200/config" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":{"hostname":"dns","memory":"512","net0":"name=eth0,bridge=vmbr0"}}`))
	})
	c := newTestClient(t, srv)
	cfg, err := c.GuestConfig(context.Background(), "pve1", "lxc", 200)
	if err != nil {
		t.Fatalf("GuestConfig: %v", err)
	}
	if cfg["hostname"] != "dns" || cfg["memory"] != "512" {
		t.Fatalf("конфиг: %+v", cfg)
	}
}

func TestGuestAgentIPsMissingAgentIsEmpty(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented) // PVE: "guest-agent is not running"
	})
	c := newTestClient(t, srv)
	ips, err := c.GuestAgentIPs(context.Background(), "pve1", 101)
	if err != nil || len(ips) != 0 {
		t.Fatalf("ips=%v err=%v", ips, err)
	}
}

func TestGuestAgentIPsAgentDown500IsEmpty(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // PVE отвечает и 500, когда агента нет
	})
	c := newTestClient(t, srv)
	ips, err := c.GuestAgentIPs(context.Background(), "pve1", 101)
	if err != nil || len(ips) != 0 {
		t.Fatalf("ips=%v err=%v", ips, err)
	}
}

func TestGuestAgentIPsUnauthorizedPropagates(t *testing.T) {
	// нода недоступна/сессия истекла — это НЕ «агента нет», ошибка идёт наверх
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := newTestClient(t, srv)
	ips, err := c.GuestAgentIPs(context.Background(), "pve1", 101)
	if err == nil {
		t.Fatalf("ожидалась ошибка, получено ips=%v", ips)
	}
}

func TestGuestAgentIPsFiltersLoopback(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/qemu/101/agent/network-get-interfaces" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":{"result":[
			{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1","ip-address-type":"ipv4"},{"ip-address":"::1","ip-address-type":"ipv6"}]},
			{"name":"eth0","ip-addresses":[{"ip-address":"192.0.2.10","ip-address-type":"ipv4"},{"ip-address":"fe80::1","ip-address-type":"ipv6"}]}
		]}}`))
	})
	c := newTestClient(t, srv)
	ips, err := c.GuestAgentIPs(context.Background(), "pve1", 101)
	if err != nil || len(ips) != 2 || ips[0] != "192.0.2.10" || ips[1] != "fe80::1" {
		t.Fatalf("ips=%v err=%v", ips, err)
	}
}

// Мусорный адрес от агента (не парсится как IP) в колонку не попадает.
func TestGuestAgentIPsSkipsGarbageAddresses(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"result":[
			{"name":"eth0","ip-addresses":[{"ip-address":"not-an-ip","ip-address-type":"ipv4"},{"ip-address":"192.0.2.10","ip-address-type":"ipv4"}]}
		]}}`))
	})
	c := newTestClient(t, srv)
	ips, err := c.GuestAgentIPs(context.Background(), "pve1", 101)
	if err != nil || len(ips) != 1 || ips[0] != "192.0.2.10" {
		t.Fatalf("ips=%v err=%v, хотим только [192.0.2.10]", ips, err)
	}
}

// Ревью 3 (Minor): PVE отвечает data:null на status завершённой задачи без
// exitstatus → exit="" и сообщение «задача …: » с хвостом-пустотой.
func TestWaitTaskNullStatusReadableError(t *testing.T) {
	calls := 0
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Write([]byte(`{"data":{"status":"running"}}`))
			return
		}
		w.Write([]byte(`{"data":null}`))
	})
	c := newTestClient(t, srv)
	err := c.WaitTask(context.Background(), "UPID:pve1:x:x:qemu:stop:root@pam:", time.Millisecond)
	if err == nil {
		t.Fatalf("ожидалась ошибка")
	}
	if strings.Contains(err.Error(), ": ") && strings.HasSuffix(err.Error(), ": ") {
		t.Errorf("хвост-пустота в сообщении: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "без статуса") {
		t.Errorf("нет внятного текста: %q", err.Error())
	}
}

// BACKLOG-миноры слепых ревью: релогин с ctx (не висит после отмены) и
// errors.Join — исходная 401 не теряется при неудачном релогине.
func TestReloginFailureJoinsErrors(t *testing.T) {
	calls := 0
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.HasSuffix(r.URL.Path, "access/ticket") {
			w.WriteHeader(http.StatusUnauthorized) // релогин тоже падает
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	c := NewClient(srv.URL, false, time.Second)
	if err := c.Login("root@pam", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	// имитация смены пароля на сервере: сохранённый больше не подходит
	c.user, c.pass = "root@pam", "wrong-after-change"
	err := c.doJSON(context.Background(), http.MethodGet, "/x", nil, &struct{}{})
	if err == nil {
		t.Fatalf("ожидалась ошибка")
	}
	aggr, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("ошибка не errors.Join-агрегат: %v", err)
	}
	if len(aggr.Unwrap()) < 2 {
		t.Fatalf("агрегат меньше 2 ошибок: %v", err)
	}
}

// Спека M45-остаток: LXC-консоль через termproxy (PVE оборачивает терминал
// LXC в VNC — port+ticket, тот же vncview).
func TestTermProxy(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/lxc/200/termproxy" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		w.Write([]byte(`{"data":{"port":5901,"ticket":"PVECT:abc"}}`))
	})
	c := newTestClient(t, srv)
	tp, err := c.TermProxy(context.Background(), "pve1", 200)
	if err != nil || tp.Port != 5901 || tp.Ticket != "PVECT:abc" {
		t.Fatalf("tp=%+v err=%v", tp, err)
	}
}

// Спека M45-остаток: бэкап vzdump — POST с формой, ответ UPID.
func TestBackup(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/vzdump" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		r.ParseForm()
		if r.PostForm.Get("storage") != "local" || r.PostForm.Get("mode") != "snapshot" ||
			r.PostForm.Get("vmid") != "101" || r.PostForm.Get("compress") != "zstd" {
			t.Errorf("форма: %v", r.PostForm)
		}
		w.Write([]byte(`{"data":"UPID:pve1:0001:vzdump:root@pam:"}`))
	})
	c := newTestClient(t, srv)
	upid, err := c.Backup(context.Background(), "pve1", 101, "local", "snapshot")
	if err != nil || !strings.HasPrefix(upid, "UPID:") {
		t.Fatalf("upid=%q err=%v", upid, err)
	}
}
