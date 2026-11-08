package scripts

// M32 PACScripts-движок: RunAPI — запускает подпроцесс с socketpair на fd 3 и
// Serve-диспетчером. Тестовый подпроцесс = этот же test binary с env-флагом
// (_ASBRU_RPC_HELPER), который читает fd 3, отвечает на JSON-RPC и пишет в stderr.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("_ASBRU_RPC_HELPER") == "1" {
		rpcHelperMain()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// rpcHelperMain — РОЛЬ подпроцесса-скрипта: это КЛИЕНТ движка. Пишет JSON-RPC
// запросы (connect/send/stop/open) в fd 3, читает ответы, эхом пишет их в stderr
// (чтобы RunAPI вернул их как вывод). По «stop» ожидает ошибку (проверка ответа).
func rpcHelperMain() {
	f := os.NewFile(3, "rpc")
	if f == nil {
		return
	}
	enc := json.NewEncoder(f)
	dec := json.NewDecoder(f)
	if v := os.Getenv("_ASBRU_RPC_HELPER_STDOUT_BYTES"); v != "" {
		// «болтливый скрипт»: пишет заданный объём stdout строками по 1КиБ и
		// завершает вывод маркером. Нужно для проверок лимита и полноты хвоста
		// (раньше cmd.Wait() закрывал pipe до дренажа читателей).
		n, _ := strconv.Atoi(v)
		line := strings.Repeat("x", 1023) + "\n"
		for written := 0; written < n; written += len(line) {
			if _, err := os.Stdout.WriteString(line); err != nil {
				return
			}
		}
		os.Stdout.WriteString("TAIL-MARKER\n")
		return
	}
	if os.Getenv("_ASBRU_RPC_HELPER_HANG") == "1" {
		// зависший «скрипт»: держит fd 3 открытым и молчит (имитация ухода
		// в бесконечный опрос) — ctx-отмена должна его убить. f жив (в скоупе),
		// поэтому fd 3 не закрывается до kill.
		_ = f
		select {}
	}
	if os.Getenv("_ASBRU_RPC_HELPER_STDERR_FLOOD") == "1" {
		// регресс-тест дедлока (ревью): пишем >pipe-buffer (~64КиБ) в stderr,
		// НЕ закрывая stdout (не выходим), затем выходим. При последовательном
		// drain stdout-вперёд это вешало RunAPI; параллельный — не должен.
		blob := make([]byte, 256*1024)
		os.Stderr.Write(blob)
		os.Stdout.WriteString("done\n")
		return
	}
	reqs := []rpcRequest{
		{ID: 1, Method: "connect", Params: []string{"u1"}},
		{ID: 2, Method: "send", Params: []string{"u1", "ls\n"}},
		{ID: 3, Method: "stop", Params: []string{"u1"}},
		{ID: 4, Method: "open", Params: nil},
	}
	out := bufio.NewWriter(os.Stderr)
	for _, r := range reqs {
		if err := enc.Encode(r); err != nil {
			return
		}
		var resp rpcResponse
		if err := dec.Decode(&resp); err != nil {
			return
		}
		out.WriteString("resp:" + r.Method + ":" + resp.Error + "\n")
		out.Flush()
	}
}

// TestRunAPIContextCancel — зависший подпроцесс убивается по ctx-отмене
// (M32-ревью: не зависнуть на старте). Возвращает prompt-ошибку, не висит.
func TestRunAPIContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "_ASBRU_RPC_HELPER=1", "_ASBRU_RPC_HELPER_HANG=1")
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := RunAPI(ctx, cmd, &memoAPI{}, nil)
		done <- err
	}()
	// даём подпроцессу стартовать, затем отменяем.
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("отменённый контекст должен дать ошибку (не nil)")
		}
		if d := time.Since(start); d > 4*time.Second {
			t.Fatalf("отмена заняла слишком долго: %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("RunAPI не завершился после отмены контекста — завис")
	}
}

// TestRunAPIStderrFloodNoDeadlock — регресс-тест дедлока из ревью: скрипт пишет
// больший (>64КиБ) объём в stderr, не закрывая stdout, и выходит. Последовательный
// drain (stdout-вперёд) вешал RunAPI; параллельные ридеры — не должны.
func TestRunAPIStderrFloodNoDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "_ASBRU_RPC_HELPER=1", "_ASBRU_RPC_HELPER_STDERR_FLOOD=1")
	out, err := RunAPI(ctx, cmd, &memoAPI{}, nil)
	if err != nil {
		t.Fatalf("RunAPI при stderr-флуде: %v", err)
	}
	if !strings.Contains(out, "done\n") {
		t.Fatalf("ожидали stdout 'done', out=%q", out)
	}
}

func helperCmd(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "_ASBRU_RPC_HELPER=1")
	return cmd
}

func TestRunAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	a := &memoAPI{}
	out, err := RunAPI(ctx, helperCmd(t), a, nil)
	if err != nil {
		t.Fatalf("RunAPI: %v", err)
	}
	// подпроцесс прислал 4 запроса → движок (Serve) задиспатчил к api.
	if len(a.conns) != 1 || a.conns[0] != "u1" {
		t.Fatalf("conns=%v", a.conns)
	}
	if len(a.sent) != 1 || a.sent[0] != "u1:ls\n" {
		t.Fatalf("sent=%v", a.sent)
	}
	if len(a.stop) != 1 || a.stop[0] != "u1" {
		t.Fatalf("stop=%v", a.stop)
	}
	// подпроцесс логировал свои ответы в stderr → они в возвращённом out.
	for _, want := range []string{"resp:connect:", "resp:send:", "resp:stop:", "resp:open:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ответы подпроцесса не в out: %q (want %s)", out, want)
		}
	}
}

// M7: stdout одной неразрывной строки > лимита bufio.Scanner (1МиБ) не должен
// дедлочить (child заблокирован на stdout-write, родитель в cmd.Wait()).
func TestRunAPIStdoutHugeLineNoDeadlock(t *testing.T) {
	done := make(chan struct{})
	var out string
	var err error
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.Command("/bin/sh", "-c", "head -c 1200000 /dev/zero | tr '\\000' 'x'")
		out, err = RunAPI(ctx, cmd, nil, nil)
		close(done)
	}()
	select {
	case <-done:
		// вернулся — не завис.
	case <-time.After(5 * time.Second):
		t.Fatalf("RunAPI завис (deadlock) на stdout >1МиБ строке")
	}
	_ = out
	_ = err
}

// helperBulkCmd — подпроцесс, пишущий n байт в stdout + TAIL-MARKER.
func helperBulkCmd(t *testing.T, n int) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "_ASBRU_RPC_HELPER=1",
		"_ASBRU_RPC_HELPER_STDOUT_BYTES="+strconv.Itoa(n))
	return cmd
}

// Регресс: stdout+stderr копились без ограничения (скрипт мог съесть память).
// Вывод обязан усекаться до лимита и явно сообщать об усечении.
func TestRunAPIBoundsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := RunAPI(ctx, helperBulkCmd(t, 2*maxOutputBytes), nil, nil)
	if err != nil {
		t.Fatalf("RunAPI: %v", err)
	}
	if !strings.Contains(out, "вывод обрезан") {
		t.Fatalf("нет маркера усечения (len=%d)", len(out))
	}
	limit := maxOutputBytes + len(outputTruncatedMarker)
	if len(out) > limit {
		t.Fatalf("вывод не ограничен: %d байт > %d", len(out), limit)
	}
}

// Регресс (os/exec docs): Wait нельзя звать до дренажа StdoutPipe/StderrPipe —
// иначе pipe закрывается с непрочитанным хвостом. Основной процесс-скрипт
// выходит сразу, но фоновый потомок держит stdout открытым и дописывает хвост
// позже: старый код звал cmd.Wait() (закрывал pipe) до дренажа — хвост терялся.
func TestRunAPIOutputTailComplete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c",
		"( sleep 0.2; printf 'TAIL-MARKER\\n' ) & exit 0")
	out, err := RunAPI(ctx, cmd, nil, nil)
	if err != nil {
		t.Fatalf("RunAPI: %v", err)
	}
	if !strings.HasSuffix(out, "TAIL-MARKER\n") {
		t.Fatalf("хвост stdout потерян (len=%d, конец=%q)", len(out), tail(out, 64))
	}
}

// tail — последние n байт строки (для диагностики).
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ag-sz9: скрипт оставляет фоновый процесс, наследующий stdout/stderr, и
// выходит. EOF для читателей pipe не наступает никогда; старый дренаж
// (owg.Wait()) висел вечно, даже после отмены ctx: exec.CommandContext убивает
// только прямого ребёнка, а фон продолжал держать write-концы. Отмена ctx
// обязана разблокировать RunAPI.
func TestRunAPIContextCancelBackgroundChildHoldsPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Фон спавнится ДО маркера, поэтому получение BG-READY гарантирует: внук
	// уже форкнут и держит write-концы stdout/stderr (без sleep-гонки).
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c",
		"sleep 30 & printf 'BG-READY\\n'; exit 0")
	// Setpgid — чтобы после теста убить группу целиком (sh + фоновый sleep),
	// иначе фоновый процесс живёт 30 с и держит pipe RunAPI.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// pid берём в читающей горутине RunAPI (она создана после Start, поэтому
	// чтение cmd.Process там гонки не даёт) и прибираем группу в defer —
	// в том числе на ветках t.Fatalf.
	pid := 0
	defer func() {
		if pid != 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
	ready := make(chan int, 1)

	done := make(chan error, 1)
	go func() {
		_, err := RunAPI(ctx, cmd, nil, func(string) {
			select {
			case ready <- cmd.Process.Pid:
			default:
			}
		})
		done <- err
	}()

	select {
	case pid = <-ready:
	case <-time.After(5 * time.Second):
		t.Fatalf("подпроцесс не напечатал BG-READY")
	}
	cancel()
	canceledAt := time.Now()

	select {
	case err := <-done:
		// именно отмена (не Start-гонка) должна разблокировать RunAPI.
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("после отмены ожидали context.Canceled, получили %v", err)
		}
		if d := time.Since(canceledAt); d > 3*time.Second {
			t.Fatalf("RunAPI вернулся слишком поздно после отмены: %v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("RunAPI завис: фоновый потомок держит stdout/stderr")
	}
}
