package rdpview

/*
#include <stdint.h>
#include "bridge.h"
*/
import "C"

import "unsafe"

// dibFromBmp — обёртка над чистым C-хелпером rdp_dib_strip_bmp_header
// (bridge.h): QImageWriter("BMP") пишет BMP-файл, а cliprdr на проводе ждёт
// ГОЛЫЙ DIB (CF_DIB), поэтому 14-байтный BITMAPFILEHEADER срезается.
//
// Обёртка существует ради тестов: cgo в _test.go запрещён («use of cgo in test
// not supported»), а разбор заголовка — ровно то место, где легко ошибиться
// (усечённый файл, чужая магия).
func dibFromBmp(bmp []byte) ([]byte, bool) {
	if len(bmp) == 0 {
		return nil, false
	}
	var dibLen C.uint32_t
	ok := C.rdp_dib_strip_bmp_header(
		(*C.uint8_t)(unsafe.Pointer(&bmp[0])),
		C.uint32_t(len(bmp)), &dibLen)
	if ok == 0 || dibLen == 0 || int(dibLen) > len(bmp)-14 {
		return nil, false
	}
	return bmp[14 : 14+int(dibLen)], true
}

// dibPixelOffset — обёртка над rdp_dib_pixel_offset (bridge.h): смещение
// пикселей внутри голого DIB (biSize + палитра + маски BI_BITFIELDS + профиль
// V5). 0 — DIB невалиден/усечён.
func dibPixelOffset(dib []byte) uint32 {
	if len(dib) == 0 {
		return 0
	}
	return uint32(C.rdp_dib_pixel_offset(
		(*C.uint8_t)(unsafe.Pointer(&dib[0])), C.uint32_t(len(dib))))
}

// bmpFromDib — обёртка над rdp_bmp_from_dib (bridge.h): сервер→локальный DIB
// превращается в BMP-файл (14-байтный BITMAPFILEHEADER + DIB), который умеет
// читать QImage::fromData. ok=false — DIB невалиден/усечён.
func bmpFromDib(dib []byte) ([]byte, bool) {
	if len(dib) == 0 {
		return nil, false
	}
	out := make([]byte, 14+len(dib))
	n := C.rdp_bmp_from_dib(
		(*C.uint8_t)(unsafe.Pointer(&dib[0])), C.uint32_t(len(dib)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])), C.uint32_t(len(out)))
	if n == 0 || int(n) > len(out) {
		return nil, false
	}
	return out[:int(n)], true
}
