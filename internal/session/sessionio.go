package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/method"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

var errNoCommand = errors.New("empty command line")

// vncpasswdPath — бинарник шифрования пароля TigerVNC (инжектируемо в тестах).
var vncpasswdPath = "vncpasswd"

// spawnPTY — реальный канал связи: дочерняя команда в PTY (без sh-обёртки).
func (s *Session) spawnPTY(pe *config.Environment) (io.ReadWriteCloser, error) {
	argv, err := BuildCmd(s.env, pe, s.vncBackend())
	if err != nil {
		return nil, err
	}
	if len(argv) == 0 {
		return nil, errNoCommand
	}
	if isVNCMethod(pe.Method) && s.vncBackend() == method.VNCTigerVNC {
		// -PasswordFile обязан существовать до старта (ревью H3):
		// легаси создавал его через `vncpasswd -f`.
		if err := writeVNCPassFile(s.env.TmpDir, s.env.UUID, pe.Pass); err != nil {
			return nil, err
		}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}
	return newPTYStream(cmd, f), nil
}

// ptyStream — PTY-поток + код завершения дочернего процесса (M24): легаси
// переподключается только при $why ne 0 (asbru_conn:2258), поэтому чистый
// выход (ssh exit 0, пользовательский exit) должен отличаться от обрыва.
type ptyStream struct {
	f    *os.File
	cmd  *exec.Cmd
	done chan struct{} // закрывается после cmd.Wait() (M3: не читать ExitStatus до)

	mu   sync.Mutex
	code int
	have bool
}

// newPTYStream — Wait() в фоне: процесс репится сразу (без зомби), а
// ExitStatus остаётся неблокирующим.
func newPTYStream(cmd *exec.Cmd, f *os.File) *ptyStream {
	p := &ptyStream{f: f, cmd: cmd, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if err == nil {
			p.code, p.have = 0, true
		} else if ee, ok := err.(*exec.ExitError); ok {
			p.code, p.have = ee.ExitCode(), true
		}
		p.mu.Unlock()
		close(p.done)
	}()
	return p
}

// Done — канал, закрываемый после завершения cmd.Wait() (M3: дождаться, чтобы
// ExitStatus был достоверным — иначе «чистый exit 0» уходит в exitRemote/reconnect).
func (p *ptyStream) Done() <-chan struct{} { return p.done }

func (p *ptyStream) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *ptyStream) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *ptyStream) Close() error                { return p.f.Close() }

// ExitStatus — код выхода дочернего процесса; ok=false: ещё не завершился
// или статус недоступен (exitStatus-контракт, session.go).
func (p *ptyStream) ExitStatus() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, p.have
}

// vncStdinPassword — байты для stdin standalone VNC-процесса (легаси
// `echo "$PASS" | vncviewer ...`, asbru_conn:1284-1285). Только для VNCGeneric:
// бэкенды с файлом пароля (TigerVNC -PasswordFile / RealVNC UserName) пароль в
// stdin не принимают. Пустой Pass → nil.
// isVNCMethod — метод подключения, для которого вообще нужен VNC-бэкенд
// (пароль-файл/согласование). Держать отдельно от s.vncBackend(): detection
// видит установленный vncviewer глобально и не должен влиять на SSH/Telnet.
func isVNCMethod(m string) bool {
	return m == "VNC" || m == "vncviewer"
}

func vncStdinPassword(e *config.Environment, backend method.VNCBackend) []byte {
	if e == nil {
		return nil
	}
	if backend != method.VNCGeneric {
		return nil
	}
	if !isVNCMethod(e.Method) {
		return nil
	}
	if e.Pass == "" {
		return nil
	}
	return []byte(e.Pass + "\n")
}

// feedPasswordStdin — запись пароля в stdin дочернего процесса. Для реального
// PTY на время записи выключается ECHO (иначе пароль светился бы в терминале —
// легаси кормил пайпом, где эха нет); фейковые потоки в тестах — обычный Write.
func feedPasswordStdin(w io.Writer, p []byte) {
	if ps, ok := w.(*ptyStream); ok {
		setPtyEcho(ps.f, false)
		defer setPtyEcho(ps.f, true)
	}
	_, _ = w.Write(p)
}

// setPtyEcho — включить/выключить ECHO на pty-слейве (через мастер-файл;
// мастер и слейв разделяют termios).
func setPtyEcho(f *os.File, on bool) {
	if f == nil {
		return
	}
	t, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return
	}
	if on {
		t.Lflag |= unix.ECHO
	} else {
		t.Lflag &^= unix.ECHO
	}
	_ = unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, t)
}

// writeVNCPassFile создаёт зашифрованный файл пароля TigerVNC (0600).
func writeVNCPassFile(dir, uuid, pass string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(vncpasswdPath, "-f")
	cmd.Stdin = strings.NewReader(pass)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("vncpasswd: %w", err)
	}
	// Security (sec-ревью MINOR): uuid — clave конфига (path traversal при
	// импорте недоверенного YAML). Санитизируем как в writeProxyPass.
	path := filepath.Join(dir, "asbru_conn_"+sanitizeFileToken(uuid)+".pass")
	return os.WriteFile(path, out, 0o600)
}

// vncHelp — вывод `vncviewer --help` (инжектируемо в тестах).
// Таймаут 2с: битая обёртка не вешает старт VNC-сессии (L2-ревью).
var vncHelp = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vncviewer", "--help").CombinedOutput()
	return string(out), err
}

// vncDetector — детект бинарника по --help (легаси asbru_conn:1263-1267),
// кэш на процесс; тесты подменяют vncD целиком (без копии noCopy).
type vncDetector struct {
	once *sync.Once
	back method.VNCBackend
}

var vncD = &vncDetector{once: new(sync.Once)}

func (d *vncDetector) get() method.VNCBackend {
	d.once.Do(func() {
		help, err := vncHelp()
		if err != nil {
			d.back = method.VNCGeneric
			return
		}
		d.back = method.DetectVNC(help)
	})
	return d.back
}

func (s *Session) vncBackend() method.VNCBackend {
	if s.opts.vnc != "" {
		return s.opts.vnc
	}
	return vncD.get()
}
