// Package wgt — cgo-мост к QTermWidget (телетайп-режим, P0 из плана M8).
//
// Архитектура: сессия Ásbrú (Go) остаётся единственным владельцем логики;
// вывод сессии уходит в эмулятор записью в pty-slave виджета (qtw_feed),
// нажатия пользователя возвращаются сигналом sendData → колбэк (канал).
package wgt

/*
#cgo pkg-config: qtermwidget6
#cgo LDFLAGS: -lqtermwidget6
#include <stdlib.h>
#include "bridge.h"

// Колбэки, экспортируемые в Go (объявления ДО Go-генерации //export;
// сигнатуры должны точно совпадать с сгенерированными: char*, не const char*).
extern void goOnData(int id, char *data, int n);
extern void goOnTitle(int id, char *title);
extern void goOnClosed(int id);
extern void goOnResize(int id, int cols, int rows);
extern void goOnContextMenu(int id, int x, int y);

// Проводник-колбэки: обязаны быть неблокирующими (вызываются из Qt-нити).
static void s_on_data(int id, const char *data, int n) { goOnData(id, (char *)data, n); }
static void s_on_title(int id, const char *title) { goOnTitle(id, (char *)title); }
static void s_on_closed(int id) { goOnClosed(id); }
static void s_on_resize(int id, int cols, int rows) { goOnResize(id, cols, rows); }
static void s_on_menu(int id, int x, int y) { goOnContextMenu(id, x, y); }
static void s_set_menu_cb(int id) { qtw_set_menu_cb(id, s_on_menu); }

static int  s_create(int teletype) { return qtw_create(teletype, s_on_data, s_on_title, s_on_closed, s_on_resize); }
static void s_feed(int id, const char *data, int n) { qtw_feed(id, data, n); }
static void s_attach(int id, void *p) { qtw_attach(id, p); }
static void *s_native(int id) { return qtw_native(id); }
static void *s_menu_new() { return qtw_menu_new(); }
static void *s_menu_item(void *m, const char *t) { return qtw_menu_item(m, t); }
static void *s_menu_submenu(void *m, const char *t) { return qtw_menu_submenu(m, t); }
static void *s_menu_exec(void *m, int x, int y) { return qtw_menu_exec(m, x, y); }
static void s_menu_free(void *m) { qtw_menu_free(m); }
static void s_show(int id) { qtw_show(id); }
static void s_destroy(int id) { qtw_destroy(id); }
static void s_set_font(int id, const char *f) { qtw_set_font(id, f); }
static void s_set_colorscheme(int id, const char *n) { qtw_set_colorscheme(id, n); }
static void s_set_cursor_shape(int id, int s) { qtw_set_cursor_shape(id, s); }
static void s_set_history_size(int id, int l) { qtw_set_history_size(id, l); }
static void s_set_scrollbar(int id, int p) { qtw_set_scrollbar(id, p); }
static void s_set_opacity(int id, double l) { qtw_set_opacity(id, l); }
static void s_set_word_chars(int id, const char *c) { qtw_set_word_chars(id, c); }
static void s_set_auto_close(int id, int v) { qtw_set_auto_close(id, v); }
static void s_copy(int id) { qtw_copy(id); }
static void s_paste(int id) { qtw_paste(id); }
static void s_find(int id) { qtw_find(id); }
*/
import "C"

import (
	"sync"
	"unsafe"
)

// TermConfig — параметры Look&Feel для Configure (M27).
type TermConfig struct {
	Font        string  // "Monospace 9"
	ColorScheme string  // имя или путь .colorscheme
	CursorShape int     // 0=block,1=underline,2=ibeam (QTermWidget enum)
	HistorySize int     // строк истории (0=нет, <0=∞)
	Scrollbar   int     // 0=no,1=left,2=right
	Opacity     float64 // 0..1
	WordChars   string
	AutoClose   bool
}

// Configure применяет Look&Feel к виджету (только Qt-нить; M27).
func Configure(id int, tc TermConfig) {
	// Шрифт — ВСЕГДА (пустая/незаданная → системный моно-шрифт в qtw_set_font;
	// иначе QTermWidget ругается «variable-width font»). Б (user-тест).
	f := C.CString(tc.Font)
	C.s_set_font(C.int(id), f)
	C.free(unsafe.Pointer(f))
	if tc.ColorScheme != "" {
		n := C.CString(tc.ColorScheme)
		C.s_set_colorscheme(C.int(id), n)
		C.free(unsafe.Pointer(n))
	}
	C.s_set_cursor_shape(C.int(id), C.int(tc.CursorShape))
	C.s_set_history_size(C.int(id), C.int(tc.HistorySize))
	C.s_set_scrollbar(C.int(id), C.int(tc.Scrollbar))
	C.s_set_opacity(C.int(id), C.double(tc.Opacity))
	if tc.WordChars != "" {
		w := C.CString(tc.WordChars)
		C.s_set_word_chars(C.int(id), w)
		C.free(unsafe.Pointer(w))
	}
	if tc.AutoClose {
		C.s_set_auto_close(C.int(id), 1)
	}
}

// Copy — выделение терминала → буфер обмена (M28, Qt-нить).
func Copy(id int) { C.s_copy(C.int(id)) }

// Paste — буфер обмена → в терминал (M28, Qt-нить).
func Paste(id int) { C.s_paste(C.int(id)) }

// Find — показать поисковую панель QTermWidget (M36, Qt-нить).
func Find(id int) { C.s_find(C.int(id)) }

// SetContextMenuCB — регистрирует обработчик правого клика (M28, Qt-нить).
func SetContextMenuCB(id int, fn func(x, y int)) {
	mu.Lock()
	menuCBs[id] = fn
	mu.Unlock()
	C.s_set_menu_cb(C.int(id))
}

// Callback — данные, пришедшие из виджета (нажатия пользователя).
type Callback struct {
	Data   func(b []byte) // вызов из Qt-нити, неблокирующий
	Title  func(string)
	Closed func()
	Resize func(cols, rows int) // изменение терм-размера (M11)
}

var (
	mu        sync.Mutex
	callbacks = map[int]*Callback{}
	menuCBs   = map[int]func(x, y int){} // M28: колбэк правого клика
)

//export goOnData
func goOnData(id C.int, data *C.char, n C.int) {
	buf := C.GoBytes(unsafe.Pointer(data), n)
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Data != nil {
		cb.Data(buf)
	}
}

//export goOnTitle
func goOnTitle(id C.int, title *C.char) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Title != nil {
		cb.Title(C.GoString(title))
	}
}

//export goOnClosed
func goOnClosed(id C.int) {
	mu.Lock()
	cb := callbacks[int(id)]
	mu.Unlock()
	if cb != nil && cb.Closed != nil {
		cb.Closed()
	}
}

// Create создаёт телетайп-виджет и регистрирует колбэки.
// Вызывать ТОЛЬКО из Qt-нити.
func Create(cb *Callback) (int, error) {
	mu.Lock()
	defer mu.Unlock()
	id := int(C.s_create(1))
	if id <= 0 {
		return 0, errNoWidget
	}
	callbacks[id] = cb
	return id, nil
}

// Feed — вывод сессии в эмулятор (из любой нити; write в slave).
func Feed(id int, data []byte) {
	if len(data) == 0 {
		return
	}
	C.s_feed(C.int(id), (*C.char)(unsafe.Pointer(&data[0])), C.int(len(data)))
}

// Attach — встроить под Qt-контейнер (из Qt-нити).
func Attach(id int, parentNative unsafe.Pointer) {
	C.s_attach(C.int(id), parentNative)
}

// Native — нативный указатель виджета (для QTabWidget.AddTab через miqt-wrap).
func Native(id int) unsafe.Pointer {
	return C.s_native(C.int(id))
}

// Show — показать терминал отдельным окном (из Qt-нити; для e2e-этапа).
func Show(id int) {
	C.s_show(C.int(id))
}

// Destroy убирает виджет (из Qt-нити).
func Destroy(id int) {
	mu.Lock()
	delete(callbacks, id)
	delete(menuCBs, id)
	mu.Unlock()
	C.s_destroy(C.int(id))
}

var errNoWidget = &shimError{"qtermwidget: создать виджет не удалось"}

type shimError struct{ msg string }

func (e *shimError) Error() string { return e.msg }

// формат: //export после объявлений — сигнатура goOnResize(int id, int cols, int rows)
//
//export goOnContextMenu
func goOnContextMenu(id C.int, x C.int, y C.int) {
	mu.Lock()
	fn := menuCBs[int(id)]
	mu.Unlock()
	if fn != nil {
		fn(int(x), int(y))
	}
}

//export goOnResize
func goOnResize(id C.int, cols C.int, rows C.int) {
	mu.Lock()
	cb, ok := callbacks[int(id)]
	mu.Unlock()
	if !ok || cb.Resize == nil {
		return
	}
	cb.Resize(int(cols), int(rows))
}

// Menu — лёгкое контекстное меню (официальный miqt не экспортирует
// QMenu::addAction, поэтому добавка сделана в шиме).
type Menu struct {
	raw unsafe.Pointer
}

// NewMenu создаёт пустое QMenu (native-владение до Close).
func NewMenu() *Menu {
	m := &Menu{raw: C.s_menu_new()}
	return m
}

// Item — пункт меню; ptr — для сравнения с результатом Check.
func (m *Menu) Item(text string) unsafe.Pointer {
	cs := C.CString(text)
	defer C.free(unsafe.Pointer(cs)) // L1-ревью: утечка на каждый клик
	return C.s_menu_item(m.raw, cs)
}

// Submenu — вложенное подменю (М36); пункты — через Item на возвращённом Menu.
func (m *Menu) Submenu(title string) *Menu {
	cs := C.CString(title)
	defer C.free(unsafe.Pointer(cs))
	return &Menu{raw: C.s_menu_submenu(m.raw, cs)}
}

// Check — модальный exec с позиции (Qt-нить); возвращает выбранный ptr.
func (m *Menu) Check(x, y int) unsafe.Pointer {
	return C.s_menu_exec(m.raw, C.int(x), C.int(y))
}

// Free — удаление меню (после Check).
func (m *Menu) Free() {
	C.s_menu_free(m.raw)
}
