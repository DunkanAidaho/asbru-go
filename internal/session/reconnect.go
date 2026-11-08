package session

import (
	"context"
	"time"
)

// M24: автопереподключение (легаси PACTerminal.pm:1409-1424, asbru_conn
// RESTART): поле environments.autoreconnect; max retry on disconnect из
// defaults (PACUtils.pm:2053, дефолт 50). Ожидание между попытками —
// простые числа (легаси-список до 500).

var (
	// waitSleep — пауза перед попыткой; ctx-aware (Finding #7: отмена ctx
	// прерывает backoff). Возвращает false, если ожидание отменено.
	// Тесты подменяют на no-op/свой контрол.
	waitSleep = func(ctx context.Context, d time.Duration) bool {
		if d <= 0 {
			return ctx.Err() == nil
		}
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
	// reconnectWait — прайм-прогрессия по номеру попытки (легаси-список).
	reconnectWait = primeWait
)

// primeWait — простые числа до 500 (движок легаси с regex-фильтром).
func primeWait(n int) time.Duration {
	if n >= len(primes) {
		n = len(primes) - 1
	}
	if n < 0 {
		n = 0
	}
	return time.Duration(primes[n]) * time.Second
}

var primes = []int{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53,
	59, 61, 67, 71, 73, 79, 83, 89, 97, 101, 103, 107, 109, 113, 127, 131, 137,
	139, 149, 151, 157, 163, 167, 173, 179, 181, 191, 193, 197, 199, 211, 223,
	227, 229, 233, 239, 241, 251, 257, 263, 269, 271, 277, 281, 283, 293, 307,
	311, 313, 317, 331, 337, 347, 349, 353, 359, 367, 373, 379, 383, 389, 397,
	401, 409, 419, 421, 431, 433, 439, 443, 449, 457, 461, 463, 467, 479, 487,
	491, 499}

// WithRetries — max-число попыток переподключения (defaults
// 'max retry on disconnect').
func WithRetries(max int) Option {
	return func(o *sessionOptions) { o.retries = max }
}
