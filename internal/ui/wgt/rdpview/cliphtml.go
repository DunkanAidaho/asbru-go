package rdpview

/*
#include <stdint.h>
#include "bridge.h"
*/
import "C"

import "unsafe"

// Длина заголовка CF_HTML, которую строит rdp_cf_html_build: Version:0.9 плюс
// четыре поля по 10 цифр. Постоянна, поэтому смещения в payload — байтовые.
const cfHTMLHeaderLen = 105

// isASCII — обёртка над rdp_text_is_ascii (bridge.h): CF_TEXT допустим только
// для чистого ASCII (иначе ANSI-кодовая страница Windows превратит UTF-8 в
// мозаику). Пустой текст считается ASCII.
func isASCII(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	return C.rdp_text_is_ascii(
		(*C.uint8_t)(unsafe.Pointer(&b[0])), C.uint32_t(len(b))) != 0
}

// cfHTMLBuild — обёртка над rdp_cf_html_build (bridge.h): UTF-8 HTML →
// CF_HTML payload для провода. Маркеры StartFragment/EndFragment в HTML
// сохраняются как границы фрагмента. ok=false — пустой HTML/слишком большой
// буфер (cgo в _test.go запрещён, поэтому обёртка в обычном файле).
func cfHTMLBuild(html []byte) ([]byte, bool) {
	if len(html) == 0 || uint64(len(html))+cfHTMLHeaderLen > 0xFFFFFFFF {
		return nil, false
	}
	out := make([]byte, len(html)+cfHTMLHeaderLen)
	n := C.rdp_cf_html_build(
		(*C.uint8_t)(unsafe.Pointer(&html[0])), C.uint32_t(len(html)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])), C.uint32_t(len(out)))
	if n == 0 || int(n) > len(out) {
		return nil, false
	}
	return out[:int(n)], true
}

// cfHTMLParse — обёртка над rdp_cf_html_parse (bridge.h): байтовые границы
// фрагмента [start,end) внутри payload. Header → маркеры → весь payload;
// явно испорченный заголовок отвергается.
func cfHTMLParse(payload []byte) (start, end uint32, ok bool) {
	if len(payload) == 0 {
		return 0, 0, false
	}
	var s, e C.uint32_t
	n := C.rdp_cf_html_parse(
		(*C.uint8_t)(unsafe.Pointer(&payload[0])), C.uint32_t(len(payload)),
		&s, &e)
	if n == 0 {
		return 0, 0, false
	}
	return uint32(s), uint32(e), true
}
