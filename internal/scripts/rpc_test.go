package scripts

// M32 PACScripts-движок: wire-протокол JSON-RPC-подобного вида (stdlib).
// Запрос: {id, method, params[]}; ответ: {id, result, error}. Методы = api.go
// (connect/send/stop/open). Транспорт-независимо; Serve/runner — отдельно.

import (
	"testing"
)

func TestMarshalRequest(t *testing.T) {
	req := rpcRequest{ID: 7, Method: "send", Params: []string{"u1", "uptime\n"}}
	b, err := marshalRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":7,"method":"send","params":["u1","uptime\n"]}`
	if string(b) != want {
		t.Fatalf("marshalRequest = %s, want %s", b, want)
	}
}

func TestUnmarshalRequest(t *testing.T) {
	var r rpcRequest
	if err := unmarshalRequest([]byte(`{"id":1,"method":"connect","params":["u1"]}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.ID != 1 || r.Method != "connect" || len(r.Params) != 1 || r.Params[0] != "u1" {
		t.Fatalf("unmarshal = %+v", r)
	}
}

func TestUnmarshalRequestBadJSON(t *testing.T) {
	var r rpcRequest
	if err := unmarshalRequest([]byte(`{not-json`), &r); err == nil {
		t.Fatal("bad JSON должен дать ошибку")
	}
}

func TestResponseRoundTrip(t *testing.T) {
	// успех: result != nil, error пустой.
	ok := rpcResponse{ID: 3, Result: "connected", Error: ""}
	b, err := marshalResponse(ok)
	if err != nil {
		t.Fatal(err)
	}
	var got rpcResponse
	if err := unmarshalResponse(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != 3 || got.Result != "connected" || got.Error != "" {
		t.Fatalf("ok response = %+v", got)
	}
	// ошибка: error != "", result пустой.
	errResp, err := marshalResponse(rpcResponse{ID: 4, Result: "", Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	if string(errResp) != `{"id":4,"result":"","error":"boom"}` {
		t.Fatalf("err response = %s", errResp)
	}
}
