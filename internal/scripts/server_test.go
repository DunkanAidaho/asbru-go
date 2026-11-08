package scripts

// M32 PACScripts-движок: Serve — транспорт-агностик диспетчер JSON-RPC.
// Читает rpcRequest из conn, вызывает метод api (Connect/StopConn/Send/OpenConn),
// пишет rpcResponse. Не знает про Qt/процессы — только conn + api.

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
)

// sendRequest пишет один JSON-RPC-запрос в conn и возвращает ответ (один).
func sendRequest(t *testing.T, conn net.Conn, req rpcRequest) rpcResponse {
	t.Helper()
	b, err := marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	if !sc.Scan() {
		t.Fatalf("нет ответа: %v", sc.Err())
	}
	var resp rpcResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %v (body=%q)", err, sc.Text())
	}
	return resp
}

// TestServeConn — диспатчинг всех 4 методов API на in-memory pipe.
func TestServeConn(t *testing.T) {
	a := &memoAPI{}
	server, client := net.Pipe()
	defer client.Close()
	go func() {
		defer server.Close()
		_ = Serve(server, a)
	}()

	// connect
	resp := sendRequest(t, client, rpcRequest{ID: 1, Method: "connect", Params: []string{"u1"}})
	if resp.Error != "" || len(a.conns) != 1 || a.conns[0] != "u1" {
		t.Fatalf("connect resp=%+v conns=%v", resp, a.conns)
	}
	// send
	resp = sendRequest(t, client, rpcRequest{ID: 2, Method: "send", Params: []string{"u1", "ls\n"}})
	if resp.Error != "" || len(a.sent) != 1 || a.sent[0] != "u1:ls\n" {
		t.Fatalf("send resp=%+v sent=%v", resp, a.sent)
	}
	// stop
	resp = sendRequest(t, client, rpcRequest{ID: 3, Method: "stop", Params: []string{"u1"}})
	if resp.Error != "" || len(a.stop) != 1 || a.stop[0] != "u1" {
		t.Fatalf("stop resp=%+v stop=%v", resp, a.stop)
	}
	// open
	openCalled := false
	ao := &memoAPI{openFn: func() error { openCalled = true; return nil }}
	server2, client2 := net.Pipe()
	defer client2.Close()
	go func() { defer server2.Close(); _ = Serve(server2, ao) }()
	resp = sendRequest(t, client2, rpcRequest{ID: 4, Method: "open", Params: nil})
	if resp.Error != "" || !openCalled {
		t.Fatalf("open resp=%+v openCalled=%v", resp, openCalled)
	}
}

// TestServeConnAPIError — метод API возвращает error → rpcResponse.Error.
func TestServeConnAPIError(t *testing.T) {
	a := &memoAPI{errOn: "connect"}
	server, client := net.Pipe()
	defer client.Close()
	go func() { defer server.Close(); _ = Serve(server, a) }()
	resp := sendRequest(t, client, rpcRequest{ID: 9, Method: "connect", Params: []string{"u9"}})
	if resp.Error == "" || !strings.Contains(resp.Error, "boom") {
		t.Fatalf("ожидали error в ответе: %+v", resp)
	}
}

// TestServeConnUnknownMethod — неизвестный метод → error, без паники.
func TestServeConnUnknownMethod(t *testing.T) {
	a := &memoAPI{}
	server, client := net.Pipe()
	defer client.Close()
	go func() { defer server.Close(); _ = Serve(server, a) }()
	resp := sendRequest(t, client, rpcRequest{ID: 5, Method: "nope", Params: nil})
	if resp.Error == "" {
		t.Fatalf("unknown method должен дать error: %+v", resp)
	}
}

// TestServeConnBadJSON — мусорный запрос → error-ответ (не зависает, не падает).
func TestServeConnBadJSON(t *testing.T) {
	a := &memoAPI{}
	server, client := net.Pipe()
	defer client.Close()
	go func() { defer server.Close(); _ = Serve(server, a) }()
	if _, err := client.Write([]byte("{not-json\n")); err != nil {
		t.Fatal(err)
	}
	// движок должен ответить error-строкой, не закрыть молча.
	sc := bufio.NewScanner(client)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	if !sc.Scan() {
		t.Fatalf("нет ответа на мусорный запрос: %v", sc.Err())
	}
	var resp rpcResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		t.Fatalf("ответ не JSON: %v (body=%q)", err, sc.Text())
	}
	if resp.Error == "" {
		t.Fatalf("bad JSON должен дать error-ответ: %+v", resp)
	}
}

// TestServeConnEOF — закрытие клиента завершает Serve без ошибки/паники.
func TestServeConnEOF(t *testing.T) {
	a := &memoAPI{}
	server, client := net.Pipe()
	client.Close()
	err := Serve(server, a)
	server.Close()
	if err != nil && err != io.EOF {
		t.Fatalf("Serve при EOF: err=%v", err)
	}
}
