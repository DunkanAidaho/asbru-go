// Package rdpview — cgo-мост к встроенному RDP-клиенту (libfreerdp3 3.30)
// для M33-а: Qt-канвас во вкладке, фреймбуфер [BGRX32] → QImage. Логика клиента
// живёт на C++-нити; Go получает колбэки готовности кадра и завершения.
package rdpview

/*
// M39: freerdp-client3 нужен для каналов (freerdp_client_load_addins и
// freerdp_channels_load_static_addin_entry живут в libfreerdp-client3, не в
// libfreerdp3) — без него буфер обмена (cliprdr) не поднимается.
#cgo pkg-config: freerdp3 freerdp-client3 winpr3 Qt6Widgets
#cgo CXXFLAGS: -std=c++17
#cgo LDFLAGS: -lstdc++
#include <stdlib.h>
#include "bridge.h"

// Колбэки, экспортируемые в Go (объявления ДО //export; сигнатуры СОВПАДАЮТ).
extern void rdpGoFrame(int id, int w, int h);
extern void rdpGoDone(int id, int ok, char *reason);
// M39-fullscreen: Esc в полноэкранном режиме вкладки → локальный выход.
extern void rdpGoFullscreen(int id);
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

//export rdpGoFrame
func rdpGoFrame(id C.int, w C.int, h C.int) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Frame != nil {
		cb.Frame(int(w), int(h))
	}
}

//export rdpGoDone
func rdpGoDone(id C.int, ok C.int, reason *C.char) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Done != nil {
		cb.Done(ok != 0, C.GoString(reason))
	}
}

//export rdpGoFullscreen
func rdpGoFullscreen(id C.int) {
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
	id := int(C.rdp_create())
	if id <= 0 {
		return 0, errNoCanvas
	}
	callbacks[id] = cb
	return id, nil
}

// Native — нативный QWidget* (для вставки во вкладку через miqt).
func Native(id int) unsafe.Pointer {
	return C.rdp_native(C.int(id))
}

// Drive — перенаправляемый встроенным клиентом локальный каталог (M39,
// канал rdpdr): сервер увидит его как \\tsclient\<Share>. Read-only libfreerdp3
// не поддерживает — права у сервера такие же, как у пользователя.
type Drive struct {
	Share string
	Path  string
}

// Start запускает клиент на C++-нити (неблокирующий; НЕ Qt-нить).
// sound — аргумент канала rdpsnd ("sys:alsa", "sys:pulse", …); "" — звук не
// включаем (дефолтная подсистема FreeRDP sys:fake, т.е. без звука).
func Start(id int, host string, port int, user, domain, pass, title string,
	w, h, bpp int, clipboard bool, flags uint32, tlsSecLevel int, kbd string,
	sound string, drives []Drive) {
	ch := C.CString(host)
	cu := C.CString(user)
	cd := C.CString(domain)
	cp := C.CString(pass)
	ct := C.CString(title)
	ck := C.CString(kbd)
	cs := C.CString(sound)
	cb := 0
	if clipboard {
		cb = 1
	}
	// M39: массивы шэров/путей для rdp_start (const char *const *). Строки —
	// C-копии; вектор дисков C++ копирует себе до возврата.
	shares := make([]*C.char, len(drives))
	paths := make([]*C.char, len(drives))
	for i, d := range drives {
		shares[i] = C.CString(d.Share)
		paths[i] = C.CString(d.Path)
	}
	var sharesPtr, pathsPtr **C.char
	if len(drives) > 0 {
		sharesPtr = &shares[0]
		pathsPtr = &paths[0]
	}
	C.rdp_start(C.int(id), ch, C.int(port), cu, cd, cp, ct,
		C.int(w), C.int(h), C.int(bpp), C.int(cb),
		C.uint(flags), C.int(tlsSecLevel), ck, cs,
		sharesPtr, pathsPtr, C.int(len(drives)))
	C.free(unsafe.Pointer(ch))
	C.free(unsafe.Pointer(cu))
	C.free(unsafe.Pointer(cd))
	// Пароль — секрет: затираем C-копию перед free (C++ уже скопировал строку
	// в std::string m_pass и затирает её в деструкторе; см. отчёт).
	zeroBytes(unsafe.Slice((*byte)(unsafe.Pointer(cp)), len(pass)))
	C.free(unsafe.Pointer(cp))
	C.free(unsafe.Pointer(ct))
	C.free(unsafe.Pointer(ck))
	C.free(unsafe.Pointer(cs))
	for i := range drives {
		C.free(unsafe.Pointer(shares[i]))
		C.free(unsafe.Pointer(paths[i]))
	}
}

// zeroBytes — стереть байты в C-памяти перед освобождением.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Stop просит worker выйти (без Qt; безопасно из любой нити).
func Stop(id int) { C.rdp_stop(C.int(id)) }

// SetFullscreen помечает канвас как живущий в полноэкранном режиме вкладки:
// пока on, одиночный Esc вызывает Callback.Fullscreen вместо отправки в сессию.
// Только Qt-нить.
func SetFullscreen(id int, on bool) {
	v := C.int(0)
	if on {
		v = 1
	}
	C.rdp_set_fullscreen(C.int(id), v)
}

// Destroy убирает канвас (Qt-нить): stop + join + deleteLater.
func Destroy(id int) {
	mu.Lock()
	delete(callbacks, id)
	mu.Unlock()
	C.rdp_destroy(C.int(id))
}

var errNoCanvas = &canvasError{"rdpview: создать канвас не удалось"}

type canvasError struct{ msg string }

func (e *canvasError) Error() string { return e.msg }
