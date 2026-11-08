#ifndef ASBRU_RDP_VIEW_BRIDGE_H
#define ASBRU_RDP_VIEW_BRIDGE_H

#include <stdint.h>

// Обесцвечивание последнего кадра при обрыве сессии (последний кадр — ч/б).
#include "../grayframe.h"

// Мост к встроенному RDP-клиенту (libfreerdp3 3.30). Канвас — QWidget внутри
// вкладки Ásbrú; фреймбуфер [BGRX32] из gdi->primary_buffer рисуется в QImage
// (Format_RGB32). Логика клиента живёт на C++-нити (std::thread) по схеме
// Pattern A (manual freerdp_new): freerdp_new -> context_new -> настройки ->
// gdi_init(BGRX32) -> hook update->EndPaint/DesktopResize -> freerdp_connect ->
// цикл freerdp_get_event_handles+freerdp_check_event_handles -> freerdp_disconnect.
// Ввод: Qt-события канваса -> очередь -> worker (freerdp_input_send_*).

// Go-экспорт колбэки (определяются в Go через //export; char*, не const char*).
// В C++ — внутри extern "C" (иначе C++-мангинг не совпадёт с C-символом Go-экспорта).
#ifdef __cplusplus
extern "C" {
#endif
extern void rdpGoFrame(int id, int w, int h);
extern void rdpGoDone(int id, int ok, char *reason);
// M39-fullscreen: в полноэкранном режиме вкладки Esc — локальный выход, а не
// клавиша серверу. Go выставляет режим через rdp_set_fullscreen и получает
// этот колбэк (Qt-нить) вместо отправки Esc в сессию.
extern void rdpGoFullscreen(int id);
#ifdef __cplusplus
}
#endif

#ifdef __cplusplus
extern "C" {
#endif

// Создать канвас (QT-нить). Возвращает id (0 — ошибка).
int  rdp_create(void);
// Флаги указателя для события колеса: accum — накопленное вращение в единицах
// WHEEL_DELTA (120 = щелчок), dy — очередная порция. Возвращает flags для
// freerdp_input_send_mouse_event (младшие 9 бит — вращение, см. WheelRotationMask)
// либо 0, если целого щелчка ещё не набралось. Чистая функция — тестируется из Go.
uint16_t rdp_wheel_step(int32_t *accum, int32_t dy);

// Горизонтальное колесо (M40-остаток): тот же 9-битный формат вращения, но с
// флагом PTR_FLAGS_HWHEEL. Канон FreeRDP client/X11/xf_client.c:1091 (Button6/
// Button7): вправо — положительное вращение, влево — отрицательное
// (дополнение до двух). Чистая функция — тестируется из Go.
uint16_t rdp_hwheel_step(int32_t *accum, int32_t dx);

// Целевой размер десктопа для DisplayControl (динамическое разрешение,
// галка «/dynamic-resolution»): физические пиксели виджета (логический размер ×
// dpr, округление 0.5 вверх), кламп в протокольные пределы провода
// DISPLAY_CONTROL_MIN/MAX_MONITOR_WIDTH/HEIGHT = 200..8192 (freerdp/channels/disp.h).
// Возвращает 1 и пишет outW/outH, если размер изменился относительно текущего
// десктопа curW/curH (layout надо слать), 0 — если слать нечего (виджет ещё не
// разложен или размер уже применён сервером). Чистая функция — тестируется из Go.
int rdp_disp_resize_target(int widgetW, int widgetH, double dpr,
                           int curW, int curH, int *outW, int *outH);

// --- M39: картинки в буфере обмена (CF_DIB/CF_DIBV5) ---
// На проводе cliprdr формат CF_DIB/CF_DIBV5 — это ГОЛЫЙ DIB (BITMAPINFOHEADER
// или BITMAPV5HEADER + палитра + пиксели), без 14-байтного BITMAPFILEHEADER
// («BM»). QImageWriter("BMP") пишет полный BMP-файл, поэтому локальный→сервер
// заголовок срезается, а сервер→локальный — синтезируется.
//
// Срезать BITMAPFILEHEADER: возвращает 1 и *outDibLen = len - 14, если файл
// начинается с «BM» и длиннее заголовка; иначе 0 (усечённый/чужой формат).
int rdp_dib_strip_bmp_header(const uint8_t *bmp, uint32_t len, uint32_t *outDibLen);

// Смещение пиксельных данных внутри DIB (от начала DIB) по его заголовку:
//   biSize + палитра + (12 для BI_BITFIELDS при biSize == 40) +
//   bV5ProfileSize (только BITMAPV5HEADER).
// Палитра: при biBitCount <= 8 — (biClrUsed ? biClrUsed : 1<<biBitCount) * 4;
// при большем biBitCount — biClrUsed * 4 (обычно 0). У V4/V5 маски BI_BITFIELDS
// лежат ВНУТРИ заголовка, поэтому +12 к ним не применяется.
// Возвращает 0, если DIB усечён/невалиден (у валидного смещение всегда >= 40).
uint32_t rdp_dib_pixel_offset(const uint8_t *dib, uint32_t len);

// Собрать BMP-файл из голого DIB: пишет 14-байтный BITMAPFILEHEADER («BM»,
// bfSize, bfOffBits = 14 + rdp_dib_pixel_offset, резервы нули) и копирует DIB.
// Возвращает записанную длину (14 + len) либо 0 при невалидном/усечённом DIB
// или если outCap < 14 + len.
uint32_t rdp_bmp_from_dib(const uint8_t *dib, uint32_t len,
                          uint8_t *out, uint32_t outCap);

// --- M39: CF_HTML и CF_TEXT ---
// Предикат «текст — чистый ASCII». CF_TEXT на проводе — ANSI-строка системной
// кодовой страницы Windows; UTF-8 с не-ASCII байтами в ней превращается в
// мозаику. Поэтому CF_TEXT рекламируется только при len == 0 или всех байтах
// < 0x80. Чистая функция — тестируется из Go.
int rdp_text_is_ascii(const uint8_t *s, uint32_t len);

// Собрать CF_HTML payload из UTF-8 HTML: заголовок
//   Version:0.9 / StartHTML / EndHTML / StartFragment / EndFragment (по 10 цифр)
// и сам HTML. Все смещения — БАЙТОВЫЕ от начала payload; заголовок всегда
// 105 байт, поэтому EndHTML = 105 + len. Если в HTML есть маркеры
// <!--StartFragment-->/<!--EndFragment-->, границы фрагмента берутся между ними,
// иначе фрагмент — весь HTML. Возвращает длину payload либо 0, если outCap мал.
uint32_t rdp_cf_html_build(const uint8_t *html, uint32_t len,
                           uint8_t *out, uint32_t outCap);

// Разобрать CF_HTML payload: пишет БАЙТОВЫЕ границы фрагмента [outStart,outEnd).
// Сначала используются StartFragment/EndFragment из заголовка (с проверкой
// диапазона), при их отсутствии/битости — маркеры-комментарии, при их
// отсутствии — весь payload (голый HTML). Если же заголовок явно есть и испорчен
// (начинается с «Version:»), разбор отказывает, чтобы в буфер не попал сам
// заголовок. Возвращает 1 при успехе, 0 — если фрагмент не найден.
int rdp_cf_html_parse(const uint8_t *payload, uint32_t len,
                      uint32_t *outStart, uint32_t *outEnd);

// --- M39: файлы в буфере обмена (FileGroupDescriptorW, локальный→сервер) ---
// text/uri-list для cliprdr_file_context_update_client_data: каждый локальный
// путь (UTF-8) превращается в "file://" + percent-encoded путь (RFC 3986:
// unreserved и '/' сохраняются, остальные БАЙТЫ кодируются %XX), элементы
// разделяются CRLF. Пустые пути, а также корневые FIFO и unix-сокеты
// пропускаются (ag-q2t: winpr_fopen такой FIFO блокируется навсегда).
// Возвращает длину результата; при out == NULL — ТРЕБУЕМУЮ длину (для
// двухпроходной аллокации в Go-обёртке), 0 — если путей нет или буфер мал.
uint32_t rdp_uri_list_build(const char *const *paths, uint32_t count,
                            uint8_t *out, uint32_t outCap);

// FileGroupDescriptorW на проводе — упакованный список файлов MS-RDPECLIP 2.2.5.2.3
// (CLIPRDR_FILELIST): UINT32 cItems + cItems × FILEDESCRIPTORW (592 байта,
// cFileName — UTF-16LE, завершённый NUL). Обход повторяет
// cliprdr_file_context_update_client_data: pre-order DFS, каталоги рекурсивно,
// дети — в порядке readdir, FIFO пропускаются; корневые FIFO и unix-сокеты
// отбрасываются целиком (ag-q2t: иначе сервер запросит содержимое, а
// winpr_fopen(FIFO) заблокируется навсегда); несуществующий путь верхнего
// уровня всё равно попадает в список (как append_entry в FreeRDP). Порядок =
// listIndex в FILECONTENTS_REQUEST.
//
// Сборка — ОДИН обход (раньше было два: подсчёт длины и запись) с жёсткими
// пределами (ag-32m): копирование каталога с огромным числом файлов не должно
// подвешивать UI/воркер и раздувать память. Возвращает malloc-буфер (владелец —
// вызывающий, освобождать rdp_free) либо NULL; *outLen — длина, *outReason —
// причина отказа (kRdpClipList*).
//
// *_alloc — производственные пределы (65536 записей, 32 МиБ, 5 с);
// *_alloc_limits — явные пределы, 0 = без предела (для тестов).
enum {
    kRdpClipListOk = 0,
    kRdpClipListTooMany = 1,
    kRdpClipListTooBig = 2,
    kRdpClipListTimeout = 3,
    kRdpClipListIoError = 4,
    kRdpClipListEmpty = 5,
    // Имя файла недопустимо в Windows (порт winpr ValidFileNameComponent,
    // ag-73o): такой список не строим — FileGroupDescriptorW не объявляем.
    kRdpClipListInvalidName = 6,
};
uint8_t *rdp_cliprdr_file_list_alloc(const char *const *paths, uint32_t count,
                                     uint32_t *outLen, int *outReason);
uint8_t *rdp_cliprdr_file_list_alloc_limits(
        const char *const *paths, uint32_t count,
        uint32_t maxItems, uint32_t maxBytes, uint32_t maxMs,
        uint32_t *outLen, int *outReason);
void rdp_free(void *p);

// Нативный QWidget* (для вставки во вкладку через miqt).
void *rdp_native(int id);
// Битмаска настроек RDP. Значения ОБЯЗАНЫ совпадать с embed.Flag* в Go
// (контракт проверяется тестом TestRDPFlagsMatchBridgeHeader).
#define RDP_FLAG_CERT_IGNORE        (1u << 0)
#define RDP_FLAG_NO_NLA             (1u << 1)
#define RDP_FLAG_NO_TLS             (1u << 2)
#define RDP_FLAG_NO_RDP             (1u << 3)
#define RDP_FLAG_NO_AUTH            (1u << 4)
#define RDP_FLAG_NO_FAST_PATH       (1u << 5)
#define RDP_FLAG_RFX                (1u << 6)
#define RDP_FLAG_NSC                (1u << 7)
#define RDP_FLAG_DYNAMIC_RESOLUTION (1u << 8)
#define RDP_FLAG_FONT_SMOOTH        (1u << 9)
#define RDP_FLAG_ADMIN              (1u << 10)

// Значение FreeRDP_IgnoreCertificate по маске RDP_FLAG_*: TRUE только при
// RDP_FLAG_CERT_IGNORE (семантика xfreerdp — по умолчанию сертификат
// проверяется). Чистая функция — контракт проверяется из Go.
int rdp_ignore_certificate(unsigned int flags);

// Запустить клиент. Неблокирующий: сам клиент живёт на отдельной НЕ-Qt-нити.
//
// ВАЖНО (ревью MINOR-4): вызывать ТОЛЬКО из Qt-нити. Причина — не таймер:
// `QTimer::singleShot` с получателем в Qt-нити из чужой нити МАРШАЛИТСЯ и
// сработает (проверено стендом). Причина в том, что beginWhenSized читает
// состояние QWidget — width()/height()/devicePixelRatioF() — а оно доступно
// только Qt-нити: из чужой нити это гонка на QWidget, а не таймаут. Все
// текущие пути (меню/IPC/API через mainthreadW) идут из Qt-нити.
// w/h/bpp <=0 — дефолты (размер виджета / 1024x768, 16 бит).
// flags — RDP_FLAG_* (галки редактора), tlsSecLevel — 0 (не задано) или 1..5.
// M39: sound — аргумент канала rdpsnd ("sys:alsa", "sys:pulse", …); NULL/"" —
// звук не включаем (остаётся дефолтная подсистема FreeRDP sys:fake). Реальную
// подсистему ставим ТОЛЬКО при явном токене в options (легаси-паритет); если
// бэкенд в этой сборке недоступен, остаётся fake, connect не падает.
// M39: driveShares/drivePaths/driveCount — перенаправляемые каталоги (канал
// rdpdr): элемент i — имя шэра и локальный путь. Пути, которых нет/это не
// каталог, пропускаются; отсутствие аддина rdpdr НЕ валит connect (диски просто
// не объявляются). driveCount == 0 — без редиректа (NULL/NULL/0).
void rdp_start(int id, const char *host, int port, const char *user,
               const char *domain, const char *pass, const char *title,
               int w, int h, int bpp, int clipboard,
               unsigned int flags, int tlsSecLevel, const char *kbd,
               const char *sound,
               const char *const *driveShares, const char *const *drivePaths,
               int driveCount);
// Остановить и убрать канвас (Qt-нить).
void rdp_stop(int id);
void rdp_destroy(int id);

// M39-fullscreen: пометить канвас как живущий в полноэкранном режиме вкладки
// (on != 0). Пока режим включён, одиночный Esc перехватывается канвасом
// (rdpGoFullscreen) и НЕ уходит в сессию; выключение режима — Esc снова идёт
// серверу. Только Qt-нить.
void rdp_set_fullscreen(int id, int on);

#ifdef __cplusplus
}
#endif

#endif
