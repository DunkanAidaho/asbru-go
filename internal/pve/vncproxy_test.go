package pve

import (
	"context"
	"net/http"
	"testing"
)

func TestVNCProxyReturnsPortAndTicket(t *testing.T) {
	srv, _ := mockPVE(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api2/json/nodes/pve1/qemu/101/vncproxy" {
			t.Errorf("bad %s %s", r.Method, r.URL.Path)
		}
		// websocket=0 — намеренно классический VNC, не noVNC-вебсокет
		if err := r.ParseForm(); err != nil || r.PostForm.Get("websocket") != "0" {
			t.Errorf("форма websocket=0: %v (%v)", r.PostForm, err)
		}
		w.Write([]byte(`{"data":{"port":5900,"ticket":"PVEVNC:abc123","cert":"---CERT---"}}`))
	})
	c := newTestClient(t, srv)
	vp, err := c.VNCProxy(context.Background(), "pve1", 101)
	if err != nil {
		t.Fatalf("VNCProxy: %v", err)
	}
	if vp.Port != 5900 || vp.Ticket != "PVEVNC:abc123" {
		t.Fatalf("vncproxy: %+v", vp)
	}
}
