package session

// M31: 'save session logs' — путь лога сессии из паттерна с подстановками
// макросов (легаси PACTerminal.pm:160-166, PACUtils.pm:2127).

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestSessionLogPath(t *testing.T) {
	r := Resolver{Local: map[string]string{
		"UUID": "u1", "NAME": "web",
		"DATE_Y": "2026", "DATE_M": "08", "DATE_D": "27",
		"TIME_H": "15", "TIME_M": "04", "TIME_S": "05",
	}}
	env := &config.Environment{
		Name:              "web",
		SaveSessionLogs:   1,
		SessionLogsFolder: "/var/log/asbru",
		SessionLogPattern: "<UUID>_<NAME>_<DATE_Y><DATE_M><DATE_D>_<TIME_H><TIME_M><TIME_S>.txt",
	}
	path, ok := sessionLogPath(env, r)
	if !ok {
		t.Fatal("ok=false при включённых логах")
	}
	want := "/var/log/asbru/u1_web_20260827_150405.txt"
	if path != want {
		t.Fatalf("path=%q want %q", path, want)
	}

	// выключено
	if _, ok := sessionLogPath(&config.Environment{SaveSessionLogs: 0}, r); ok {
		t.Fatal("ok=true при выключенных логах")
	}
	// nil
	if _, ok := sessionLogPath(nil, r); ok {
		t.Fatal("ok=true при nil env")
	}
	// пустой паттерн/каталог — не собираем путь (нет корневого каталога)
	if _, ok := sessionLogPath(&config.Environment{SaveSessionLogs: 1, SessionLogsFolder: "/f"}, r); ok {
		t.Fatal("ok=true при пустом паттерне")
	}
}

// M31: при SaveSessionLogs=1 вывод сессии пишется в файл <folder>/<pattern>.
func TestSessionLogWrite(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &pipeStream{r: make(chan []byte, 8), w: make(chan []byte, 8)}
	cfg := &config.Environment{
		Name: "web", User: "root", Pass: "s", Method: "SSH",
		SaveSessionLogs: 1, SessionLogsFolder: dir, SessionLogPattern: "<UUID>.log",
	}
	_, err := New(ctx, BuildEnv{UUID: "u1"}, cfg, WithStreamFactory(func() (io.ReadWriteCloser, error) {
		return stream, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	stream.serverSend("HELLO-OUTPUT\n")
	time.Sleep(80 * time.Millisecond) // дать горутине прочитать и записать
	cancel()

	data, err := os.ReadFile(dir + "/u1.log")
	if err != nil {
		t.Fatalf("лог не создан: %v", err)
	}
	if !strings.Contains(string(data), "HELLO-OUTPUT") {
		t.Fatalf("лог не содержит вывод: %q", data)
	}
}

// M31-ревью #1: ротация трогает ТОЛЬКО файлы-логи по маске, а не всё из
// каталога; старые логи сверх amount удаляются по mtime.
func TestRotateSessionLogs(t *testing.T) {
	const mask = "*_*_***_***.txt"
	dir := t.TempDir()
	now := time.Now()
	mk := func(name string, mt time.Time) {
		if err := os.WriteFile(dir+"/"+name, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir+"/"+name, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	// старый лог (index=0) → удалится; новый (1) → останется.
	mk("u1_web_20260827_150405.txt", now.Add(-3*time.Hour))
	mk("u1_web_20260827_153405.txt", now.Add(-1*time.Hour))
	// посторонний файл в том же каталоге — НЕ должен удаляться (ревью #1).
	mk(".keep", now.Add(-5*time.Hour))

	rotateSessionLogs(dir, 1, mask)

	if _, err := os.Stat(dir + "/.keep"); err != nil {
		t.Fatalf("посторонний .keep удалён: %v", err)
	}
	if _, err := os.Stat(dir + "/u1_web_20260827_150405.txt"); !os.IsNotExist(err) {
		t.Fatalf("старый лог не удалён: %v", err)
	}
	if _, err := os.Stat(dir + "/u1_web_20260827_153405.txt"); err != nil {
		t.Fatalf("новый лог удалён: %v", err)
	}
}

// sessionLogMask: <MACRO> → '*' (для glob-маски ротации).
func TestSessionLogMask(t *testing.T) {
	got := sessionLogMask("<UUID>_<NAME>_<DATE_Y><DATE_M><DATE_D>_<TIME_H><TIME_M><TIME_S>.txt")
	if got != "*_*_***_***.txt" {
		t.Fatalf("mask=%q", got)
	}
}
