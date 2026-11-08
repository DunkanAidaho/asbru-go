package rdpview

/*
#include "bridge.h"
*/
import "C"

// wheelStep — обёртка над чистым C-хелпером кодирования колеса (bridge.h):
// накапливает вращение в единицах WHEEL_DELTA (120 = щелчок) и возвращает флаги
// указателя для очередного события, либо 0, если целого щелчка ещё не набралось.
//
// Обёртка существует ради тестов: cgo в _test.go запрещён («use of cgo in test
// not supported»), а кодирование вращения — ровно то место, где колесо RDP
// ломалось (выставлялся только бит знака, величина вращения терялась).
func wheelStep(accum *int32, dy int32) uint16 {
	a := C.int32_t(*accum)
	f := C.rdp_wheel_step(&a, C.int32_t(dy))
	*accum = int32(a)
	return uint16(f)
}

// hwheelStep — обёртка над rdp_hwheel_step (горизонтальное колесо, M40-
// остаток): тот же накопитель щелчков, флаг PTR_FLAGS_HWHEEL.
func hwheelStep(accum *int32, dx int32) uint16 {
	a := C.int32_t(*accum)
	f := C.rdp_hwheel_step(&a, C.int32_t(dx))
	*accum = int32(a)
	return uint16(f)
}
