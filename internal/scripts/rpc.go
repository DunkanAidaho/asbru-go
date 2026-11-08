package scripts

// M32 PACScripts-движок: wire-протокол JSON-RPC-подобного вида (stdlib, без
// внешних зависимостей). Запрос/ответ — простые struct с полями id/method/
// params/result/error. Методы соответствуют API (api.go): connect/send/stop/open.
// Транспорт-независимо: Serve (server.go) читает rpcRequest и пишет rpcResponse;
// runner (runner.go) связывает их через socketpair на fd 3 подпроцесса.

import "encoding/json"

// rpcRequest — запрос от скрипта к движку.
type rpcRequest struct {
	ID     int      `json:"id"`
	Method string   `json:"method"`
	Params []string `json:"params"`
}

// rpcResponse — ответ движка скрипту. Result заполнен при успехе, Error — при
// ошибке (одно из двух; см. api.go: методы возвращают error).
type rpcResponse struct {
	ID     int    `json:"id"`
	Result string `json:"result"`
	Error  string `json:"error"`
}

func marshalRequest(r rpcRequest) ([]byte, error) {
	return json.Marshal(r)
}

func unmarshalRequest(b []byte, r *rpcRequest) error {
	return json.Unmarshal(b, r)
}

func marshalResponse(r rpcResponse) ([]byte, error) {
	return json.Marshal(r)
}

func unmarshalResponse(b []byte, r *rpcResponse) error {
	return json.Unmarshal(b, r)
}
