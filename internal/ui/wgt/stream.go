package wgt

import (
	"io"
	"sync"
)

// Post — выполнение колбэка на Qt-нити (production: miqt/mainthread.Wait,
// тесты: прямой вызов).
type Post func(fn func())

// TermStream — сессионный поток поверх телетайп-виджета (адаптация
// session.Stream ↔ QTermWidget P0):
//
//	Read  — нажатия пользователя (сигнал sendData → канал, Qt-нить);
//	Write — вывод сессии в эмулятор (запись в pty-slave виджета);
//	Close — снятие колбэков и удаление виджета (через Post на Qt-нить).
type TermStream struct {
	id   int
	post Post

	in      chan []byte
	pending []byte
	closed  chan struct{}
	once    sync.Once
}

// NewTermStream регистрирует колбэки виджета на канал потока.
func NewTermStream(id int, post Post) *TermStream {
	s := &TermStream{
		id:     id,
		post:   post,
		in:     make(chan []byte, 16),
		closed: make(chan struct{}),
	}
	mu.Lock()
	callbacks[id] = &Callback{
		Data: func(b []byte) {
			select {
			case s.in <- b:
			case <-s.closed:
			default: // неблокирующий ввод и из Qt-нити (ревью C2a)
			}
		},
	}
	mu.Unlock()
	return s
}

// Read — доставка чанка с виджета; при частичном копировании остаток
// сохраняется и отдаётся следующими вызовами (ревью M1 — без потерь).
func (s *TermStream) Read(p []byte) (int, error) {
	if len(s.pending) > 0 {
		return s.servePending(p), nil
	}
	select {
	case b := <-s.in:
		s.pending = b
		return s.servePending(p), nil
	case <-s.closed:
		return 0, io.EOF
	}
}

func (s *TermStream) servePending(p []byte) int {
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	if len(s.pending) == 0 {
		s.pending = nil
	}
	return n
}

// Write — вывод сессии в эмулятор (системный write в pty-slave,
// безопасен из любой нити).
func (s *TermStream) Write(p []byte) (int, error) {
	Feed(s.id, p)
	return len(p), nil
}

// Close — идемпотентное снятие колбэков и удаление виджета.
func (s *TermStream) Close() error {
	s.once.Do(func() {
		close(s.closed)
		s.post(func() {
			Destroy(s.id)
		})
	})
	return nil
}
