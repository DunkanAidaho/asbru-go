// Package vncview — cgo-мост к встроенному VNC-клиенту (libvncclient 0.9.15)
// для M33-б: Qt-канвас во вкладке, фреймбуфер [BGRA] → QImage. Логика клиента
// живёт на C++-нити; Go получает колбэки готовности кадра и завершения.
package vncview

/*
#cgo pkg-config: Qt6Widgets libvncclient
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lstdc++
#include <stdlib.h>
#include "bridge.h"

// Колбэки, экспортируемые в Go (объявления ДО //export; сигнатуры СОВПАДАЮТ).
extern void vncGoFrame(int id, int w, int h);
extern void vncGoDone(int id, int ok, char *reason);
// M39-fullscreen: Esc в полноэкранном режиме вкладки → локальный выход.
extern void vncGoFullscreen(int id);
*/
import "C"

import (
	"sync"
	"unsafe"
)

// Callback — события из C++-нити (не Qt): кадр готов и клиент завершился.
type Callback struct {
	Frame func(w, h int)               // новый кадр (worker-нить)
	Done  func(ok bool, reason string) // клиент завершился (worker-нить)
	// Fullscreen — в полноэкранном режиме вкладки нажат одиночный Esc
	// (M39-fullscreen). Вызывается на Qt-нити; Go возвращает вкладку к
	// обычному размеру. Esc в сессию при этом НЕ уходит.
	Fullscreen func()
}

var (
	mu        sync.Mutex
	callbacks = map[int]*Callback{}
)

//export vncGoFrame
func vncGoFrame(id C.int, w C.int, h C.int) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Frame != nil {
		cb.Frame(int(w), int(h))
	}
}

//export vncGoDone
func vncGoDone(id C.int, ok C.int, reason *C.char) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Done != nil {
		cb.Done(ok != 0, C.GoString(reason))
	}
}

//export vncGoFullscreen
func vncGoFullscreen(id C.int) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Fullscreen != nil {
		cb.Fullscreen()
	}
}

// Create создаёт Qt-канвас (только Qt-нить). id>0 — успех.
func Create(cb *Callback) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	id := int(C.vnc_create())
	if id <= 0 {
		return 0, errNoCanvas
	}
	callbacks[id] = cb
	return id, nil
}

// Native — нативный QWidget* (для вставки во вкладку через miqt).
func Native(id int) unsafe.Pointer {
	return C.vnc_native(C.int(id))
}

// Start запускает клиент на C++-нити (неблокирующий; НЕ Qt-нить).
// security — VeNCrypt-режим: "auto" (дефолт), "vencrypt" (форс), "none" (запрет).
func Start(id int, host string, port int, user, pass, title, security string) {
	ch := C.CString(host)
	cu := C.CString(user)
	cp := C.CString(pass)
	ct := C.CString(title)
	cs := C.CString(security)
	// копируются сразу внутри C++; освобождаем C-строки после вызова.
	C.vnc_start(C.int(id), ch, C.int(port), cu, cp, ct, cs)
	C.free(unsafe.Pointer(ch))
	C.free(unsafe.Pointer(cu))
	// Пароль — секрет: затираем C-копию перед free (C++ уже скопировал строку
	// в std::string m_pass и затирает её в деструкторе; см. отчёт).
	zeroBytes(unsafe.Slice((*byte)(unsafe.Pointer(cp)), len(pass)))
	C.free(unsafe.Pointer(cp))
	C.free(unsafe.Pointer(ct))
	C.free(unsafe.Pointer(cs))
}

// zeroBytes — стереть байты в C-памяти перед освобождением.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Stop просит worker выйти (без Qt; безопасно из любой нити).
func Stop(id int) { C.vnc_stop(C.int(id)) }

// SetFullscreen помечает канвас как живущий в полноэкранном режиме вкладки:
// пока on, одиночный Esc вызывает Callback.Fullscreen вместо отправки в сессию.
// Только Qt-нить.
func SetFullscreen(id int, on bool) {
	v := C.int(0)
	if on {
		v = 1
	}
	C.vnc_set_fullscreen(C.int(id), v)
}

// Destroy убирает канвас (Qt-нить): stop + join + deleteLater.
func Destroy(id int) {
	mu.Lock()
	delete(callbacks, id)
	mu.Unlock()
	C.vnc_destroy(C.int(id))
}

var errNoCanvas = &canvasError{"vncview: создать канвас не удалось"}

type canvasError struct{ msg string }

func (e *canvasError) Error() string { return e.msg }
