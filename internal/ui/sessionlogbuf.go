package ui

import "bytes"

// sessionLogBuf — ограниченный кольцевой буфер вывода вкладки (для «Сохранить
// лог сессии»). Не растёт бесконечно: держит последние max байт.
type sessionLogBuf struct {
	max int
	buf bytes.Buffer
}

const sessionLogMax = 4 << 20 // 4 МБ — разумный предел памяти на вкладку

func newSessionLogBuf() *sessionLogBuf {
	return &sessionLogBuf{max: sessionLogMax}
}

// append — добавляет байты; при превышении max отбрасывает начало.
func (b *sessionLogBuf) append(p []byte) {
	if len(p) == 0 {
		return
	}
	b.buf.Write(p)
	if b.buf.Len() > b.max {
		cur := b.buf.Bytes()
		drop := b.buf.Len() - b.max
		copy(cur, cur[drop:])
		b.buf.Truncate(b.max)
	}
}

// bytes — текущее содержимое.
func (b *sessionLogBuf) bytes() []byte { return b.buf.Bytes() }

// len — число байт.
func (b *sessionLogBuf) len() int { return b.buf.Len() }

// reset — очистить.
func (b *sessionLogBuf) reset() { b.buf.Reset() }
