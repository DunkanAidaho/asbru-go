package ui

// Тест ensureLogin (pvewidget.go): голый struct pveWidget БЕЗ Qt-объектов —
// сам виджет (тулбар/дерево) в тестах по-прежнему не создаётся; сетевую
// часть подменяет httptest-сервер (по образцу mockPVE в internal/pve).

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/pve"
)

// ensureLogin: неудачный первый логин (транзиентная сеть) НЕ кэшируется
// навсегда — следующее «Обновить» повторяет попытку; успешный логин
// кэшируется (третьего POST /access/ticket нет).
func TestPveEnsureLoginRetriesAfterFailure(t *testing.T) {
	var ok atomic.Bool
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/access/ticket" {
			t.Errorf("неожиданный путь %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		n.Add(1)
		if !ok.Load() {
			w.WriteHeader(http.StatusInternalServerError) // транзиентный сбой
			return
		}
		w.Write([]byte(`{"data":{"ticket":"PVE:root@pam:t","CSRFPreventionToken":"CSRF"}}`))
	}))
	defer srv.Close()

	w := &pveWidget{env: &config.Environment{User: "root@pam", Pass: "pw"}}
	c := pve.NewClient(srv.URL, true, 2*time.Second)

	if err := w.ensureLogin(c); err == nil {
		t.Fatal("первый логин должен упасть (сервер 500)")
	}
	ok.Store(true)
	if err := w.ensureLogin(c); err != nil {
		t.Fatalf("после сбоя попытка должна повториться, получено: %v", err)
	}
	if err := w.ensureLogin(c); err != nil {
		t.Fatalf("успешный логин должен кэшироваться, получено: %v", err)
	}
	if got := n.Load(); got != 2 {
		t.Fatalf("POST /access/ticket = %d, хотим 2 (сбой + успех; третий вызов — из кэша)", got)
	}
}
