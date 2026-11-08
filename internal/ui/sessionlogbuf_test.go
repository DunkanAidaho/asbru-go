package ui

import (
	"bytes"
	"testing"
)

// sessionLogBuf — кольцевой буфер удерживает последние max байт вывода.
func TestSessionLogBufKeepsTail(t *testing.T) {
	b := newSessionLogBuf()
	b.max = 16                           // маленький предел для теста
	b.append([]byte("0123456789ABCDEF")) // 16 байт
	if b.len() != 16 {
		t.Fatalf("len = %d, want 16", b.len())
	}
	b.append([]byte("G")) // 17-й → отбрасывает '0'
	if got := string(b.bytes()); got != "123456789ABCDEFG" {
		t.Fatalf("после превышения = %q, want '123456789ABCDEFG'", got)
	}
}

// append больших чанков не растёт сверх max и сохраняет хвост.
func TestSessionLogBufLargeAppend(t *testing.T) {
	b := newSessionLogBuf()
	b.max = 10
	b.append([]byte("aaaaaaa")) // 7
	b.append([]byte("bbbbbbb")) // 14 → отброс
	if b.len() != 10 {
		t.Fatalf("len = %d, want 10", b.len())
	}
	got := string(b.bytes())
	if !bytes.Contains([]byte(got), []byte("bbbbbb")) {
		t.Fatalf("хвост потерян: %q", got)
	}
}

// removeEscapeSeqs — вырезает ESC/CSI/OSC-последовательности, оставляет текст.
func TestRemoveEscapeSeqs(t *testing.T) {
	in := "\x1b]0;title\x07\x1b[31mRED\x1b[0m plain\nline"
	got := removeEscapeSeqs(in)
	if got != "RED plain\nline" {
		t.Fatalf("removeEscapeSeqs = %q, want %q", got, "RED plain\nline")
	}
}

// M36-ревью MINOR: неитерминированный OSC (без BEL/ST) не утекает payload в лог.
func TestRemoveEscapeSeqsUnterminatedOSC(t *testing.T) {
	if got := removeEscapeSeqs("abc\x1b]0;titleDEF"); got != "abc" {
		t.Fatalf("unterminated OSC = %q, want 'abc' (payload убран)", got)
	}
}

// M36-ревью MINOR: 3-байтный ESC-код (ESC ( B) не утекает final-байт.
func TestRemoveEscapeSeqs3ByteESC(t *testing.T) {
	if got := removeEscapeSeqs("\x1b(Babc"); got != "abc" {
		t.Fatalf("3-byte ESC = %q, want 'abc'", got)
	}
}
