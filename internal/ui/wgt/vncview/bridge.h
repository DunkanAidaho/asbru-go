#ifndef ASBRU_VNC_VIEW_BRIDGE_H
#define ASBRU_VNC_VIEW_BRIDGE_H

#include <stdint.h>

// Обесцвечивание последнего кадра при обрыве сессии (последний кадр — ч/б).
#include "../grayframe.h"

// Мост к встроенному VNC-клиенту (libvncclient, LibVNCServer 0.9.15+).
// Канвас — QWidget внутри вкладки Ásbrú; фреймбуфер [BGRA] рисуется в QImage
// (Format_RGB32, старший байт игнорируется). Логика клиента живёт в C++-нити
// (std::thread), по схеме 0.9.15: ConnectToRFBServer -> InitialiseRFBConnection ->
// цикл WaitForMessage+HandleRFBServerMessage. Чтение/запись одного сокета — только
// на этой нити; ввод (Qt-события канваса) ставится в очередь и дренится циклом.
// Ввод обрабатывается нативно в канвасе (Qt-надстроечные события), Go его не
// трогает — только старт/стоп/канвас/колбэки готовности кадра и завершения.

// Go-экспорт колбэки (определяются в Go через //export; сигнатуры СОВПАДАЮТ с
// сгенерированными: char*, не const char*). В C++ — внутри extern "C" (иначе
// C++-мангинг не совпадёт с C-символом Go-экспорта).
#ifdef __cplusplus
extern "C" {
#endif
extern void vncGoFrame(int id, int w, int h);
extern void vncGoDone(int id, int ok, char *reason);
// M39-fullscreen: в полноэкранном режиме вкладки Esc — локальный выход, а не
// клавиша серверу (см. vnc_set_fullscreen).
extern void vncGoFullscreen(int id);
#ifdef __cplusplus
}
#endif

#ifdef __cplusplus
extern "C" {
#endif

// Создать канвас (QT-нить). Возвращает id (0 — ошибка).
int  vnc_create(void);
// Нативный QWidget* (для вставки во вкладку через miqt).
void *vnc_native(int id);
// Запустить клиент. Неблокирующий: сам клиент живёт на отдельной НЕ-Qt-нити.
//
// ВАЖНО: вызывать ТОЛЬКО из Qt-нити — здесь подписывается QClipboard::dataChanged
// (M39, буфер обмена), а QClipboard живёт в Qt-нити (у rdp_start причина та же:
// чтение состояния QWidget).
// security — VeNCrypt-режим: "auto"/"vencrypt"/"none" (САНИТИЗИРУЕТСЯ внутри C++).
void vnc_start(int id, const char *host, int port, const char *user,
               const char *pass, const char *title, const char *security);
// Остановить и убрать канвас (Qt-нить).
void vnc_stop(int id);
void vnc_destroy(int id);

// M39-fullscreen: пометить канвас как живущий в полноэкранном режиме вкладки
// (on != 0). Пока режим включён, одиночный Esc вызывает vncGoFullscreen и НЕ
// уходит в сессию. Только Qt-нить.
void vnc_set_fullscreen(int id, int on);

#ifdef __cplusplus
}
#endif

#endif
