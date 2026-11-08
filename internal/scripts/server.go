package scripts

// M32 PACScripts-движок: Serve — транспорт-агностик диспетчер JSON-RPC.
// Читает построчно rpcRequest из conn (bufio.Scanner, лимит 1 МБ — защита от
// гигантских строк), вызывает метод api, пишет rpcResponse. Ошибки метода API
// и неизвестный метод → rpcResponse.Error (клиент не падает, не зависает).
// Возвращает ошибку только при фатальном сбое чтения/записи (сеть/EOF).

import (
	"bufio"
	"fmt"
	"net"
)

// maxRPCLine — верхняя граница одной RPC-строки (авто-защита от переполнения
// буфера Scanner; строки с ошибками/мусором не должны уронить движок).
const maxRPCLine = 1 << 20

// Serve читает JSON-RPC-запросы из conn и диспатчит их к api до закрытия
// conn (или фатальной ошибки чтения). Неблокирующий — работает в отдельной
// горутине (runner.go). Не зависит от транспорта: net.Pipe (тесты),
// socketpair на fd 3 (реальный подпроцесс).
func Serve(conn net.Conn, api API) error {
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 4096), maxRPCLine)
	for sc.Scan() {
		line := sc.Bytes()
		// пропускаем пустые/пробельные строки (клиент мог прислать \n).
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := unmarshalRequest(line, &req); err != nil {
			if werr := writeResponse(conn, rpcResponse{ID: -1, Error: "bad request: " + err.Error()}); werr != nil {
				return werr
			}
			continue
		}
		resp := sdispatch(req, api)
		if werr := writeResponse(conn, resp); werr != nil {
			return werr
		}
	}
	return sc.Err()
}

// sdispatch — вызов метода API по req; возвращает ответ. Ошибка метода и
// неизвестный метод → Error; успех → Result.
func sdispatch(req rpcRequest, api API) rpcResponse {
	resp := rpcResponse{ID: req.ID}
	if err := sdispatchErr(req, api); err != nil {
		resp.Error = err.Error()
		return resp
	}
	resp.Result = "ok"
	return resp
}

func sdispatchErr(req rpcRequest, api API) error {
	switch req.Method {
	case "connect":
		if len(req.Params) < 1 {
			return fmt.Errorf("connect: нет uuid")
		}
		return api.Connect(req.Params[0])
	case "stop":
		if len(req.Params) < 1 {
			return fmt.Errorf("stop: нет uuid")
		}
		return api.StopConn(req.Params[0])
	case "send":
		if len(req.Params) < 2 {
			return fmt.Errorf("send: нет uuid/текста")
		}
		return api.Send(req.Params[0], req.Params[1])
	case "open":
		return api.OpenConn()
	default:
		return fmt.Errorf("unknown method %q", req.Method)
	}
}

func writeResponse(conn net.Conn, resp rpcResponse) error {
	b, err := marshalResponse(resp)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}
