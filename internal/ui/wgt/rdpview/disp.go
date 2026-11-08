package rdpview

/*
#include "bridge.h"
*/
import "C"

// dispResizeTarget — обёртка над чистым C-хелпером целевого размера десктопа
// для DisplayControl (динамическое разрешение, см. bridge.h): физические пиксели
// виджета (логический размер × DPR), кламп в протокольные пределы 200..8192
// и сравнение с текущим размером десктопа. Возвращает (w, h, слать ли).
//
// Обёртка существует ради тестов: cgo в _test.go запрещён («use of cgo in test
// not supported»), как у обёрток колеса (wheel.go) и обесцвечивания (gray.go).
func dispResizeTarget(widgetW, widgetH int, dpr float64, curW, curH int) (int, int, bool) {
	var w, h C.int
	ok := C.rdp_disp_resize_target(C.int(widgetW), C.int(widgetH), C.double(dpr),
		C.int(curW), C.int(curH), &w, &h)
	return int(w), int(h), ok != 0
}
