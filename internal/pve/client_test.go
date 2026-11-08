package pve

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// новый мок-PVE: считает логины, отдаёт fixed-ответ, проверяет тикет-cookie.
// Логин принимается только с паролем "pw" (как в newTestClient); прочие
// пароли дают 401 — как реальный PVE с неверными учётными данными.
func mockPVE(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *int) {
	logins := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		logins++
		if err := r.ParseForm(); err != nil || r.PostForm.Get("password") != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"ticket": "PVE:root@pam:deadbeef", "CSRFPreventionToken": "CSRF:42",
		}})
	})
	mux.HandleFunc("/", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &logins
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	c := NewClient(srv.URL, false, 2*time.Second)
	if err := c.Login("root@pam", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	return c
}

func TestLoginStoresTicketAndCSRF(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CSRFPreventionToken") != "CSRF:42" {
			t.Errorf("POST без CSRF-токена")
		}
		if ck, err := r.Cookie("PVEAuthCookie"); err != nil || ck.Value != "PVE:root@pam:deadbeef" {
			t.Errorf("нет тикет-cookie: %v", err)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ok": 1}})
	})
	c := newTestClient(t, srv)
	var out struct{ Ok int }
	if err := c.doJSON(context.Background(), http.MethodPost, "/cluster/nextid", url.Values{}, &out); err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if out.Ok != 1 {
		t.Fatalf("тело не распарсено: %+v", out)
	}
}

func TestErrorCarriesPVEMessage(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":"vm 999 does not exist"}`))
	})
	c := newTestClient(t, srv)
	err := c.doJSON(context.Background(), http.MethodGet, "/x", nil, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "vm 999 does not exist") {
		t.Fatalf("ожидалась PVE-ошибка с текстом, got %v", err)
	}
}

func TestReloginOnceOnExpiredTicket(t *testing.T) {
	srv, logins := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if ck, _ := r.Cookie("PVEAuthCookie"); ck == nil || ck.Value == "stale" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ok": 1}})
	})
	c := newTestClient(t, srv)
	c.ticket = "stale" // имитация протухшего тикета
	var out struct{ Ok int }
	if err := c.doJSON(context.Background(), http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatalf("релогин не спас: %v", err)
	}
	if *logins != 2 {
		t.Fatalf("логинов %d, ожидался 1 исходный + 1 релогин", *logins)
	}
}

func TestBadCredsIsErrorNotPanic(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	c := NewClient(srv.URL, false, time.Second)
	if err := c.Login("root@pam", "wrong"); err == nil {
		t.Fatalf("ожидалась ошибка логина")
	}
}

// Спека п.7 (п.2): сетевой таймаут — отдельный сценарий. httptest-хендлер
// спит дольше таймаута клиента → клиент обязан вернуть timeout-ошибку
// (net.Error), а не зависнуть.
func TestRequestTimeoutIsNetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, false, 50*time.Millisecond)
	err := c.Login("root@pam", "pw")
	if err == nil {
		t.Fatalf("ожидалась таймаут-ошибка")
	}
	var ne net.Error
	if !errors.As(err, &ne) {
		t.Fatalf("ошибка не net.Error (нет признака таймаута): %v", err)
	}
}
