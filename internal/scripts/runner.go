package scripts

// M32 PACScripts-движок: RunAPI — запускает подпроцесс скрипта и связывает его
// с API через socketpair на fd 3. Подпроцесс получает fd 3 как канал JSON-RPC
// (ExtraFiles); движок читает его stdin/записи и диспатчит в Serve. Stdout/Stderr
// подпроцесса собираются и возвращаются (легаси-скрипты пишут туда свой вывод).
//
// onLine — опциональный колбэк для каждой строки ответа подпроцесса (полезно для
// логов/UI). Реальный API-бэкенд (действия над живыми сессиями) внедряется как
// api — это сделает UI-слой при подключении.

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// maxOutputBytes — верхняя граница суммарного stdout+stderr, который RunAPI
// держит в памяти. Скрипт может печатать без остановки; без лимита вывод
// съел бы память процесса. Излишек дренируется и отбрасывается (нельзя
// перестать читать — child заблокируется на pipe), факт усечения виден в
// возвращённой строке.
const maxOutputBytes = 1 << 20 // 1 МиБ

// outputTruncatedMarker — явный признак усечения в конце вывода.
var outputTruncatedMarker = "\n[вывод обрезан: превышен лимит " +
	strconv.Itoa(maxOutputBytes) + " байт]\n"

// outputSink — ограниченный приёмник суммарного stdout+stderr. Обе горутины-
// читателя пишут сюда под одним мьютексом, поэтому лимит общий, а не на поток.
// Write всегда «принимает» весь p (возвращает len(p), nil): io.Copy обязан
// продолжать дренаж, иначе child заблокируется на pipe.
type outputSink struct {
	mu    sync.Mutex
	sb    strings.Builder
	total int
	trunc bool
}

// addString копирует максимум, сколько осталось до лимита. Возвращает true,
// если строка скопирована целиком (для onLine: не отдаём в UI обрезанную).
func (s *outputSink) addString(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := maxOutputBytes - s.total
	if room <= 0 {
		s.trunc = true
		return false
	}
	if len(p) > room {
		s.sb.WriteString(p[:room])
		s.total = maxOutputBytes
		s.trunc = true
		return false
	}
	s.sb.WriteString(p)
	s.total += len(p)
	return true
}

// addBytes — то же для io.Copy (без лишней конвертации в string).
func (s *outputSink) addBytes(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := maxOutputBytes - s.total
	if room <= 0 {
		s.trunc = true
		return
	}
	if len(p) > room {
		s.sb.Write(p[:room])
		s.total = maxOutputBytes
		s.trunc = true
		return
	}
	s.sb.Write(p)
	s.total += len(p)
}

func (s *outputSink) Write(p []byte) (int, error) {
	s.addBytes(p)
	return len(p), nil
}

func (s *outputSink) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sb.String()
}

func (s *outputSink) truncated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.trunc
}

// RunAPI запускает команду cmd как подпроцесс, поднимает socketpair на fd 3,
// гоняет Serve(conn, api) до завершения cmd и возвращает CombinedOutput
// (stdout+stderr скрипта). ВАЖНО: cmd обязан быть создан через
// exec.CommandContext(ctx, ...) — так отмена ctx реально убивает подпроцесс
// (иначе зависший скрипт заморозил бы запуск, M32-ревью). Go не даёт вручную
// ставить cmd.Cancel на команде без CommandContext (см. exec.Cmd), поэтому
// здесь это НЕ выставляем — контроль остаётся на контексте команды.
// onLine — опциональный колбэк для каждой СТРОКИ stdout подпроцесса.
// Реальный API-бэкенд (действия над живыми сессиями) внедряется как api
// (это сделает UI-слой при подключении скрипта).
func RunAPI(ctx context.Context, cmd *exec.Cmd, api API, onLine func(string)) (string, error) {
	// socketpair: служебный канал RPC. Один конец — дочерний (fd 3), другой — наш.
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return "", err
	}
	childFile := os.NewFile(uintptr(fds[0]), "rpc-child")
	parentFile := os.NewFile(uintptr(fds[1]), "rpc-parent")
	defer parentFile.Close()
	// ExtraFiles → дочерний процесс получает его как fd 3.
	cmd.ExtraFiles = append(cmd.ExtraFiles, childFile)

	parentConn, err := net.FileConn(parentFile)
	if err != nil {
		childFile.Close()
		return "", err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		parentConn.Close()
		childFile.Close()
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		parentConn.Close()
		childFile.Close()
		return "", err
	}

	if err := cmd.Start(); err != nil {
		parentConn.Close()
		childFile.Close()
		return "", err
	}
	// дочерний конец можно закрыть в родителе (он уже передан подпроцессу).
	childFile.Close()

	// горутина-диспетчер: читает запросы от подпроцесса, вызывает api.
	// При её завершении (Serve вернулся — bad-line >maxRPCLine, write-error,
	// EOF) закрываем parentConn: разблокирует дочерний процесс, если тот
	// продолжает писать в fd 3 без читателя (иначе child блокируется на
	// socket-write, а родитель — в cmd.Wait(); вторичный deadlock).
	srvDone := make(chan struct{})
	go func() {
		defer close(srvDone)
		_ = Serve(parentConn, api)
		parentConn.Close()
	}()

	// Собираем stdout+stderr ПАРАЛЛЕЛЬНО (ревью-дедлок): если читать stdout
	// целиком, потом stderr, то при большом (>pipe-buffer ~64КиБ) stderr у живого
	// ребёнка: child блокируется на stderr-write, родитель — на stdout-read EOF
	// (которого нет, т.к. child не вышел) → взаимоблок. Раздельные горутины
	// дренируют оба pipe независимо. onLine остаётся контрактом «stdout-строки».
	// Вывод ограничен outputSink (maxOutputBytes суммарно).
	var sink outputSink
	var owg sync.WaitGroup
	owg.Add(2)
	go func() {
		defer owg.Done()
		defer stdout.Close() // M7: не оставлять pipe открытым — иначе при неразрывной
		// строке >1МиБ (ErrTooLong) child блокируется на write, родитель в cmd.Wait()
		// (дедлок). Закрытие даёт child EPIPE/EOF → он выходит.
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 4096), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if sink.addString(line+"\n") && onLine != nil {
				onLine(line)
			}
		}
	}()
	go func() {
		defer owg.Done()
		defer stderr.Close()
		// стderr дренируем io.Copy БЕЗ токен-лимита: bufio.Scanner с токеном
		// >1 МиБ (неразрывная длинная строка без \n) мог бы снова заблокировать
		// child на stderr-write (ревью MINOR). io.Copy не буферизует по токенам.
		// outputSink.Write всегда возвращает len(p) — дренаж не прекращается.
		_, _ = io.Copy(&sink, stderr)
	}()

	// os/exec docs: Wait закрывает pipe после выхода процесса и НЕ должен
	// вызываться, пока читатели не дочитали до EOF (иначе теряется хвост).
	// Поэтому сначала ждём оба ридера, и только потом Wait.
	//
	// ag-sz9: EOF не гарантирован — скрипт может оставить фоновый потомок
	// (daemon & / nohup … &), который наследует write-концы stdout/stderr и
	// держит pipe открытым. exec.CommandContext убивает только ПРЯМОГО ребёнка;
	// фон продолжает жить, и owg.Wait() висел бы вечно, даже после отмены ctx.
	// Поэтому дренаж ctx-aware: по ctx.Done() закрываем read-концы — os.File
	// прерывает блокирующий Read через poller, читатели завершаются, RunAPI
	// возвращает ctx.Err(). Порядок «дренаж до Wait» сохранён, чтобы не терять
	// хвост вывода при штатном завершении (TestRunAPIOutputTailComplete).
	// Границы: закрытие pipe разблокирует только чтение — если onLine или
	// API-колбэк завис, owg.Wait()/srvDone всё равно его ждут. Побочный эффект
	// отмены: write выжившего фона в закрытый pipe даёт EPIPE/SIGPIPE (daemon
	// может умереть). Без отмены (context.Background() у вызывающего) фон
	// по-прежнему держит RunAPI — для ограниченного прогона нужен WithTimeout.
	drainDone := make(chan struct{})
	defer close(drainDone)
	go func() {
		select {
		case <-ctx.Done():
			// Двойное закрытие безопасно: читатели закрывают те же файлы в defer.
			_ = stdout.Close()
			_ = stderr.Close()
		case <-drainDone:
		}
	}()

	owg.Wait()
	waitErr := cmd.Wait()
	// закрываем RPC-канал после завершения подпроцесса (диспетчер выйдет).
	parentConn.Close()
	<-srvDone

	out := sink.String()
	if sink.truncated() {
		out += outputTruncatedMarker
	}
	// контекст отменён (таймаут) — возвращаем ошибку вместе с буфером.
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, waitErr
}
