package scripts

// M32 backlog №4: PACScripts API-контракт — действия, доступные пользовательскому
// скрипту (современный Go-интерфейс вместо легаси asbru_* функций). Исполняющий
// движок и GUI-редактор по контракту — последующие шаги.

// API — контракт операций скрипта над Ásbrú (легаси-кандидаты: asbru_conn,
// asbru_stop_conn, asbru_conn_text, ...). Скрипт получает этот API при запуске.
type API interface {
	// Connect — подключить соединение по UUID.
	Connect(uuid string) error
	// StopConn — закрыть соединение по UUID.
	StopConn(uuid string) error
	// Send — отправить текст в терминал по UUID.
	Send(uuid, text string) error
	// OpenConn — открыть соединения/группы (например, все избранные).
	OpenConn() error
}
