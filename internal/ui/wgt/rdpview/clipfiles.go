package rdpview

/*
#include <stdint.h>
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import "unsafe"

// cPathArray — массив NUL-строк для C-хелперов (`const char *const *`).
// Возвращает cleanup, который освобождает все копии; вызывать после C-вызова.
func cPathArray(paths []string) ([]*C.char, func()) {
	cp := make([]*C.char, len(paths))
	for i, p := range paths {
		cp[i] = C.CString(p)
	}
	return cp, func() {
		for _, p := range cp {
			C.free(unsafe.Pointer(p))
		}
	}
}

// uriListBuild — обёртка над rdp_uri_list_build (bridge.h): локальные пути →
// CRLF-разделённый text/uri-list с file:// и percent-encoding. Двухпроходно:
// первый вызов с out=NULL даёт требуемую длину. cgo в _test.go запрещён,
// поэтому обёртка живёт здесь.
func uriListBuild(paths []string) ([]byte, bool) {
	if len(paths) == 0 {
		return nil, false
	}
	cp, free := cPathArray(paths)
	defer free()

	n := C.rdp_uri_list_build(&cp[0], C.uint32_t(len(cp)), nil, 0)
	if n == 0 {
		return nil, false
	}
	out := make([]byte, int(n))
	got := C.rdp_uri_list_build(&cp[0], C.uint32_t(len(cp)),
		(*C.uint8_t)(unsafe.Pointer(&out[0])), n)
	if got != n {
		return nil, false
	}
	return out, true
}

// cliprdrFileListBuild — обёртка над rdp_cliprdr_file_list_alloc (bridge.h):
// локальные пути → CLIPRDR_FILELIST (данные формата FileGroupDescriptorW) с
// рекурсивным обходом каталогов. Порядок дескрипторов совпадает с обходом
// FreeRDP: от него зависит listIndex в FILECONTENTS_REQUEST.
//
// Сборка идёт ОДНИМ обходом (ag-32m: раньше — два прохода, подсчёт и запись) и
// под жёсткими пределами; при их превышении возвращается false.
func cliprdrFileListBuild(paths []string) ([]byte, bool) {
	if len(paths) == 0 {
		return nil, false
	}
	cp, free := cPathArray(paths)
	defer free()

	var n C.uint32_t
	var reason C.int
	buf := C.rdp_cliprdr_file_list_alloc(&cp[0], C.uint32_t(len(cp)), &n, &reason)
	if buf == nil {
		return nil, false
	}
	defer C.rdp_free(unsafe.Pointer(buf))
	return C.GoBytes(unsafe.Pointer(buf), C.int(n)), true
}

// cliprdrFileListBuildLimits — та же сборка с явными пределами (0 = без
// предела). Нужна тестам, чтобы проверить отказ без создания десятков тысяч
// файлов.
func cliprdrFileListBuildLimits(paths []string, maxItems, maxBytes, maxMs uint32) ([]byte, bool) {
	if len(paths) == 0 {
		return nil, false
	}
	cp, free := cPathArray(paths)
	defer free()

	var n C.uint32_t
	var reason C.int
	buf := C.rdp_cliprdr_file_list_alloc_limits(
		&cp[0], C.uint32_t(len(cp)),
		C.uint32_t(maxItems), C.uint32_t(maxBytes), C.uint32_t(maxMs),
		&n, &reason)
	if buf == nil {
		return nil, false
	}
	defer C.rdp_free(unsafe.Pointer(buf))
	return C.GoBytes(unsafe.Pointer(buf), C.int(n)), true
}
