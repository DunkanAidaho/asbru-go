package wgt

import (
	"io"
	"testing"
	"time"
)

func syncPost(fn func()) { fn() }

// feedQt имитирует сигнал sendData из Qt-нити (cb.Data — колбэк типа TermStream).
func feedQt(id int, s string) {
	mu.Lock()
	cb := callbacks[id]
	mu.Unlock()
	cb.Data([]byte(s))
}

// TermStream: канал из sendData-колбэков → Read; Write → Feed; Close → EOF.
func TestTermStreamReadWrite(t *testing.T) {
	s := NewTermStream(4242, syncPost)

	feedQt(4242, "root\n")
	feedQt(4242, "secret\n")

	buf := make([]byte, 64)
	n, err := s.Read(buf)
	if err != nil || string(buf[:n]) != "root\n" {
		t.Fatalf("read1 = %q, %v", buf[:n], err)
	}
	// вычитываем накопленный остаток
	n, err = s.Read(buf)
	if err != nil || string(buf[:n]) != "secret\n" {
		t.Fatalf("read2 = %q, %v", buf[:n], err)
	}

	go func() {
		time.Sleep(30 * time.Millisecond)
		s.Close()
	}()
	_, err = s.Read(buf)
	if err == nil {
		t.Fatal("после Close и исчерпания очереди Read должен вернуть io.EOF")
	}

	if n, err := s.Write([]byte("hi")); err != nil || n != 2 {
		t.Fatalf("Write = %d, %v", n, err)
	}
}

// Двойной Close идемпотентен.
func TestTermStreamCloseOnce(t *testing.T) {
	s := NewTermStream(777, syncPost)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	_, err := s.Read(make([]byte, 1))
	if err != io.EOF {
		t.Fatalf("после Close: %v", err)
	}
}

// Частичные чтения: маленький буфер не теряет хвост чанка (ревью M1).
func TestTermStreamPartialRead(t *testing.T) {
	s := NewTermStream(555, syncPost)
	feedQt(555, "0123456789ABCDEF")

	buf := make([]byte, 4)
	var got []byte
	for len(got) < 16 {
		n, err := func() (int, error) {
			type r struct {
				n int
				e error
			}
			ch := make(chan r, 1)
			go func() {
				n, err := s.Read(buf)
				ch <- r{n, err}
			}()
			select {
			case v := <-ch:
				return v.n, v.e
			case <-time.After(2 * time.Second):
				t.Fatalf("Read завис после %d байт (%q)", len(got), got)
				return 0, nil
			}
		}()
		if err != nil {
			t.Fatalf("Read: %v on %q", err, got)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != "0123456789ABCDEF" {
		t.Fatalf("частичные чтения потеряли данные: %q", got)
	}
}
