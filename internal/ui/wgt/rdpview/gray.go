package rdpview

/*
#include "bridge.h"
*/
import "C"

import "unsafe"

// grayFrame — обёртка над чистым C-хелпером обесцвечивания кадра (../grayframe.h):
// переводит кадр 32bpp [B,G,R,X] в градации серого in place.
//
// Обёртка существует ради тестов: cgo в _test.go запрещён («use of cgo in test
// not supported»), как у обёртки колеса (wheel.go).
func grayFrame(bgra []byte, w, h, stride int) {
	if len(bgra) == 0 {
		return
	}
	C.asbru_gray_frame((*C.uint8_t)(unsafe.Pointer(&bgra[0])), C.int(w), C.int(h), C.int(stride))
}
