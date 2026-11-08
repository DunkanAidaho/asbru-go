// Пакет lock: single-instance блокировка через flock (вместо сокета PACPipe).
package lock

import (
	"errors"

	"github.com/gofrs/flock"
)

// ErrHeld — блокировка уже удержана другим процессом (ошибка single-instance).
var ErrHeld = errors.New("asbru: уже запущен (блокировка конфигурации удержана)")

// Acquire берёт эксклюзивную блокировку path (неблокирующе).
// Возвращает release-функцию; вторая попытка — ErrHeld.
func Acquire(path string) (release func(), err error) {
	fl := flock.New(path)
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrHeld
	}
	return func() { _ = fl.Unlock() }, nil
}
