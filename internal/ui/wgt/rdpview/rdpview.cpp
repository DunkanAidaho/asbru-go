// RdpCanvas — встроенный RDP-клиент (libfreerdp3 3.30) в Qt-канвасе вкладки.
// Потоки: Qt-нить создаёт канвас и получает ввод; std::thread (worker) владеет
// соединением (freerdp_connect + цикл event-handles). После gdi_init фреймбуфер
// [BGRX32] копируется в QImage (Format_RGB32) по hook'у update->EndPaint и
// пробрасывается repaint'ом на Qt-нить. Ввод от Qt-событий — в очередь, дренится
// worker'ом (freerdp_input_send_* только на RDP-нити).
#include <QWidget>
#include <QImage>
#include <QTimer>
#include <QPainter>
#include <QPaintEvent>
#include <QMouseEvent>
#include <QKeyEvent>
#include <QEvent>
#include <QWheelEvent>
#include <QResizeEvent>
#include <QApplication>
#include <QMetaObject>
#include <QString>
#include <QColor>
#include <QFont>
#include <QMutex>
#include <QMutexLocker>
#include <QGuiApplication>
#include <QClipboard>
#include <QBuffer>
#include <QImageWriter>
#include <QMimeData>
#include <QTextDocument>
#include <QHashFunctions>
#include <QUrl>
#include <atomic>
#include <algorithm>
#include <thread>
#include <mutex>
#include <chrono>
#include <deque>
#include <string>
#include <cstring>
#include <cstdlib>
#include <cstdint>
#include <cstdio>
#include <cstdarg>
#include <map>
#include <set>
#include <vector>
#include <utility>
#include <sys/stat.h> // M39: файлы в буфере — stat/lstat для FILEDESCRIPTORW
#include <dirent.h>   // M39: обход каталогов в порядке readdir (как FreeRDP)

#include <freerdp/freerdp.h>
#include <freerdp/locale/keyboard.h>
#include <freerdp/gdi/gdi.h>
#include <freerdp/input.h>
#include <freerdp/settings.h>
#include <freerdp/settings_keys.h>
#include <freerdp/settings_types.h> // M40: CONNECTION_TYPE_AUTODETECT
#include <freerdp/scancode.h>
#include <freerdp/codec/color.h>
#include <winpr/synch.h>
#include <winpr/handle.h>
#include <winpr/wlog.h>
#include <winpr/user.h> // CF_UNICODETEXT
// M39: каналы виртуальных устройств (буфер обмена = cliprdr).
// M41: GFX-конвейер (rdpgfx) — канал DVC, подписка на ChannelConnected и
// инициализация обработчиков EGFX в gdi.
#include <freerdp/gdi/gfx.h>         // gdi_graphics_pipeline_init (публичный API)
#include <freerdp/client/rdpgfx.h>   // RdpgfxClientContext
#include <freerdp/channels/rdpgfx.h> // RDPGFX_DVC_CHANNEL_NAME
#include <winpr/collections.h>       // PubSub_SubscribeChannelConnected
#include <freerdp/addin.h>             // freerdp_register_addin_provider
#include <freerdp/client/cmdline.h>    // freerdp_client_load_addins
#include <freerdp/client/channels.h>   // freerdp_channels_load_static_addin_entry
#include <freerdp/channels/channels.h> // freerdp_channels_get_static_channel_interface
#include <freerdp/channels/cliprdr.h>  // CLIPRDR_*, CLIPRDR_SVC_CHANNEL_NAME, CB_*
#include <freerdp/channels/rdpdr.h>    // M39: RDPDR_SVC_CHANNEL_NAME (drive), RDPDR_DTYP_*
#include <freerdp/channels/rdpsnd.h>   // M39: RDPSND_CHANNEL_NAME (звук)
#include <freerdp/client/cliprdr.h>    // CliprdrClientContext
// M39: файловый буфер — публичный API FreeRDP 3.32.2 (client/common/
// client_cliprdr_file.c). init() забирает cliprdr->custom себе и ставит
// ServerLock/Unlock/FileContentsRequest(+Response только с FUSE), поэтому наши
// колбэки получают RdpCanvas через c->rdpcontext, а не через custom.
#include <freerdp/client/client_cliprdr_file.h>
// Динамическое разрешение (ресайз окна → смена разрешения сервером): канал disp
// + клиентский контекст DispClientContext (SendMonitorLayout/DisplayControlCaps).
#include <freerdp/channels/disp.h>
#include <freerdp/client/disp.h>

#include "bridge.h"

// Глобальные реестры и прототипы — НЕ в anonymous namespace: используются и
// членами класса (в anon namespace), и extern "C" функциями (глобально).
class RdpCanvas;

std::mutex g_mu;
std::map<int, RdpCanvas*> g_views;
std::map<rdpContext*, RdpCanvas*> g_byCtx;
// Свой диапазон id (см. widget.cpp): иначе RDP-канвас и первая вкладка-терминал
// получают один id. static — символ не должен иметь внешней линковки.
static int g_nextId = 1000000;

// Верхняя граница фреймбуфера (байт) — защита от DoS/огромного копирования QImage,
// если сервер пришлёт гигантские gdi->width/height (M33-г security). 8192x8192x4.
static const uint64_t kMaxFrameBytes = 8192ULL * 8192ULL * 4ULL;
// Предел стороны десктопа (пиксели) — тот же предел, но по стороне: размер
// запрашиваемого десктопа клампится им, чтобы клиент не попросил кадр, который
// setFrame обязан отбросить (иначе вкладка висит в «подключение…»).
static const uint64_t kMaxSidePx = 8192ULL;

// M39: зарегистрированный формат «HTML Format» (CF_HTML). Имя обязательно —
// клиент объявляет CB_USE_LONG_FORMAT_NAMES; id лежит в диапазоне 0xC000..0xFFFF,
// отведённом MS-RDPECLIP под зарегистрированные форматы (xfreerdp делает так же
// через ClipboardGetFormatId). Сервер может вернуть СВОЙ id — на приёме формат
// опознаётся по имени, а на запрос данных дополнительно принимается любой
// неизвестный id при наличии HTML в кэше.
static const char kClipHtmlFormatName[] = "HTML Format";
static const UINT32 kClipHtmlFormatId = 0xC000;
// M39: файловый формат. На проводе список файлов — это ЗАРЕГИСТРИРОВАННЫЙ
// формат FileGroupDescriptorW с payload CLIPRDR_FILELIST (MS-RDPECLIP 2.2.5.2.3).
// CF_HDROP (15) — локальная Win32-форма (DROPFILES), на провод она не идёт:
// так делают эталонные клиенты (client/Windows/wf_cliprdr.c, client/X11/
// xf_cliprdr.c — formatName "FileGroupDescriptorW"), и IronRDP. id — из
// зарегистрированного диапазона 0xC000..0xFFFF.
static const char kClipFileListFormatName[] = "FileGroupDescriptorW";
static const UINT32 kClipFileListFormatId = 0xC001;

// --- M39: буфер обмена через канал cliprdr ---
// Диагностика цепочки буфера. Молчит, пока не задан ASBRU_RDPCLIP_LOG=<файл> —
// в проде никакого шума. Нужна потому, что при неудаче код раньше молчал, и
// отличить «канал не согласован сервером» от «сервер не прислал MonitorReady»
// или «не доехала галка» по внешнему поведению невозможно.
static void clipDiag(const char* fmt, ...) {
    const char* path = getenv("ASBRU_RDPCLIP_LOG");
    if (!path || !*path) return;
    FILE* f = fopen(path, "a");
    if (!f) return;
    va_list ap;
    va_start(ap, fmt);
    vfprintf(f, fmt, ap);
    va_end(ap);
    fputc('\n', f);
    fclose(f);
}

// Провайдер СТАТИЧЕСКИХ аддинов регистрируется штатно ВНУТРИ
// freerdp_client_context_new(); наш путь — freerdp_new(), поэтому без этой
// регистрации freerdp_load_channel_addin_entry уходит в ДИНАМИЧЕСКУЮ загрузку
// libcliprdr-client.so, которого в сборке нет (проба: до регистрации
// lookup = NULL, после — найден, плюс WARN «Failed to load channel cliprdr»).
// Регистрация глобальна и идемпотентна — делаем один раз на процесс.
static void ensureAddinProvider() {
    static std::once_flag once;
    std::call_once(once, [] {
        freerdp_register_addin_provider(freerdp_channels_load_static_addin_entry, 0);
    });
}

// M39 (drive): есть ли СТАТИЧЕСКИЙ аддин канала. Это тот же поиск, который
// делает freerdp_client_load_static_channel_addin внутри load_addins, но без
// загрузки. Нужен, чтобы отсутствие аддина rdpdr (drive) не убило connect:
// load_addins на неразрешённом аддине возвращает FALSE, и freerdp_connect
// падает ЦЕЛИКОМ, а не только диск (см. cbLoadChannels).
static bool staticAddinAvailable(const char* name) {
    if (freerdp_load_channel_addin_entry(
            name, nullptr, nullptr,
            FREERDP_ADDIN_CHANNEL_STATIC | FREERDP_ADDIN_CHANNEL_ENTRYEX))
        return true;
    return freerdp_load_channel_addin_entry(name, nullptr, nullptr,
                                           FREERDP_ADDIN_CHANNEL_STATIC) != nullptr;
}

// M39 (sound): доступна ли ПОДСИСТЕМА канала rdpsnd в этой сборке. Аддин
// канала (rdpsnd) и аддины устройств (alsa/pulse/oss/sndio/fake) — разные
// записи реестра: load_addins находит канал, а устройство подгружается позже по
// имени подсистемы. Проверяем ровно тот же lookup, которым это делает сам
// rdpsnd (pszType = nullptr, флаги STATIC|ENTRYEX — проверено стендом 3.32.2:
// "alsa"/"pulse"/"oss"/"sndio"/"fake" находятся, "sys:alsa" и неверный type —
// нет). Нужен, чтобы заранее отбросить недоступный бэкенд и не рисковать
// connect (load_addins на неразрешимом аддине возвращает FALSE).
static bool rdpsndSubsystemAvailable(const char* channelArg) {
    if (!channelArg) return false;
    const char* sub = channelArg;
    if (strncmp(sub, "sys:", 4) == 0) sub += 4;
    if (!*sub) return false;
    return freerdp_load_channel_addin_entry(
               RDPSND_CHANNEL_NAME, sub, nullptr,
               FREERDP_ADDIN_CHANNEL_STATIC | FREERDP_ADDIN_CHANNEL_ENTRYEX) != nullptr;
}

// Аддины грузим ИМЕННО ОТСЮДА: freerdp_connect() пересоздаёт context->channels
// (utils_reload_channels → freerdp_channels_free/new), поэтому всё, загруженное
// ДО connect, теряется — причём адрес объекта может остаться тем же (malloc
// переиспользует память), так что проверка «указатель не изменился» лжёт.
// Колбэк зовётся и при reconnect — аддины перезагружаются сами.
//
// ВАЖНО (ревью MAJOR-1): freerdp_client_load_addins грузит ВСЕ статические
// каналы по их сеттингам, а не только cliprdr. Проверено стендом на 3.31.1:
// после вызова клиент объявляет серверу rdpdr, rdpsnd (с подсистемой sys:fake),
// cliprdr и drdynvc — то есть поднимаются ещё перенаправление устройств, звук и
// динамические каналы (disp/ainput), причём БЕЗУСЛОВНО, независимо от галки
// «Буфер обмена». Ущерба не найдено (RedirectDrives/RedirectHomeDrive = 0,
// DeviceCount = 0 — файлы наружу не уходят; звук по умолчанию — no-op бэкенд
// fake, реальная подсистема ставится только при явном /sound: в options; DISP
// сам не ресайзит), но это изменение протокола всех встроенных подключений,
// которое НЕ валидировано на живом сервере. Оставлено сознательно: так же
// работает официальный клиент. Второе следствие: если в дистрибутивной сборке
// какой-то аддин не разрешится, load_addins вернёт FALSE и УПАДЁТ ВЕСЬ connect,
// а не только буфер (поэтому rdpsnd-подсистема проверяется заранее — см. блок
// [sound] в run()). См. BACKLOG.md «M39 остаток».
static BOOL cbLoadChannels(freerdp* instance) {
    if (!instance || !instance->context) return FALSE;
    BOOL rc = freerdp_client_load_addins(instance->context->channels,
                                         instance->context->settings);
    clipDiag("load_addins rc=%d", (int)rc);
    return rc;
}

RdpCanvas* rdpViewFor(int id);
RdpCanvas* ctxCanvas(rdpContext* ctx);
void regCtx(rdpContext* ctx, RdpCanvas* c);
void unregCtx(rdpContext* ctx);


struct InputMsg {
    enum Kind { KeyUnicode, KeyScancode, Ptr } kind;
    // Инициализаторы по умолчанию: для Ptr-сообщений code/down/repeat не задаются,
    // и без них поля оставались бы неинициализированными (ревью M42 MINOR-4:
    // копирование indeterminate-значения — erroneous behavior в C++26).
    uint32_t code = 0;      // unicode или rdp_scancode
    int down = 0;
    // Автоповтор (Qt isAutoRepeat). В RDP «клавиша уже нажата» выражается битом
    // KBD_FLAGS_DOWN (0x4000) у TS_KEYBOARD_EVENT (сканкодное событие);
    // у TS_UNICODE_KEYBOARD_EVENT такого бита нет вовсе (см. drainInput).
    int repeat = 0;
    uint16_t flags = 0;     // PTR_FLAGS_*
    int x = 0, y = 0;
};

class RdpCanvas : public QWidget {
public:
    explicit RdpCanvas(int id) : QWidget(), m_id(id), m_stop(false), m_reported(false),
        m_dw(0), m_dh(0), m_savedEndPaint(nullptr), m_savedDesktopResize(nullptr),
        m_context(nullptr) {
        setFocusPolicy(Qt::StrongFocus);
    }
    ~RdpCanvas() override {
        requestStop();
        if (m_thread.joinable()) m_thread.join();
        // M33-г security: стереть пароль/пользователя/домен при уничтожении, чтобы
        // они не остались в freed-куче (менеджер учёток).
        m_pass.assign(m_pass.size(), '\0');
        m_user.assign(m_user.size(), '\0');
        m_domain.assign(m_domain.size(), '\0');
        // M39-ревью MINOR-2: кэш буфера обмена — тоже секрет (в приложении есть
        // штатное «Копировать пароль»), стираем как и креды.
        m_clipUtf16.assign(m_clipUtf16.size(), '\0');
        m_clipDib.assign(m_clipDib.size(), '\0');
        m_clipHtml.assign(m_clipHtml.size(), '\0');
        // M39: список файлов (пути/имена) — тоже чувствителен.
        m_clipFileList.assign(m_clipFileList.size(), '\0');
        for (auto& p : m_clipPathsPending) p.assign(p.size(), '\0');
        m_clipPathsPending.clear();
        // M39: файловый контекст освобождаем последним — после join воркера
        // (выше), поэтому колбэки cliprdr уже не могут его использовать.
        if (m_clipFile) {
            if (m_cliprdr) cliprdr_file_context_uninit(m_clipFile, m_cliprdr);
            cliprdr_file_context_free(m_clipFile);
            m_clipFile = nullptr;
        }
    }

    // Вид запрошенного у сервера формата: в FORMAT_DATA_RESPONSE нет ни id, ни
    // имени, поэтому помним не только id (m_clipReqFmt), но и вид. Для
    // зарегистрированного «HTML Format» числовой id назначает сервер и с нашей
    // константой может не совпасть.
    enum ClipReqKind {
        ClipReqNone = 0,
        ClipReqTextUni,
        ClipReqTextAnsi,
        ClipReqDib,
        ClipReqDibV5,
        ClipReqHtml,
    };

    void start(const std::string& host, int port, const std::string& user,
               const std::string& domain, const std::string& pass,
               const std::string& title, int w, int h, int bpp, int clipboard,
               unsigned int flags, int tlsLevel, const std::string& kbd,
               const std::string& sound,
               const std::vector<std::pair<std::string, std::string>>& drives) {
        // Ревью MINOR-6: защита от повторного вызова — второй std::thread в
        // joinable-поле это std::terminate, плюс дубль подписки на QClipboard.
        // Сегодня недостижимо (каждый канвас стартует один раз), но цена защиты — строка.
        if (m_thread.joinable()) return;
        m_host = host; m_port = port; m_user = user; m_domain = domain;
        m_pass = pass; m_title = title; m_w = w; m_h = h; m_bpp = bpp;
        m_clipboard = clipboard != 0;
        m_flags = flags; m_tlsLevel = tlsLevel; m_kbd = kbd; // M37/M38
        m_sound = sound;                                      // M39 (звук)
        m_drives = drives;                                    // M39 (/drive:)
        clipDiag("start: clipboard=%d (галка «Буфер обмена» доехала)", (int)m_clipboard);
        clipDiag("start: drives=%zu", m_drives.size());
        clipDiag("start: sound=%s", m_sound.empty() ? "(нет — sys:fake)" : m_sound.c_str());
        // M39: буфер обмена. Подписка на QClipboard — из Qt-нити (start вызывается
        // ею), контекст `this` отключает соединение при уничтожении канваса.
        // Сразу «праймим» кэш: если в локальном буфере уже что-то есть, сервер
        // узнает об этом по MonitorReady (иначе пришлось бы копировать заново).
        if (m_clipboard) {
            // Атомики и кэш сбрасываем без m_clipMu: воркер ещё не запущен
            // (start() гейтит повторный вход через m_thread.joinable(), а поток
            // создаётся позже, в beginWhenSized()).
            m_clipHave = false; m_clipAnnounce = false; m_clipReady = false;
            m_clipHaveDib = false; m_clipHaveHtml = false; m_clipTextAscii = false;
            m_clipHaveFiles = false;
            m_clipFilePending = false;
            m_clipPathsPending.clear();
            m_clipFileReady = false;
            m_clipReqKind = ClipReqNone;
            m_lastRemote.clear();
            m_lastRemoteHtmlHash = 0;
            // M39: файловый буфер. set_locally_available(TRUE) взводит
            // file_formats_registered, без него cliprdr_file_context_current_flags
            // вернёт 0 и сервер не узнает о поддержке файлов. Сам контекст
            // инициализируется позже — когда канал cliprdr уже существует
            // (cliprdr_file_context_init забирает cliprdr->custom себе).
            if (!m_clipFile) {
                m_clipFile = cliprdr_file_context_new(this);
                if (m_clipFile)
                    cliprdr_file_context_set_locally_available(m_clipFile, TRUE);
                else
                    clipDiag("cliprdr_file_context_new FAILED — файлы недоступны");
            }
            if (QClipboard* cb = QGuiApplication::clipboard())
                QObject::connect(cb, &QClipboard::dataChanged, this,
                                 [this]{ onLocalClipChanged(); });
            onLocalClipChanged();
        }
        // Нет явного /size — сессия открывается в РАЗМЕР КАНВАСА (как Remmina:
        // десктоп заполняет окно 1:1). Иначе фиксированный размер (1920x1080 /
        // экран) не совпадал с виджетом → кадр масштабировался / «квадратил».
        // Вызывается на Qt-нити (rdp_start). Если канвас ещё не разложен
        // (width/height == 0) — 1024x768, letterbox сгладит.
        m_stop = false; m_reported = false;
        m_needFull = true; // M40: первый кадр после connect копируем целиком
        beginWhenSized(0);
    }

    // M38: размер десктопа берём из РАЗМЕРА ВИДЖЕТА — тогда кадр идёт 1:1 без
    // масштабирования («квадраты»/мыло). Но сразу после AddTab виджет ещё не
    // разложен (width()==0) и раньше падало в 1024x768 → расхождение с окном.
    // Ждём раскладки короткими шагами; контекст `this` у singleShot — при
    // закрытии вкладки таймер снимается автоматически (не ловим висячий this).
    void beginWhenSized(int attempt) {
        if (m_thread.joinable()) return; // уже стартовали
        // Ревью MAJOR-1: вкладку могли закрыть В ОКНЕ ОЖИДАНИЯ раскладки —
        // тогда стартовать нельзя вообще (иначе полное рукопожатие на уже
        // умирающем канвасе). Ни потока, ни контекста.
        if (m_stop) { done(0, ""); return; }
        if (m_w <= 0 || m_h <= 0) {
            if (width() > 0 && height() > 0) {
                // HiDPI (ревью MINOR-3): десктоп задаём в ФИЗИЧЕСКИХ пикселях —
                // QPainter виджета уже несёт devicePixelRatio, поэтому кадр
                // ложится 1:1 с физической сеткой, а не растягивается в 2 раза
                // (иначе на 2x-экране картинка мылится).
                const qreal dpr = devicePixelRatioF() > 0.0 ? devicePixelRatioF() : 1.0;
                m_w = (int)(width() * dpr);
                m_h = (int)(height() * dpr);
                // Ревью M38 (MINOR-3, остаток): не запрашиваем десктоп больше
                // предела кадра (kMaxFrameBytes) — иначе setFrame отбросил бы
                // ВСЕ кадры молча и вкладка навсегда осталась бы в
                // «подключение…» с чёрным канвасом. Умножение на DPR приблизило
                // этот сценарий, поэтому клампим у источника.
                if (m_w > (int)kMaxSidePx) m_w = (int)kMaxSidePx;
                if (m_h > (int)kMaxSidePx) m_h = (int)kMaxSidePx;
            }
        }
        if (m_w > 0 && m_h > 0) {
            m_thread = std::thread([this]{ run(); });
            return;
        }
        if (attempt < 100) { // ~3 c шагами по 30 мс
            QTimer::singleShot(30, this, [this, attempt]{ beginWhenSized(attempt + 1); });
            return;
        }
        m_w = 1024; m_h = 768; // так и не разложился — прежнее поведение
        m_thread = std::thread([this]{ run(); });
    }

    void requestStop() {
        // M33-г: «разблокировка socket при закрытии вкладки» — если worker ещё в
        // freerdp_connect (блокирующий TCP/TLS), m_stop сам по себе его не прервёт.
        // abort_connect_context прерывает blocking connect на C++-нити; делаем под
        // m_ctxMu (исключаем гонку с освобождением контекста после connect).
        m_stop = true;
        std::lock_guard<std::mutex> lk(m_ctxMu);
        if (m_context) freerdp_abort_connect_context(m_context);
    }
    void joinThread() { if (m_thread.joinable()) m_thread.join(); }

    // M39-fullscreen: приложение вошло/вышло из полноэкранного режима вкладки
    // (см. rdp_set_fullscreen). Только Qt-нить.
    void setFullscreen(bool on) {
        m_fullscreen = on;
        if (!on) m_escHeld = false;
    }

    // M33-г: ошибка подключения рисуется на канвасе (вместо QMessageBox).
    void setError(const std::string& msg) {
        { std::lock_guard<std::mutex> lk(m_errMu); m_errMsg = msg; }
        QMetaObject::invokeMethod(this, [this]{ update(); }, Qt::QueuedConnection);
    }

    // M40: кадр обновляем ПО ГРЯЗНОМУ ПРЯМОУГОЛЬНИКУ (как xf_end_paint /
    // wl_end_paint), а не целиком на каждое EndPaint. m_img — ПОСТОЯННЫЙ буфер
    // кадра: в установившемся режиме нет ни аллокации, ни копии всего кадра,
    // копируются только строки dirty-rect'а. full=true — полная копия (первый
    // кадр после connect/ресайза: остальное содержимое буфера ещё невалидно).
    void setFrameRect(const uint8_t* bgra, int w, int h, int stride,
                      int rx, int ry, int rw, int rh, bool full) {
        // M33-г security: не копировать фреймбуфер при гигантских размерах (сервер
        // мог прислать gdi->width/height/stride огромными → DoS-копия/переполнение).
        if (!bgra || w <= 0 || h <= 0) return;
        if ((uint64_t)stride * (uint64_t)h > kMaxFrameBytes) return;
        QRect dirty;                 // повреждённая область в координатах кадра
        bool first = false;
        {
            QMutexLocker lk(&m_imgMu);
            // Размер сменился (ресайз/первый кадр) либо запрошена полная копия:
            // пересоздаём буфер ОДИН раз и копируем весь кадр.
            if (full || m_img.width() != w || m_img.height() != h) {
                m_img = QImage(w, h, QImage::Format_RGB32);
                rx = 0; ry = 0; rw = w; rh = h;
            }
            const int x0 = qBound(0, rx, w), y0 = qBound(0, ry, h);
            const int x1 = qBound(0, rx + rw, w), y1 = qBound(0, ry + rh, h);
            if (x1 <= x0 || y1 <= y0) return;
            for (int y = y0; y < y1; y++) {
                memcpy(m_img.scanLine(y) + (size_t)x0 * 4,
                       bgra + (size_t)y * (size_t)stride + (size_t)x0 * 4,
                       (size_t)(x1 - x0) * 4);
            }
            m_dw = w; m_dh = h;
            dirty = QRect(x0, y0, x1 - x0, y1 - y0);
            // exchange: два потока (воркер + drdynvc при GFX) не должны оба
            // решить, что они первые (Go-сторона и так под sync.Once, но
            // честность на нашей стороне).
            first = !m_frameReported.exchange(true);
        }
        // А2: первый фреймбуфер = клиент подключился → сигнал «подключено» в Go
        // (иначе вкладка висит на «подключение…» — ревью #2).
        if (first) rdpGoFrame(m_id, w, h);
        // Перерисовываем ТОЛЬКО повреждённую область — и в Qt, и (следствием)
        // в композиторе: update() без аргумента помечает весь виджет, из-за чего
        // на Wayland в шаренный буфер уходит весь кадр (25 МБ).
        // widgetRectFor читает QWidget (width/height) — только Qt-нить, поэтому
        // вызов внутри лямбды, а не здесь (мы в нити-воркере).
        QMetaObject::invokeMethod(this, [this, dirty] {
            const QRect r = widgetRectFor(dirty);
            if (!r.isEmpty()) update(r);
        }, Qt::QueuedConnection);
    }

    void pushKeyUnicode(uint32_t unicode, int down) {
        InputMsg m; m.kind = InputMsg::KeyUnicode; m.code = unicode; m.down = down;
        m.repeat = 0; // у unicode-события понятия «повтор» нет (см. InputMsg)
        if (down) m_inPushDown++; else m_inPushUp++; // M41: диагностика ввода
        QMutexLocker lk(&m_inMu); m_inputs.push_back(m);
    }
    void pushKeyScancode(uint32_t scancode, int down, int repeat) {
        InputMsg m; m.kind = InputMsg::KeyScancode; m.code = scancode; m.down = down;
        m.repeat = repeat;
        if (down) m_inPushDown++; else m_inPushUp++; // M41: диагностика ввода
        QMutexLocker lk(&m_inMu); m_inputs.push_back(m);
    }
    void pushPtr(uint16_t flags, int x, int y) {
        InputMsg m; m.kind = InputMsg::Ptr; m.flags = flags; m.x = x; m.y = y;
        QMutexLocker lk(&m_inMu); m_inputs.push_back(m);
    }

    // --- колбэки фреймбуфера (RDP-нить) ---
    // M40: как в эталонных клиентах (client/X11/xf_client.c:433 xf_end_paint,
    // client/Wayland/wlfreerdp.c:121 wl_end_paint) — берём dirty-rect кадра из
    // gdi->primary->hdc->hwnd->invalid и обновляем ТОЛЬКО его.
    // invalid->x/y/w/h — ОБЪЕДИНЯЮЩИЙ (bbox) прямоугольник всех инвалидаций
    // кадра; null=TRUE означает «в этом кадре ничего не менялось»
    // (update_begin_paint сбрасывает его перед разбором кадра, а
    // gdi_InvalidateRegion заполняет/расширяет — libfreerdp/gdi/region.c:613).
    BOOL onEndPaint(rdpContext* ctx) {
        if (m_savedEndPaint) m_savedEndPaint(ctx); // пусть GDI завершит кадр
        rdpGdi* gdi = ctx ? ctx->gdi : nullptr;
        if (!gdi || !gdi->primary_buffer || gdi->suppressOutput) return TRUE;
        HGDI_DC hdc = gdi->primary ? gdi->primary->hdc : nullptr;
        if (!hdc || !hdc->hwnd || !hdc->hwnd->invalid) return TRUE;
        const GDI_RGN* inv = hdc->hwnd->invalid;
        if (inv->null) return TRUE;  // в кадре нет ни одной инвалидации
        // Ревью M40 MINOR: флаг «полная копия» снимаем только если копия
        // ГАРАНТИРОВАННО состоится — иначе он потерялся бы и следующий кадр пошёл
        // бы частичным по невалидному буферу. Проверяем ровно те же условия, что
        // и setFrameRect (там они остаются как защита второго эшелона).
        if (gdi->width <= 0 || gdi->height <= 0) return TRUE;
        if ((uint64_t)gdi->stride * (uint64_t)gdi->height > kMaxFrameBytes) return TRUE;
        m_dgEndPaint++; // M41: счётчик презентаций (для живого лога)
        const bool full = m_needFull;
        m_needFull = false;
        setFrameRect((const uint8_t*)gdi->primary_buffer,
                     (int)gdi->width, (int)gdi->height, (int)gdi->stride,
                     inv->x, inv->y, inv->w, inv->h, full);
        // Сброс — как у эталонных клиентов: следующий update_begin_paint всё
        // равно сбросит, но так не остаётся «залипшего» bbox'а.
        hdc->hwnd->invalid->null = TRUE;
        hdc->hwnd->ninvalid = 0;
        return TRUE;
    }
    BOOL onDesktopResize(rdpContext* ctx) {
        if (m_savedDesktopResize) m_savedDesktopResize(ctx);
        rdpGdi* gdi = ctx ? ctx->gdi : nullptr;
        if (!gdi || !ctx || !ctx->settings) return TRUE;
        // РЕВЬЮ M40: прежний комментарий («gdi_resize перевыделил primary_buffer»)
        // был НЕВЕРЕН — gdi_resize у нас не звал никто, как и в эталонных клиентах:
        // его зовёт ИХ обработчик ресайза (client/X11/xf_client.c xf_desktop_resize).
        // Без него primary_buffer остаётся старого размера, а settings->Desktop*
        // уже новые → постоянный QImage перестаёт соответствовать кадру. Поэтому
        // перевыделяем сами, ровно как эталон.
        const UINT32 w = freerdp_settings_get_uint32(ctx->settings, FreeRDP_DesktopWidth);
        const UINT32 h = freerdp_settings_get_uint32(ctx->settings, FreeRDP_DesktopHeight);
        if (w > 0 && h > 0 && (w != gdi->width || h != gdi->height)) {
            // Ревью M40 MINOR-1: размер из Deactivate/Reactivation приходит от
            // сервера и не ограничен ничем (в server core data это UINT16, то есть
            // теоретически до 65535 на сторону). Политика та же, что kMaxFrameBytes
            // (8192 на сторону): ОТКАЗЫВАЕМ, а не клампим — перевыделить под чужой
            // размер значило бы рисовать кадр неверного размера.
            if (w > kMaxSidePx || h > kMaxSidePx) {
                // Предел — kMaxSidePx = 8192 пикселей на сторону (8192²×4 = 256 МиБ,
                // ровно kMaxFrameBytes), а НЕ 65535: заявленный сервером размер
                // сверх этого всё равно не прошёл бы в setFrameRect.
                clipDiag("[gfx] DesktopResize %ux%u отвергнут: больше предела %llu",
                         (unsigned)w, (unsigned)h, (unsigned long long)kMaxSidePx);
                return TRUE;
            }
            if (!gdi_resize(gdi, w, h)) return TRUE;
        }
        {
            // Пишем под m_imgMu: gdi->width/height — те же поля, что читает
            // fitRect()/paintEvent (ревью M33-г: гонка), плюс содержимое
            // постоянного QImage перестало соответствовать кадру.
            QMutexLocker lk(&m_imgMu);
            m_dw = (int)gdi->width; m_dh = (int)gdi->height;
            m_needFull = true;
        }
        // Ревью M40 MINOR: сменился fitRect — нужен ПОЛНЫЙ перерис, иначе в
        // новообразованных полях letterbox останется «призрак» прежнего кадра
        // (частичный update() их не покрывает, а явной заливки фона нет).
        QMetaObject::invokeMethod(this, [this]{ update(); }, Qt::QueuedConnection);
        return TRUE;
    }

protected:
    void paintEvent(QPaintEvent* e) override {
        QPainter p(this);
        // M33-г: ошибка подключения — прямо на канвасе (не QMessageBox).
        QString err;
        { std::lock_guard<std::mutex> lk(m_errMu); err = QString::fromStdString(m_errMsg); }
        if (!err.isEmpty()) {
            p.fillRect(rect(), QColor(24, 24, 30));
            p.setPen(QColor(240, 140, 140));
            QFont f = p.font(); f.setPointSizeF(f.pointSizeF() * 1.15); p.setFont(f);
            p.drawText(rect().adjusted(20, 20, -20, -20),
                       Qt::AlignCenter | Qt::TextWordWrap, err);
            return;
        }
        QMutexLocker lk(&m_imgMu);
        if (m_img.isNull()) { p.fillRect(e->rect(), QColor(0, 0, 0)); return; }
        // «Квадраты»: кадр масштабируется в виджет (см. fitRect), а по умолчанию
        // QPainter работает БЛИЖАЙШИМ СОСЕДОМ — при несовпадении размера десктопа
        // и виджета пиксели становятся квадратными. Включаем сглаживание.
        // Пиксель-в-пиксель это не заменяет: если нужна идеальная резкость —
        // задайте размер десктопа равным окну (галка «Размер экрана»).
        p.setRenderHint(QPainter::SmoothPixmapTransform, true);
        // M40: рисуем ТОЛЬКО запрошенную область. drawImage при этом
        // растеризует лишь пересечение клипа с fitRect() (замер: клип 200x100
        // логических — 0.052 мс против 1.574 мс на весь виджет), а не весь кадр.
        p.setClipRect(e->rect());
        // Обрыв при показанном кадре: рисуем ч/б снапшот последнего кадра
        // (см. grayLastFrame), а не живой буфер — цветной кадр выглядел как
        // живая сессия при красном индикаторе вкладки (user-report 2026-10-04).
        if (m_gray && !m_imgGray.isNull()) {
            p.drawImage(fitRect(), m_imgGray);
        } else {
            p.drawImage(fitRect(), m_img);
        }
    }
    // M40: прямоугольник dirty-rect'а кадра, переведённый в координаты виджета
    // через тот же fitRect(), что и отрисовка. Запас ±2 px — на округление
    // масштаба и на сглаживание (SmoothPixmapTransform берёт соседние пиксели).
    QRect widgetRectFor(const QRect& dr) const {
        const QRect f = fitRect();
        const int dw = m_dw.load(), dh = m_dh.load();
        if (f.isEmpty() || dr.isEmpty() || dw <= 0 || dh <= 0) return QRect();
        const qreal sx = qreal(f.width()) / qreal(dw);
        const qreal sy = qreal(f.height()) / qreal(dh);
        const QRect r(f.x() + int(dr.x() * sx), f.y() + int(dr.y() * sy),
                      int(dr.width() * sx) + 2, int(dr.height() * sy) + 2);
        return r.adjusted(-2, -2, 2, 2).intersected(rect());
    }
    // fitRect — прямоугольник отрисовки кадра с СОХРАНЕНИЕМ пропорций (как
    // Remmina): масштабируем десктоп (m_dw×m_dh) внутрь виджета по большей
    // стороне, центрируем, оставляя чёрные полосы. Иначе drawImage(rect())
    // растягивал 16:9-кадр на 4:3/другое окно → «квадраты» (очный дебаг).
    QRect fitRect() const {
        const int availW = width(), availH = height();
        if (m_dw <= 0 || m_dh <= 0 || availW <= 0 || availH <= 0)
            return rect();
        QSize scaled = QSize(m_dw, m_dh).scaled(QSize(availW, availH), Qt::KeepAspectRatio);
        int x = (availW - scaled.width()) / 2;
        int y = (availH - scaled.height()) / 2;
        return QRect(x, y, scaled.width(), scaled.height());
    }
    int fx(int x) const { return mapX(x); }
    int fy(int y) const { return mapY(y); }
    int mapX(int x) const {
        QRect r = fitRect();
        if (r.width() <= 0 || m_dw <= 0) return 0;
        int v = (x - r.x()) * m_dw / r.width();
        return v < 0 ? 0 : (v >= m_dw ? m_dw - 1 : v);
    }
    int mapY(int y) const {
        QRect r = fitRect();
        if (r.height() <= 0 || m_dh <= 0) return 0;
        int v = (y - r.y()) * m_dh / r.height();
        return v < 0 ? 0 : (v >= m_dh ? m_dh - 1 : v);
    }

    void mousePressEvent(QMouseEvent* e) override { mouse(e, true); }
    void mouseReleaseEvent(QMouseEvent* e) override { mouse(e, false); }
    void mouseMoveEvent(QMouseEvent* e) override { mouseMove(e); }
    // m_wheelAccum — накопленное вращение в единицах WHEEL_DELTA (120 = щелчок).
    int32_t m_wheelAccum = 0;
    int32_t m_hwheelAccum = 0; // то же для горизонтальной оси (M40-остаток)

    void wheelEvent(QWheelEvent* e) override {
        // Плавная прокрутка (тачпад/hi-res) приходит только пикселями:
        // переводим их в единицы WHEEL_DELTA по высоте строки канваса — как
        // TerminalWidget делает для терминала. Та же конверсия нужна и для
        // горизонтального колеса: без неё одна «строка» тачпада считалась
        // целым щелчком (~20x чувствительнее вертикали).
        const int lineH = qMax(1, fontMetrics().height());
        int wheel = QApplication::wheelScrollLines();
        if (wheel < 1) wheel = 1;
        const auto pxToWheel = [lineH, wheel](int px) {
            return int(px * 120.0 / (lineH * wheel));
        };

        int dy = e->angleDelta().y();
        if (dy == 0) {
            const int pdy = e->pixelDelta().y();
            if (pdy == 0) {
                // M40-остаток: горизонтальное колесо — PTR_FLAGS_HWHEEL
                // (Shift+колесо в Windows-приложениях). У КЛАССИЧЕСКОГО колеса
                // вращение приходит в angleDelta().x(); у тачпада — пикселями
                // pixelDelta().x(). Обе ветки сводим к единицам WHEEL_DELTA,
                // иначе горизонталь не работала вовсе (pixelDelta.x у обычного
                // колеса пуст).
                int dx = e->angleDelta().x();
                if (dx == 0) {
                    const int pdx = e->pixelDelta().x();
                    if (pdx == 0) {
                        QWidget::wheelEvent(e); // ноль по обеим осям — не наше
                        return;
                    }
                    dx = pxToWheel(pdx);
                }
                const int px = fx(e->position().x());
                const int py = fy(e->position().y());
                // Как и в вертикальной ветке, вращение передаём ОДИН раз:
                // обнуляем dx после первого шага и дальше только вычерпываем
                // остаток накопителя. Раньше pdx подставлялся на каждой
                // итерации, и при |pdx| >= 120 цикл не завершался никогда
                // (step снова и снова возвращал щелчок) — зависала Qt-нить.
                for (;;) {
                    const uint16_t hflags = rdp_hwheel_step(&m_hwheelAccum, dx);
                    dx = 0;
                    if (hflags == 0) break;
                    pushPtr(hflags, px, py);
                }
                return;
            }
            dy = pxToWheel(pdy);
        }
        // Величину больше двух щелчков хелпер отдаёт за несколько событий —
        // вычерпываем накопитель до нуля (как канон: разбиение, а не срез).
        const int px = fx(e->position().x());
        const int py = fy(e->position().y());
        for (;;) {
            const uint16_t flags = rdp_wheel_step(&m_wheelAccum, dy);
            dy = 0; // вращение передаём один раз, дальше вычерпываем остаток
            if (flags == 0) break; // целого щелчка не осталось
            pushPtr(flags, px, py);
        }
    }
    // Динамическое разрешение (галка «/dynamic-resolution»): ресайз окна → запрос
    // серверу сменить разрешение через канал DisplayControl (MS-RDPEDISP). Как у
    // xf_disp (client/X11/xf_disp.c:501 xf_disp_handle_configureNotify) цель —
    // размер окна; сам запрос шлёт worker (drainResize): вызовы
    // DispClientContext::SendMonitorLayout, как и весь libfreerdp, идут только
    // с RDP-нити. Без галки ничего не уходит — кадр масштабирует fitRect.
    void resizeEvent(QResizeEvent* e) override {
        QWidget::resizeEvent(e);
        if (!(m_flags & RDP_FLAG_DYNAMIC_RESOLUTION)) return;
        const qreal dpr = devicePixelRatioF() > 0.0 ? devicePixelRatioF() : 1.0;
        int w = 0, h = 0;
        if (!rdp_disp_resize_target(width(), height(), (double)dpr,
                                    m_dw.load(), m_dh.load(), &w, &h))
            return;
        // Last-wins: при перетаскивании края событий десятки, серверу нужен
        // только последний размер (как targetWidth/targetHeight в xf_disp).
        QMutexLocker lk(&m_resMu);
        m_resW = w; m_resH = h; m_resHave = true;
    }
    // Клавиатура сессии. Глобальные QShortcut главного окна (у них контекст
    // WindowShortcut) срабатывают при АКТИВНОМ ОКНЕ независимо от того, у кого
    // фокус, и СЪЕДАЮТ клавишу до канваса. Живые симптомы: F2 переименовывал
    // запись в дереве вместо ухода в сессию, а Ctrl+V/Ctrl+C (привязки секции
    // «Terminal» из дефолтов — copy/paste) вообще не доходили до RDP.
    // ShortcutOverride — штатный механизм Qt (подтверждено исходниками Qt 6.8 и
    // стендом): событие идёт виджету ФОКУСА, и приняв его, виджет говорит «отдай
    // клавишу мне как обычное нажатие» — дальше её обрабатывает keyPressEvent.
    // ВАЖНО: без фокуса на канвасе это не работает вовсе, поэтому фокус ставится
    // при открытии вкладки (internal/ui/ui.go: SetFocus после SetCurrentWidget).
    // Цена (проверено стендом): пока фокус в сессии, теряются УПРАВЛЯЮЩИЕ
    // сочетания приложения — Ctrl+W, CtrlShift+F4, Alt+1..9, quit. Клавиатурный
    // выход есть: Tab уводит фокус на QTabBar (это фокус-обход Qt), после чего
    // app-шорткаты снова живы; клик по дереву/тулбару — тоже.
    // Терминальные вкладки этим НЕ покрыты: QTermWidget ShortcutOverride не
    // принимает (проверено стендом и nm), там причина другая — привязки секции
    // «Connections Tree» ставятся на окно, потому что bindingActions игнорирует
    // секцию (internal/ui/hotkeys.go). См. BACKLOG.md, M39.
    bool event(QEvent* e) override {
        if (e->type() == QEvent::ShortcutOverride) {
            e->accept();
            return true;
        }
        return QWidget::event(e);
    }
    void keyPressEvent(QKeyEvent* e) override {
        // M39-fullscreen: в полноэкранном режиме вкладки одиночный Esc — выход
        // из него (локальное действие), а не клавиша удалённой сессии. В обычном
        // режиме Esc уходит серверу как раньше. Отпускание «съеденного» Esc тоже
        // не отправляем (m_escHeld), иначе сервер получил бы release без press.
        if (m_fullscreen && e->key() == Qt::Key_Escape &&
            e->modifiers() == Qt::NoModifier) {
            m_escHeld = true;
            rdpGoFullscreen(m_id);
            return;
        }
        // Новый press обычной клавиши: «съеденный» ранее Esc уже не актуален —
        // иначе его поздний release мог бы проглотить release этой клавиши.
        m_escHeld = false;
        key(e, true);
    }
    void keyReleaseEvent(QKeyEvent* e) override {
        if (m_escHeld && e->key() == Qt::Key_Escape) {
            m_escHeld = false;
            return;
        }
        key(e, false);
    }
    // Потеря фокуса: отпускаем всё, что считаем нажатым (ревью M43 D5). Иначе на
    // сервере клавиша остаётся зажатой — включается серверный автоповтор, а
    // зажатый модификатор превращает дальнейший ввод в акселераторы. Эталонные
    // клиенты делают то же (client/X11/xf_keyboard.c xf_keyboard_release_all_keypress).
    void focusOutEvent(QFocusEvent*) override {
        for (const auto& kv : m_keyScancode) pushKeyScancode(kv.second, 0, 0);
        for (const auto& kv : m_keyUnicode) pushKeyUnicode(kv.second, 0);
        m_keyScancode.clear();
        m_keyUnicode.clear();
        // M39-fullscreen: release «съеденного» Esc может уже не дойти — сбрасываем,
        // чтобы следующим Esc не проглотить release обычного нажатия.
        m_escHeld = false;
    }

private:
    static uint16_t buttonFlag(Qt::MouseButton b) {
        if (b == Qt::LeftButton) return PTR_FLAGS_BUTTON1;
        if (b == Qt::RightButton) return PTR_FLAGS_BUTTON2;
        if (b == Qt::MiddleButton) return PTR_FLAGS_BUTTON3;
        return 0;
    }
    void mouse(QMouseEvent* e, bool press) {
        uint16_t flags = buttonFlag(e->button());
        if (press) flags |= PTR_FLAGS_DOWN;
        pushPtr(flags, fx(e->position().x()), fy(e->position().y()));
    }
    void mouseMove(QMouseEvent* e) {
        uint16_t flags = PTR_FLAGS_MOVE;
        if (e->buttons() & Qt::LeftButton) flags |= PTR_FLAGS_BUTTON1;
        if (e->buttons() & Qt::RightButton) flags |= PTR_FLAGS_BUTTON2;
        if (e->buttons() & Qt::MiddleButton) flags |= PTR_FLAGS_BUTTON3;
        pushPtr(flags, fx(e->position().x()), fy(e->position().y()));
    }
    // M43: путь ввода. Печатные клавиши раньше ВСЕГДА уходили unicode-событиями
    // (freerdp_input_send_unicode_keyboard_event). На Windows это инъекция
    // VK_PACKET, и всё, что на сервере сидит на низкоуровневых хуках клавиатуры
    // (IME, автопереключатели раскладки, макросы, хуки самого приложения),
    // обрабатывает такое событие ИНАЧЕ, чем обычное нажатие. KRDC, который шлёт
    // только физические сканкоды (в его плагине нет даже символа unicode-отправки,
    // проверено nm), на том же сервере не дублирует — а мы дублировали.
    // Теперь по умолчанию шлём ФИЗИЧЕСКИЙ сканкод (позиционно, как KRDC/xfreerdp/
    // mstsc: сервер переводит его СВОЕЙ раскладкой), unicode остаётся запасным
    // путём для клавиш без физического кода. Вернуть прежнее: ASBRU_RDP_KBD=unicode.
    static bool kbdUnicodeMode() {
        static const bool uni = [] {
            const char* v = getenv("ASBRU_RDP_KBD");
            return v && *v && strcmp(v, "unicode") == 0;
        }();
        return uni;
    }
    // Таблица FreeRDP пуста, пока не позван freerdp_keyboard_init() — без него
    // функция возвращает 0 на любой keycode (проверено пробой).
    static uint32_t scancodeFromNative(uint32_t native) {
        if (!native) return 0;
        static std::once_flag once;
        static bool tableOk = false;
        std::call_once(once, [] {
            freerdp_keyboard_init(0);
            // Санити-проба таблицы (ревью M43 D1): если X недоступен (чистый
            // Wayland без XWayland), FreeRDP заполняет таблицу по EVDEV-кодам, а
            // мы читаем её по X11-keycode — молчаливый сдвиг на 8, то есть на
            // сервер уехали бы СОСЕДНИЕ клавиши. Проверяем эталонные пары и, если
            // не сошлось, возвращаем 0 → вызывающий уходит на unicode-путь
            // (раскладко-независимый) вместо тихо неверных символов.
            const DWORD a = freerdp_keyboard_get_rdp_scancode_from_x11_keycode(42); // 'g'
            const DWORD b = freerdp_keyboard_get_rdp_scancode_from_x11_keycode(22); // Backspace
            tableOk = (RDP_SCANCODE_CODE(a) == 0x22) && (RDP_SCANCODE_CODE(b) == 0x0e);
            if (!tableOk)
                clipDiag("[kbd] ТАБЛИЦА СКАНКОДОВ НЕВЕРНА (X недоступен?) — "
                         "принудительно unicode-ввод, сканкоды не используем");
        });
        if (!tableOk) return 0;
        return (uint32_t)freerdp_keyboard_get_rdp_scancode_from_x11_keycode(native);
    }

    void key(QKeyEvent* e, bool down) {
        const int k = e->key();
        const int rep = e->isAutoRepeat() ? 1 : 0;
        // Физический сканкод клавиши (X11 keycode → RDP scancode). 0 — неизвестен.
        const uint32_t nat = kbdUnicodeMode() ? 0 : scancodeFromNative((uint32_t)e->nativeScanCode());
        // M41: диагностика ввода — считаем по границам (Qt → очередь → отправка),
        // чтобы на живом сеансе видеть, где именно появляется лишнее событие.
        if (down) {
            m_inQtPress++;
            if (rep) m_inQtRepeat++;
            // Нажатие клавиши, которую мы считаем уже нажатой (не было отпускания):
            // именно это выглядит на сервере как автоповтор/дубли.
            if (m_keyUnicode.count(k) || m_keyScancode.count(k)) m_inDupDown++;
        } else {
            m_inQtRelease++;
        }
        // Отпускание ОБЯЗАНО уйти тем же путём, что и нажатие (ревью MINOR-2):
        // иначе сервер получает keydown сканкодом, а keyup — unicode-событием, и
        // клавиша «залипает». Сценарий: Ctrl↓ V↓ Ctrl↑ V↑ — к моменту отпускания
        // V модификатор уже снят, text() стал печатным «v», и без этой памяти
        // половина пары ушла бы другим путём. Обратный случай (нажатие unicode,
        // отпускание со сканкодом) симметричен — помним оба пути.
        if (!down) {
            if (auto it = m_keyScancode.find(k); it != m_keyScancode.end()) {
                // Код берём ИЗ НАЖАТИЯ, а не выводим заново: scancodeFor() для
                // кириллицы (Qt::Key == кодовая точка) вернул бы 0, и пара
                // «down сканкодом, up — ничем» оставила бы клавишу зажатой на
                // сервере (а это как раз «залипание»/автоповтор).
                const uint32_t sc = it->second;
                m_keyScancode.erase(it);
                pushKeyScancode(sc, 0, 0);
                return;
            }
            if (auto it = m_keyUnicode.find(k); it != m_keyUnicode.end()) {
                const uint32_t u = it->second;
                m_keyUnicode.erase(it);
                pushKeyUnicode(u, 0);
                return;
            }
            m_inStrayUp++; // отпускание без парного нажатия (потерян press/смена фокуса)
        }
        QChar c = e->text().isEmpty() ? QChar() : e->text().at(0);
        if (!c.isNull() && c.isPrint()) {
            // M43: печатный символ уходит СКАНКОДОМ, если физический код известен
            // (сервер переведёт его своей раскладкой — как KRDC/xfreerdp/mstsc);
            // иначе — прежним unicode-путём (раскладко-независимым).
            if (nat) {
                if (down) m_keyScancode[k] = nat;
                pushKeyScancode(nat, down ? 1 : 0, down ? rep : 0);
                return;
            }
            const uint32_t u = (uint32_t)c.unicode();
            if (down) m_keyUnicode[k] = u;
            pushKeyUnicode(u, down ? 1 : 0);
            return;
        }
        uint32_t sc = nat ? nat : scancodeFor(k);
        if (sc) {
            if (down) m_keyScancode[k] = sc;
            // repeat осмыслен только для нажатия (у эталонов — xf_keyboard.c,
            // wlf_input.c: флаг «клавиша уже была нажата»)
            pushKeyScancode(sc, down ? 1 : 0, down ? rep : 0);
        }
    }
    // M39 (нашёл живой тест: не работали Ctrl+C/V/X/A/Z, а Shift+Insert работал).
    // Печатные клавиши уходят Unicode-путём, и это правильно (работает при любой
    // раскладке), НО при зажатом Ctrl Qt отдаёт text() управляющим символом
    // (Ctrl+V → \x16) — он не isPrint(), поэтому мы падали сюда, а в scancodeFor
    // для букв и цифр не было ни одного case'а: не отправлялось НИЧЕГО.
    // Сканкод — это ФИЗИЧЕСКАЯ клавиша (Set 1), именно её ждёт сервер в
    // сочетаниях с модификаторами; сами модификаторы мы уже шлём отдельными
    // событиями (LCONTROL/LSHIFT/LMENU).
    static uint32_t printableScancode(int key) {
        static const struct {
            int qt;
            uint32_t sc;
        } tab[] = {
            {Qt::Key_A, RDP_SCANCODE_KEY_A}, {Qt::Key_B, RDP_SCANCODE_KEY_B},
            {Qt::Key_C, RDP_SCANCODE_KEY_C}, {Qt::Key_D, RDP_SCANCODE_KEY_D},
            {Qt::Key_E, RDP_SCANCODE_KEY_E}, {Qt::Key_F, RDP_SCANCODE_KEY_F},
            {Qt::Key_G, RDP_SCANCODE_KEY_G}, {Qt::Key_H, RDP_SCANCODE_KEY_H},
            {Qt::Key_I, RDP_SCANCODE_KEY_I}, {Qt::Key_J, RDP_SCANCODE_KEY_J},
            {Qt::Key_K, RDP_SCANCODE_KEY_K}, {Qt::Key_L, RDP_SCANCODE_KEY_L},
            {Qt::Key_M, RDP_SCANCODE_KEY_M}, {Qt::Key_N, RDP_SCANCODE_KEY_N},
            {Qt::Key_O, RDP_SCANCODE_KEY_O}, {Qt::Key_P, RDP_SCANCODE_KEY_P},
            {Qt::Key_Q, RDP_SCANCODE_KEY_Q}, {Qt::Key_R, RDP_SCANCODE_KEY_R},
            {Qt::Key_S, RDP_SCANCODE_KEY_S}, {Qt::Key_T, RDP_SCANCODE_KEY_T},
            {Qt::Key_U, RDP_SCANCODE_KEY_U}, {Qt::Key_V, RDP_SCANCODE_KEY_V},
            {Qt::Key_W, RDP_SCANCODE_KEY_W}, {Qt::Key_X, RDP_SCANCODE_KEY_X},
            {Qt::Key_Y, RDP_SCANCODE_KEY_Y}, {Qt::Key_Z, RDP_SCANCODE_KEY_Z},
            {Qt::Key_0, RDP_SCANCODE_KEY_0}, {Qt::Key_1, RDP_SCANCODE_KEY_1},
            {Qt::Key_2, RDP_SCANCODE_KEY_2}, {Qt::Key_3, RDP_SCANCODE_KEY_3},
            {Qt::Key_4, RDP_SCANCODE_KEY_4}, {Qt::Key_5, RDP_SCANCODE_KEY_5},
            {Qt::Key_6, RDP_SCANCODE_KEY_6}, {Qt::Key_7, RDP_SCANCODE_KEY_7},
            {Qt::Key_8, RDP_SCANCODE_KEY_8}, {Qt::Key_9, RDP_SCANCODE_KEY_9},
            {Qt::Key_Space, RDP_SCANCODE_SPACE},
            // Пунктуация — US-раскладка (как в самом FreeRDP для VK_OEM_*).
            {Qt::Key_Minus, RDP_SCANCODE_OEM_MINUS},
            {Qt::Key_Equal, RDP_SCANCODE_OEM_PLUS},
            {Qt::Key_BracketLeft, RDP_SCANCODE_OEM_4},
            {Qt::Key_BracketRight, RDP_SCANCODE_OEM_6},
            {Qt::Key_Semicolon, RDP_SCANCODE_OEM_1},
            {Qt::Key_Apostrophe, RDP_SCANCODE_OEM_7},
            {Qt::Key_QuoteLeft, RDP_SCANCODE_OEM_3},
            {Qt::Key_Backslash, RDP_SCANCODE_OEM_5},
            {Qt::Key_Comma, RDP_SCANCODE_OEM_COMMA},
            {Qt::Key_Period, RDP_SCANCODE_OEM_PERIOD},
            {Qt::Key_Slash, RDP_SCANCODE_OEM_2},
        };
        for (const auto& e : tab) {
            if (e.qt == key) return e.sc;
        }
        return 0;
    }
    static uint32_t scancodeFor(int key) {
        if (uint32_t sc = printableScancode(key)) return sc; // Ctrl/Alt+клавиша
        switch (key) {
            case Qt::Key_Backspace: return RDP_SCANCODE_BACKSPACE;
            case Qt::Key_Tab: return RDP_SCANCODE_TAB;
            case Qt::Key_Return: case Qt::Key_Enter: return RDP_SCANCODE_RETURN;
            case Qt::Key_Escape: return RDP_SCANCODE_ESCAPE;
            case Qt::Key_Delete: return RDP_SCANCODE_DELETE;
            case Qt::Key_Insert: return RDP_SCANCODE_INSERT;
            case Qt::Key_Home: return RDP_SCANCODE_HOME;
            case Qt::Key_End: return RDP_SCANCODE_END;
            case Qt::Key_PageUp: return RDP_SCANCODE_PRIOR;
            case Qt::Key_PageDown: return RDP_SCANCODE_NEXT;
            case Qt::Key_Left: return RDP_SCANCODE_LEFT;
            case Qt::Key_Right: return RDP_SCANCODE_RIGHT;
            case Qt::Key_Up: return RDP_SCANCODE_UP;
            case Qt::Key_Down: return RDP_SCANCODE_DOWN;
            case Qt::Key_Shift: return RDP_SCANCODE_LSHIFT;
            case Qt::Key_Control: return RDP_SCANCODE_LCONTROL;
            // M33-г «полировка ввода»: модификаторы/спец-клавиши, которых не было.
            case Qt::Key_Alt: return RDP_SCANCODE_LMENU;
            case Qt::Key_AltGr: return RDP_SCANCODE_RMENU;
            case Qt::Key_Meta: return RDP_SCANCODE_LWIN;
            case Qt::Key_Super_L: return RDP_SCANCODE_LWIN;
            case Qt::Key_Super_R: return RDP_SCANCODE_RWIN;
            case Qt::Key_CapsLock: return RDP_SCANCODE_CAPSLOCK;
            case Qt::Key_ScrollLock: return RDP_SCANCODE_SCROLLLOCK;
            case Qt::Key_Pause: return RDP_SCANCODE_PAUSE;
            case Qt::Key_Menu: return RDP_SCANCODE_APPS;
        }
        if (key >= Qt::Key_F1 && key <= Qt::Key_F12)
            return MAKE_RDP_SCANCODE(0x3B + (uint32_t)(key - Qt::Key_F1), FALSE);
        return 0;
    }

    // --- M39: буфер обмена (текст CF_UNICODETEXT + картинки CF_DIB) ---
    // Направление «локальный → сервер». QClipboard::dataChanged приходит В
    // QT-НИТИ, а канал живёт в нити-воркере (все колбэки cliprdr зовутся из
    // freerdp_check_event_handles). Поэтому содержимое КЭШИРУЕМ под mutex, а
    // воркер сам объявляет форматы (pumpClipboard) и отвечает на запросы
    // сервера. Qt-буфер из воркера не трогаем вовсе — иначе понадобился бы
    // BlockingQueuedConnection (риск дедлока при закрытии вкладки).

    // Дешёвый хэш СОДЕРЖИМОГО картинки для гашения эха setImage: после
    // сервер→локальный Qt присылает dataChanged на наш же setImage, и без
    // сверки мы объявили бы ту же картинку обратно серверу. Приводим к
    // Format_RGB32, чтобы представление не зависело от исходного формата.
    static uint64_t imageHash(const QImage& img) {
        if (img.isNull()) return 0;
        QImage n = img;
        if (n.format() != QImage::Format_RGB32)
            n = n.convertToFormat(QImage::Format_RGB32);
        if (n.isNull()) return 0;
        size_t h = qHashBits(n.constBits(), (size_t)n.sizeInBytes(), 0);
        h ^= (size_t)n.width() * (size_t)0x9E3779B97F4A7C15ULL;
        h ^= (size_t)n.height() * (size_t)0xC2B2AE3D27D4EB4FULL;
        return h ? (uint64_t)h : 1; // 0 зарезервирован под «эха не было»
    }

    // Хэш HTML для гашения эха setMimeData: сервер→локальный HTML ставится
    // вместе с plain text, dataChanged приходит на наш же setMimeData. Сверяем
    // СОДЕРЖИМОЕ html (как текст/картинку), а не одноразовый латч. 0 — «эха не
    // было». Только Qt-нить: пишет лямбда cbClipDataResponse, читает
    // onLocalClipChanged.
    static uint64_t htmlHash(const QString& html) {
        if (html.isEmpty()) return 0;
        const QByteArray b = html.toUtf8();
        if (b.isEmpty()) return 0;
        size_t h = qHashBits(b.constData(), (size_t)b.size(), 0);
        return h ? (uint64_t)h : 1;
    }

    // QString → байты CF_HTML (UTF-8 payload с заголовком). Пусто/слишком
    // большой HTML — false. Вызывается из Qt-нити, результат уходит в кэш.
    static bool htmlToCfHtml(const QString& html, std::string* out) {
        if (!out || html.isEmpty()) return false;
        const QByteArray u8 = html.toUtf8();
        if (u8.isEmpty() || (uint64_t)u8.size() + 105ULL > 0xFFFFFFFFULL)
            return false;
        out->resize((size_t)u8.size() + 105);
        const uint32_t n = rdp_cf_html_build(
            (const uint8_t*)u8.constData(), (uint32_t)u8.size(),
            (uint8_t*)out->data(), (uint32_t)out->size());
        if (n == 0) {
            out->clear();
            return false;
        }
        out->resize(n);
        return true;
    }

    // QImage → DIB для провода (CF_DIB): QImageWriter("BMP") пишет BMP-файл,
    // срезаем 14-байтный BITMAPFILEHEADER. Формат пикселей для CF_DIB годится
    // любой из поддерживаемых BMP (Qt сам выбирает BITMAPINFOHEADER).
    static bool imageToDib(const QImage& img, std::string* out) {
        if (img.isNull() || !out) return false;
        QByteArray bmp;
        {
            QBuffer buf(&bmp);
            if (!buf.open(QIODevice::WriteOnly)) return false;
            QImageWriter w(&buf, "BMP");
            if (!w.write(img)) {
                clipDiag("local image: QImageWriter(BMP) не смог записать: %s",
                         w.errorString().toUtf8().constData());
                return false;
            }
            buf.close();
        }
        if (bmp.size() <= 14 || (uint64_t)bmp.size() > 0xFFFFFFFFULL) return false;
        uint32_t dibLen = 0;
        if (!rdp_dib_strip_bmp_header((const uint8_t*)bmp.constData(),
                                      (uint32_t)bmp.size(), &dibLen) || dibLen == 0)
            return false;
        out->assign(bmp.constData() + 14, dibLen);
        return true;
    }

    // M39: локальные URL буфера → пути файлов. Только isLocalFile(); удалённые
    // URL (http и т.п.) в файловый буфер не годятся. Хвостовые '/' срезаем:
    // QUrl::fromLocalFile("/tmp/dir/") даёт путь с ним, а basename тогда пуст.
    static std::vector<std::string> localFilePaths(const QMimeData* md) {
        std::vector<std::string> paths;
        if (!md || !md->hasUrls()) return paths;
        const QList<QUrl> urls = md->urls();
        for (const QUrl& u : urls) {
            if (!u.isLocalFile()) continue;
            QString p = u.toLocalFile();
            while (p.size() > 1 && p.endsWith(QLatin1Char('/'))) p.chop(1);
            if (p.isEmpty()) continue;
            paths.push_back(p.toStdString());
        }
        return paths;
    }
    static bool buildUriList(const std::vector<std::string>& paths, std::string* out) {
        if (!out || paths.empty() || paths.size() > 0xFFFFFFFFULL) return false;
        std::vector<const char*> cp;
        cp.reserve(paths.size());
        for (const auto& p : paths) cp.push_back(p.c_str());
        const uint32_t n = rdp_uri_list_build(cp.data(), (uint32_t)cp.size(), nullptr, 0);
        if (n == 0) return false;
        out->resize(n);
        const uint32_t got = rdp_uri_list_build(
            cp.data(), (uint32_t)cp.size(), (uint8_t*)out->data(), n);
        if (got != n) { out->clear(); return false; }
        return true;
    }
    // Сборка CLIPRDR_FILELIST одним обходом под производственными пределами
    // (ag-32m). Тяжёлая часть (stat по каждому элементу дерева) — поэтому
    // вызывать её можно ТОЛЬКО из воркера: onLocalClipChanged (Qt-нить) лишь
    // публикует пути, а списки строит pumpClipboard().
    static bool buildFileList(const std::vector<std::string>& paths, std::string* out) {
        if (!out || paths.empty() || paths.size() > 0xFFFFFFFFULL) return false;
        std::vector<const char*> cp;
        cp.reserve(paths.size());
        for (const auto& p : paths) cp.push_back(p.c_str());
        uint32_t n = 0;
        int reason = kRdpClipListOk;
        uint8_t* buf = rdp_cliprdr_file_list_alloc(
            cp.data(), (uint32_t)cp.size(), &n, &reason);
        if (!buf || n == 0) {
            clipDiag("file list build FAILED: reason=%d paths=%d",
                     reason, (int)paths.size());
            return false;
        }
        out->assign((const char*)buf, n);
        rdp_free(buf);
        return true;
    }

    void onLocalClipChanged() {
        QClipboard* cb = QGuiApplication::clipboard();
        if (!cb) return;
        const QMimeData* md = cb->mimeData();
        const bool hasImage = md && md->hasImage();
        // M39: файлы. update_client_data разбирает URI-список в локальные потоки,
        // из которых cliprdr_file_context отдаёт FILECONTENTS_REQUEST серверу.
        // ag-d5u: вызывать его из GUI-нити НЕЛЬЗЯ — CliprdrFileContext не
        // потокобезопасен (поля local_lock_id/client_data_hash/flags живут без
        // общего мьютекса), а файловые колбэки обслуживаются воркером. Эталонные
        // xf/wlf-клиенты зовут update_client_data строго в нити клиента — у нас
        // это воркер.
        // ag-32m: сама СБОРКА списков (рекурсивный обход каталогов со stat на
        // каждый элемент и 592 Б на файл) тоже ушла в воркер: на большом дереве
        // она замораживала GUI на секунды и могла бросить bad_alloc прямо в
        // Qt-слоте. Здесь — только дешёвое извлечение путей из QMimeData (никаких
        // stat): пути кладутся в «почтовый ящик» m_clipPathsPending, списки
        // строит и применяет воркер (pumpClipboard) под пределами.
        // haveFiles здесь — «пути есть и контекст есть»; факт регистрации
        // подтвердит воркер (он же выставит m_clipHaveFiles).
        std::vector<std::string> filePaths = localFilePaths(md);
        const int nFilePaths = (int)filePaths.size();
        const bool haveFiles = !filePaths.empty() && m_clipFile != nullptr;
        if (haveFiles)
            clipDiag("local files: %d (списки соберёт воркер)", nFilePaths);
        // HTML забираем один раз (mimeData()->html() — тоже round-trip к
        // владельцу буфера, как text()). hasHtml => есть text/html; plain text
        // при этом обычно тоже есть, но не обязан.
        const QString html = (md && md->hasHtml()) ? md->html() : QString();
        const bool hasHtml = !html.isEmpty();
        // text() — не бесплатная копия (round-trip к владельцу буфера), поэтому
        // читаем ОДИН раз (ревью MINOR-5: диагностика сама создавала нагрузку).
        const QString t = cb->text();
        clipDiag("local dataChanged: textlen=%d html=%d image=%d lastRemote=%d",
                 (int)t.size(), hasHtml ? 1 : 0, hasImage ? 1 : 0,
                 (int)m_lastRemote.size());
        if (hasImage) {
            const QImage img = cb->image();
            if (!img.isNull()) {
                // Эхо: ровно эту картинку мы только что выставили из сервера —
                // обратно не шлём. Сверка СОДЕРЖИМОГО (как у текста ниже), а не
                // одноразовый латч: латч съел бы следующее настоящее копирование.
                // При наличии файлов это заведомо настоящее локальное
                // копирование, а не эхо серверного setImage: не глотаем его.
                if (!haveFiles && imageHash(img) == m_lastRemoteImageHash) return;
                std::string dib;
                if (imageToDib(img, &dib)) {
                    // Копирование картинки НЕ должно объявлять пустой
                    // CF_UNICODETEXT: текст рекламируем только если он реально
                    // непустой (у чисто-графического буфера text() пуст).
                    std::string u16;
                    const bool haveText = !t.isEmpty();
                    bool textAscii = false;
                    if (haveText) {
                        u16 = utf16Nul(t);
                        const QByteArray u8 = t.toUtf8();
                        textAscii = !u8.isEmpty() &&
                            rdp_text_is_ascii((const uint8_t*)u8.constData(),
                                              (uint32_t)u8.size()) != 0;
                    }
                    {
                        QMutexLocker lk(&m_clipMu);
                        m_clipUtf16.swap(u16);
                        m_clipDib.swap(dib);
                        m_clipHave = haveText;
                        m_clipTextAscii = haveText && textAscii;
                        m_clipHaveDib = true;
                        m_clipHtml.clear();     // картинка вытеснила HTML
                        m_clipHaveHtml = false;
                        // Пути — воркеру; тяжёлую сборку списков он сделает сам.
                        if (haveFiles) m_clipPathsPending = std::move(filePaths);
                        else m_clipPathsPending.clear();
                        m_clipFilePending = haveFiles; // применит воркер (ag-d5u)
                        if (!haveFiles) {
                            // Копирование без файлов: файловый формат снимаем
                            // сразу (дескрипторы и потоки публикует воркер парой).
                            m_clipFileList.clear();
                            m_clipHaveFiles = false;
                        }
                        m_clipAnnounce = true; // объявить новый список форматов
                    }
                    // Текущее содержимое больше не совпадает с присланным
                    // сервером — старые маркеры эха неактуальны.
                    m_lastRemote.clear();
                    m_lastRemoteImageHash = 0;
                    m_lastRemoteHtmlHash = 0;
                    return;
                }
                // Сериализация не удалась — пробуем текстовый путь ниже.
            }
        }
        // HTML-эхо: ровно этот HTML мы только что выставили из сервера
        // (setMimeData с html + plain text) — обратно не объявляем. Сверка
        // содержимого, как у текста/картинки, сохраняет следующие настоящие
        // копирования.
        if (!haveFiles && hasHtml && htmlHash(html) == m_lastRemoteHtmlHash) return;
        // Текст. Эхо: ровно этот текст мы только что выставили из сервера —
        // обратно не шлём. Сверка СОДЕРЖИМОГО, а не одноразовый латч (ревью
        // MINOR-5): латч «съел» бы следующее настоящее копирование пользователя,
        // если платформа почему-либо не эмитит dataChanged на наш setText.
        // При HTML сверка уже сделана выше по html: plain text может совпасть
        // со старым серверным, оставаясь другим HTML.
        if (!haveFiles && !hasHtml && t == m_lastRemote) return;
        std::string u16 = utf16Nul(t);
        const bool haveText = !t.isEmpty();
        // CF_TEXT рекламируем только для чистого ASCII: на проводе это ANSI-
        // строка системной кодовой страницы Windows, и UTF-8 с не-ASCII байтами
        // превратился бы в мозаику (см. rdp_text_is_ascii). Не-ASCII текст идёт
        // исключительно как CF_UNICODETEXT.
        bool textAscii = false;
        if (haveText) {
            const QByteArray u8 = t.toUtf8();
            textAscii = !u8.isEmpty() &&
                rdp_text_is_ascii((const uint8_t*)u8.constData(),
                                  (uint32_t)u8.size()) != 0;
        }
        std::string cfHtml;
        const bool haveHtmlPayload = hasHtml && htmlToCfHtml(html, &cfHtml);
        {
            QMutexLocker lk(&m_clipMu);
            m_clipUtf16.swap(u16);
            m_clipHave = haveText;
            m_clipTextAscii = haveText && textAscii;
            m_clipHaveDib = false; // картинки в этом буфере нет
            m_clipDib.clear();
            m_clipHtml.swap(cfHtml);
            m_clipHaveHtml = haveHtmlPayload;
            // Пути — воркеру; тяжёлую сборку списков он сделает сам.
            if (haveFiles) m_clipPathsPending = std::move(filePaths);
            else m_clipPathsPending.clear();
            m_clipFilePending = haveFiles; // применит воркер (ag-d5u)
            if (!haveFiles) {
                // Копирование без файлов: файловый формат снимаем сразу.
                m_clipFileList.clear();
                m_clipHaveFiles = false;
            }
            m_clipAnnounce = true; // объявить серверу новый список форматов
        }
        m_lastRemoteImageHash = 0; // прошлое эхо-изображение неактуально
        m_lastRemoteHtmlHash = 0;  // это настоящее локальное копирование
    }
    // Воркер: объявить серверу свои форматы — после MonitorReady и на каждое
    // изменение локального буфера (как xfreerdp; до MonitorReady сервер списки
    // не принимает).
    // Клиентские возможности канала — как у официального клиента: один
    // general-набор, длина 12, версия 2. Шлём их ПЕРВЫМИ на MonitorReady:
    // без CLIP_CAPS сервер (проверено на живом Windows) не реагирует на наш
    // формат-лист — он просто молчит, и буфер не работает ни в одну сторону.
    void sendClientCaps() {
        if (!m_cliprdr) return;
        CLIPRDR_GENERAL_CAPABILITY_SET general;
        memset(&general, 0, sizeof(general));
        general.capabilitySetType = CB_CAPSTYPE_GENERAL;
        general.capabilitySetLength = CB_CAPSTYPE_GENERAL_LEN;
        general.version = CB_CAPS_VERSION_2;
        general.generalFlags = CB_USE_LONG_FORMAT_NAMES;
        // M39: файловый буфер. current_flags — как у эталона (xf/wlf): вернёт
        // CB_STREAM_FILECLIP_ENABLED|CB_FILECLIP_NO_FILE_PATHS|CB_HUGE_FILE_SUPPORT
        // только если сервер объявил поддержку (ServerCapabilities) и локальные
        // форматы зарегистрированы (set_locally_available), иначе 0.
        if (m_clipFile)
            general.generalFlags |= cliprdr_file_context_current_flags(m_clipFile);
        CLIPRDR_CAPABILITIES caps;
        memset(&caps, 0, sizeof(caps));
        caps.cCapabilitiesSets = 1;
        caps.capabilitySets = (CLIPRDR_CAPABILITY_SET*)&general;
        UINT rc = m_cliprdr->ClientCapabilities(m_cliprdr, &caps);
        clipDiag("send ClientCapabilities rc=%u", (unsigned)rc);
    }
    // ag-d5u/ag-32m: применить отложенные пути в файловом контексте.
    // ТОЛЬКО воркер. CliprdrFileContext не потокобезопасен (внутренние поля
    // local_lock_id/client_data_hash и stream->files меняются вне какого-либо
    // нашего мьютекса), поэтому все обращения к нему — new/set_locally_available
    // (в start(), до запуска воркера), init/uninit и update_client_data —
    // сериализованы: либо до старта воркера, либо в нити воркера, как в эталонных
    // xf/wlf-клиентах.
    //
    // ag-32m: сборка списков (рекурсивный обход со stat) — тяжёлая, поэтому НЕ
    // под m_clipMu: иначе GUI-нить, публикующая новый буфер, встала бы на
    // мьютексе и всё равно замерла. Порядок: под коротким мьютексом забрать
    // пути → собрать списки без блокировки → под мьютексом опубликовать
    // дескрипторы (m_clipFileList) и потоки (update_client_data) ВМЕСТЕ: иначе
    // сервер мог бы запросить FileGroupDescriptorW из нового кэша, пока в
    // потоках ещё старый набор (cbClipDataRequest читает кэш под m_clipMu) — и
    // получить байты одного файла под именем другого. Если за время сборки
    // пришло новое копирование, результат устаревает: не публикуем и не снимаем
    // флаг объявления — следующий проход (воркер крутится каждые ≤100 мс)
    // соберёт свежий.
    void pumpClipboard() {
        if (!m_cliprdr) return;
        // Формат-лист ДО MonitorReady — нарушение порядка протокола (и именно так
        // было видно в живом логе: наш список уходил раньше, чем канал готов).
        if (!m_clipReady) return;

        std::vector<std::string> paths;
        bool havePending = false;
        {
            QMutexLocker lk(&m_clipMu);
            if (m_clipFilePending && m_clipFile && m_clipFileReady) {
                paths.swap(m_clipPathsPending);   // «почтовый ящик» пустеет
                m_clipFilePending = false;
                havePending = true;
            }
        }
        std::string uri, fileList;
        bool built = false;
        if (havePending) {
            built = buildUriList(paths, &uri) && buildFileList(paths, &fileList);
            if (!built)
                clipDiag("clip file build dropped: paths=%d (предел/ошибка обхода)",
                         (int)paths.size());
        }
        // Локальные копии пользовательских путей/имён не должны дожить до
        // освобождения памяти: в «почтовом ящике» их уже нет (move), а деструктор
        // чистит только его. Трём после публикации (или отказа).
        auto scrubClipPaths = [&]() {
            for (auto& p : paths) p.assign(p.size(), '\0');
            paths.clear();
            uri.assign(uri.size(), '\0');
            uri.clear();
            fileList.assign(fileList.size(), '\0');
            fileList.clear();
        };

        // Форматы объявляем ровно те, что реально есть в локальном буфере:
        // текст — только при непустом тексте (иначе копирование картинки
        // рекламировало бы пустой CF_UNICODETEXT), DIB — только при картинке,
        // HTML — только при HTML. CF_TEXT — только для чистого ASCII.
        bool haveText = false, haveTextAscii = false;
        bool haveDib = false, haveHtml = false, haveFiles = false;
        bool appliedFiles = false;
        {
            QMutexLocker lk(&m_clipMu);
            if (havePending) {
                if (m_clipFilePending) {
                    // Пока собирали, пришло новое копирование — результат
                    // устарел. Объявление не снимаем: следующий проход разберёт
                    // его вместе со свежими путями.
                    clipDiag("clip file build superseded — отложено");
                    scrubClipPaths();
                    return;
                }
                const int uriLen = (int)uri.size();
                const int listLen = (int)fileList.size();
                const bool ok = built && !uri.empty() &&
                    cliprdr_file_context_update_client_data(
                        m_clipFile, uri.data(), uri.size()) != FALSE;
                m_clipHaveFiles = ok;
                appliedFiles = ok;
                if (ok) m_clipFileList.swap(fileList); // дескрипторы вместе с потоками
                else m_clipFileList.clear();
                clipDiag("clip file pending applied: paths=%d uri=%d list=%d ok=%d",
                         (int)paths.size(), uriLen, listLen, (int)ok);
                scrubClipPaths();
            }
            // Если файлы только что применились, объявить форматы нужно даже при
            // уже снятом m_clipAnnounce: пути могли ждать, пока канал/контекст
            // станет готов (m_clipFileReady), а флаг объявления к тому времени
            // съел более ранний проход.
            if (!m_clipAnnounce.exchange(false) && !appliedFiles) return;
            haveText = m_clipHave;
            haveTextAscii = m_clipTextAscii;
            haveDib = m_clipHaveDib;
            haveHtml = m_clipHaveHtml;
            haveFiles = m_clipHaveFiles;
        }
        // Файловый формат рекламируем только когда контекст реально поднят:
        // иначе сервер запросит содержимое, а отдать его будет некому.
        const bool advertiseFiles = haveFiles && m_clipFile && m_clipFileReady;
        if (!haveText && !haveDib && !haveHtml && !advertiseFiles) {
            // Пустой список не шлём: протокол допускает numFormats=0, но
            // «нет форматов» достаточно выразить молчанием.
            clipDiag("send ClientFormatList: буфер пуст — не объявляем");
            return;
        }
        clipDiag("send ClientFormatList (text=%d ascii=%d dib=%d html=%d files=%d)",
                 (int)haveText, (int)haveTextAscii, (int)haveDib, (int)haveHtml,
                 (int)haveFiles);
        // Файловый контекст сбрасывает состояние перед новым списком (reference:
        // notify_new_client_format_list вызывается ПЕРЕД ClientFormatList; сам
        // список он не шлёт — FileGroupDescriptorW обязан быть в fmts, иначе
        // сервер не узнает о файлах).
        if (advertiseFiles)
            cliprdr_file_context_notify_new_client_format_list(m_clipFile);
        // Не больше 4 записей: (CF_UNICODETEXT [+ CF_TEXT для ASCII]), один
        // «содержательный» формат (DIB или HTML — кэш взаимоисключающий) и
        // FileGroupDescriptorW для файлов.
        CLIPRDR_FORMAT fmts[4];
        memset(fmts, 0, sizeof(fmts));
        UINT32 n = 0;
        if (haveText) {
            fmts[n++].formatId = CF_UNICODETEXT; // formatName не нужен
            // CF_TEXT (ANSI) для ASCII-текста: некоторые серверы/приложения
            // предпочитают его. Не-ASCII не рекламируем никогда — см.
            // onLocalClipChanged/rdp_text_is_ascii (мозаика кодовой страницы).
            if (haveTextAscii && n < 4) fmts[n++].formatId = CF_TEXT;
        }
        if (haveDib && n < 4) fmts[n++].formatId = CF_DIB;
        if (haveHtml && n < 4) {
            // Зарегистрированный формат: id + имя (long format names).
            fmts[n].formatId = kClipHtmlFormatId;
            fmts[n].formatName = const_cast<char*>(kClipHtmlFormatName);
            n++;
        }
        // FileGroupDescriptorW: на проводе — CLIPRDR_FILELIST, который отдаёт
        // cbClipDataRequest из кэша m_clipFileList. Содержимое каждого файла
        // затем читается колбэком cliprdr_file_context (ServerFileContentsRequest).
        // Имя обязательно (CB_USE_LONG_FORMAT_NAMES): без него сервер не узнает
        // зарегистрированный формат.
        if (advertiseFiles && n < 4) {
            fmts[n].formatId = kClipFileListFormatId;
            fmts[n].formatName = const_cast<char*>(kClipFileListFormatName);
            n++;
        }
        CLIPRDR_FORMAT_LIST fl;
        memset(&fl, 0, sizeof(fl));
        fl.common.msgType = CB_FORMAT_LIST;
        fl.numFormats = n;
        fl.formats = fmts;
        m_cliprdr->ClientFormatList(m_cliprdr, &fl);
    }
    // M39: после cliprdr_file_context_init custom занят CliprdrFileContext,
    // поэтому канвас достаём по rdpcontext (реестр g_byCtx). Так же делают
    // X11/Wayland-клиенты (cliprdr_file_context_get_context).
    static RdpCanvas* clipCanvas(CliprdrClientContext* c) {
        return (c && c->rdpcontext) ? ctxCanvas(c->rdpcontext) : nullptr;
    }
    // Возможности сервера приходят ДО MonitorReady (MS-RDPECLIP 2.2.5.1) —
    // запоминаем их в файловом контексте, чтобы current_flags в клиентских
    // возможностях отражал реальную поддержку файлов.
    static UINT cbClipServerCaps(CliprdrClientContext* c, const CLIPRDR_CAPABILITIES* caps) {
        RdpCanvas* self = clipCanvas(c);
        if (!self || !caps) return CHANNEL_RC_OK;
        if (!self->m_clipFile) return CHANNEL_RC_OK;
        if (!cliprdr_file_context_remote_set_flags(self->m_clipFile, 0))
            return ERROR_INTERNAL_ERROR;
        const BYTE* p = (const BYTE*)caps->capabilitySets;
        for (UINT32 i = 0; i < caps->cCapabilitiesSets; i++) {
            const CLIPRDR_CAPABILITY_SET* set = (const CLIPRDR_CAPABILITY_SET*)p;
            if (set->capabilitySetLength < sizeof(CLIPRDR_CAPABILITY_SET))
                break;
            if (set->capabilitySetType == CB_CAPSTYPE_GENERAL) {
                const CLIPRDR_GENERAL_CAPABILITY_SET* gen =
                    (const CLIPRDR_GENERAL_CAPABILITY_SET*)set;
                if (!cliprdr_file_context_remote_set_flags(
                        self->m_clipFile, gen->generalFlags))
                    return ERROR_INTERNAL_ERROR;
            }
            p += set->capabilitySetLength;
        }
        clipDiag("ServerCapabilities: fileFlags=%#x current=%#x",
                 (unsigned)cliprdr_file_context_remote_get_flags(self->m_clipFile),
                 (unsigned)cliprdr_file_context_current_flags(self->m_clipFile));
        return CHANNEL_RC_OK;
    }
    static UINT cbClipMonitorReady(CliprdrClientContext* c, const CLIPRDR_MONITOR_READY*) {
        RdpCanvas* self = clipCanvas(c);
        if (!self) return CHANNEL_RC_OK;
        // Порядок — как у официальных клиентов (X11 и Wayland идентичны):
        // возможности, затем формат-лист. Без CLIP_CAPS сервер на наш список
        // не отвечает вовсе (живой лог: наши ClientFormatList уходили, а
        // ServerFormatList/DataRequest не приходили ни разу).
        clipDiag("MonitorReady: канал согласован — шлём возможности и форматы");
        self->m_clipReady = true;
        self->sendClientCaps();
        self->m_clipAnnounce = true;
        self->pumpClipboard(); // сразу за caps, в той же нити-воркере
        return CHANNEL_RC_OK;
    }
    static UINT cbClipFormatList(CliprdrClientContext* c, const CLIPRDR_FORMAT_LIST* v) {
        if (!c) return CHANNEL_RC_OK;
        // Ответить обязательно — иначе сервер ждёт ответа на свой список.
        CLIPRDR_FORMAT_LIST_RESPONSE r;
        memset(&r, 0, sizeof(r));
        r.common.msgType = CB_FORMAT_LIST_RESPONSE;
        r.common.msgFlags = CB_RESPONSE_OK;
        UINT rc = c->ClientFormatListResponse(c, &r);
        if (!v) return rc;
        clipDiag("ServerFormatList: форматов=%u", (unsigned)v->numFormats);
        // Жадная схема (как Remmina): на изменение буфера сервера сразу просим
        // данные — иначе не узнать содержимое, не умея детектировать вставку в
        // виджете. Приоритет: CF_DIBV5 > CF_DIB > HTML > CF_UNICODETEXT >
        // CF_TEXT. Картинки важнее текста (из картинок DIBV5 полнее: альфа и
        // профиль), HTML важнее plain text (сохраняет форматирование), а
        // CF_UNICODETEXT важнее CF_TEXT (CF_TEXT — ANSI-кодовая страница).
        bool haveV5 = false, haveDib = false;
        bool haveTextUni = false, haveTextAnsi = false, haveHtml = false;
        bool haveServerFiles = false; // сервер→локальный файлы — вне области M39
        UINT32 htmlId = 0;
        for (UINT32 i = 0; i < v->numFormats; i++) {
            const UINT32 id = v->formats[i].formatId;
            const char* name = v->formats[i].formatName;
            // Файловые форматы сервера (CF_HDROP / FileGroupDescriptorW /
            // FileContents) не запрашиваем: приём файлов требует WITH_FUSE +
            // wClipboard (см. BACKLOG M39). Молча пропускаем — текстовые и
            // графические форматы в том же списке обрабатываются как раньше.
            if (id == CF_HDROP ||
                (name && (strcmp(name, "FileGroupDescriptorW") == 0 ||
                          strcmp(name, "FileContents") == 0))) {
                haveServerFiles = true;
                continue;
            }
            if (id == CF_DIBV5) haveV5 = true;
            else if (id == CF_DIB) haveDib = true;
            else if (id == CF_UNICODETEXT) haveTextUni = true;
            else if (id == CF_TEXT) haveTextAnsi = true;
            // «HTML Format» — зарегистрированный формат: сервер вправе прислать
            // СВОЙ числовой id, поэтому опознаём по имени (long format names);
            // id из его списка запоминаем для FormatDataRequest.
            if (name && strcmp(name, kClipHtmlFormatName) == 0) {
                haveHtml = true;
                htmlId = id;
            } else if (id == kClipHtmlFormatId) {
                haveHtml = true; // сервер вернул наш id, но без имени
                htmlId = id;
            }
        }
        if (haveServerFiles)
            clipDiag("ServerFormatList: файловые форматы сервера пропущены "
                     "(сервер→локальный файлы — вне M39: нужен FUSE)");
        int kind = ClipReqNone;
        UINT32 want = 0;
        if (haveV5) { kind = ClipReqDibV5; want = CF_DIBV5; }
        else if (haveDib) { kind = ClipReqDib; want = CF_DIB; }
        else if (haveHtml) { kind = ClipReqHtml; want = htmlId; }
        else if (haveTextUni) { kind = ClipReqTextUni; want = CF_UNICODETEXT; }
        else if (haveTextAnsi) { kind = ClipReqTextAnsi; want = CF_TEXT; }
        if (kind != ClipReqNone) {
            // Формат ответа в PDUs не передаётся — запоминаем и id (диагностика),
            // и КАК разбирать данные: для HTML id сервер выбирает сам, сравнение
            // с константой CF_HTML тут не работает.
            RdpCanvas* self = clipCanvas(c);
            if (self) {
                self->m_clipReqFmt = want;
                self->m_clipReqKind = kind;
            }
            clipDiag("ServerFormatList: запрашиваем fmt=%u kind=%d",
                     (unsigned)want, kind);
            CLIPRDR_FORMAT_DATA_REQUEST q;
            memset(&q, 0, sizeof(q));
            q.common.msgType = CB_FORMAT_DATA_REQUEST;
            q.requestedFormatId = want;
            rc = c->ClientFormatDataRequest(c, &q);
        }
        return rc;
    }
    static UINT cbClipDataRequest(CliprdrClientContext* c, const CLIPRDR_FORMAT_DATA_REQUEST* d) {
        if (!c) return CHANNEL_RC_OK;
        RdpCanvas* self = clipCanvas(c);
        std::string data;
        bool ok = false;
        clipDiag("ServerFormatDataRequest: fmt=%u (локальный→сервер)",
                 (unsigned)(d ? d->requestedFormatId : 0));
        if (self && d && d->requestedFormatId == CF_UNICODETEXT) {
            QMutexLocker lk(&self->m_clipMu);
            if (self->m_clipHave && !self->m_clipUtf16.empty()) {
                data = self->m_clipUtf16; // копия: буфер живёт до конца вызова
                ok = true;
            }
        } else if (self && d && d->requestedFormatId == CF_TEXT) {
            // CF_TEXT — ANSI/системная кодовая страница. Рекламируем его только
            // для ASCII, поэтому Latin-1-кодирование здесь точное; для не-ASCII
            // (если сервер всё же спросит) отвечаем FAIL, а не мозаикой.
            QMutexLocker lk(&self->m_clipMu);
            if (self->m_clipHave && self->m_clipTextAscii &&
                !self->m_clipUtf16.empty()) {
                data = latin1NulFromUtf16(self->m_clipUtf16);
                ok = !data.empty();
            }
        } else if (self && d && d->requestedFormatId == CF_DIB) {
            QMutexLocker lk(&self->m_clipMu);
            if (self->m_clipHaveDib && !self->m_clipDib.empty()) {
                data = self->m_clipDib; // голый DIB (без BITMAPFILEHEADER)
                ok = true;
            }
        } else if (self && d && (d->requestedFormatId == kClipFileListFormatId ||
                                 d->requestedFormatId == CF_HDROP)) {
            // Файлы: отдаём упакованный CLIPRDR_FILELIST (cItems +
            // FILEDESCRIPTORW), собранный при копировании. CF_HDROP здесь лишь
            // запасной id на случай, если сервер транслирует имя формата в
            // стандартный номер; рекламируем мы FileGroupDescriptorW. Содержимое
            // каждого файла сервер затем тянет через FILECONTENTS_REQUEST — его
            // обслуживает колбэк, установленный cliprdr_file_context_init.
            QMutexLocker lk(&self->m_clipMu);
            if (self->m_clipFileReady && self->m_clipHaveFiles &&
                !self->m_clipFileList.empty()) {
                data = self->m_clipFileList;
                ok = true;
            }
        } else if (self && d) {
            // HTML Format. Числовой id зарегистрированного формата назначает
            // сервер (Windows регистрирует имя у себя), поэтому совпадения с
            // kClipHtmlFormatId может не быть: стандартные id отсеяны выше, и
            // при наличии HTML в кэше отвечаем на любой неизвестный запрос.
            // Файловый формат (FileGroupDescriptorW) обработан выше; FileContents
            // отдельным форматом серверу не рекламируется.
            QMutexLocker lk(&self->m_clipMu);
            if (self->m_clipHaveHtml && !self->m_clipHtml.empty()) {
                data = self->m_clipHtml; // готовый CF_HTML payload
                ok = true;
            }
        }
        CLIPRDR_FORMAT_DATA_RESPONSE r;
        memset(&r, 0, sizeof(r));
        r.common.msgType = CB_FORMAT_DATA_RESPONSE;
        // На КАЖДЫЙ запрос обязан быть ответ — иначе сервер зависает.
        r.common.msgFlags = ok ? CB_RESPONSE_OK : CB_RESPONSE_FAIL;
        r.common.dataLen = ok ? (UINT32)data.size() : 0;
        r.requestedFormatData = ok ? (BYTE*)data.data() : nullptr;
        return c->ClientFormatDataResponse(c, &r);
    }
    static UINT cbClipDataResponse(CliprdrClientContext* c, const CLIPRDR_FORMAT_DATA_RESPONSE* r) {
        RdpCanvas* self = clipCanvas(c);
        clipDiag("ServerFormatDataResponse: flags=%#x len=%u (сервер→локальный)",
                 (unsigned)(r ? r->common.msgFlags : 0), (unsigned)(r ? r->common.dataLen : 0));
        if (!self) return CHANNEL_RC_OK;
        // Формат в ответе не передаётся — берём запомненный при ServerFormatList.
        // Для зарегистрированного «HTML Format» числовой id назначает сервер,
        // поэтому вид запоминается отдельно; 0/None — ответ без запроса,
        // трактуем как текст, как было до картинок/HTML.
        const UINT32 fmt = self->m_clipReqFmt.exchange(0);
        const int kind0 = self->m_clipReqKind.exchange(ClipReqNone);
        if (!r || !r->requestedFormatData || r->common.dataLen == 0) return CHANNEL_RC_OK;
        if (!(r->common.msgFlags & CB_RESPONSE_OK)) return CHANNEL_RC_OK;
        int kind = kind0;
        if (kind == ClipReqNone)
            kind = (fmt == CF_DIB || fmt == CF_DIBV5) ? ClipReqDib : ClipReqTextUni;
        if (kind == ClipReqDib || kind == ClipReqDibV5) {
            const UINT32 len = r->common.dataLen;
            // QImage::fromData принимает int — защищаемся от переполнения.
            if ((uint64_t)len + 14ULL > 0x7FFFFFFFULL) {
                clipDiag("ServerFormatDataResponse: DIB слишком велик (%u)", (unsigned)len);
                return CHANNEL_RC_OK;
            }
            // На проводе — ГОЛЫЙ DIB; QImage::fromData("BMP") ждёт файл. Собираем
            // 14-байтный BITMAPFILEHEADER чистым C-хелпером (тестируется из Go).
            std::vector<uint8_t> bmp((size_t)14 + len);
            const uint32_t n = rdp_bmp_from_dib(r->requestedFormatData, len,
                                                bmp.data(), (uint32_t)bmp.size());
            if (n == 0) {
                clipDiag("ServerFormatDataResponse: DIB отвергнут (fmt=%u len=%u)",
                         (unsigned)fmt, (unsigned)len);
                return CHANNEL_RC_OK;
            }
            const QImage img = QImage::fromData(bmp.data(), (int)n, "BMP");
            if (img.isNull()) {
                clipDiag("ServerFormatDataResponse: QImage::fromData(BMP) = null");
                return CHANNEL_RC_OK;
            }
            clipDiag("ServerFormatDataResponse: картинка %dx%d (fmt=%u)",
                     img.width(), img.height(), (unsigned)fmt);
            // В Qt-буфер — только через invokeMethod с контекстом `this` (колбэк
            // приходит в нити-воркере; вызов отменяется, если канвас уничтожен).
            QMetaObject::invokeMethod(self, [self, img] {
                QClipboard* cb = QGuiApplication::clipboard();
                if (!cb) return;
                // Хэш содержимого: dataChanged на наш setImage не должен объявить
                // ту же картинку обратно серверу (см. onLocalClipChanged).
                // Поле трогается только из Qt-нити — мьютекс не нужен.
                self->m_lastRemoteImageHash = imageHash(img);
                self->m_lastRemote.clear(); // актуальное эхо — картинка, не текст
                self->m_lastRemoteHtmlHash = 0;
                cb->setImage(img);
            }, Qt::QueuedConnection);
            return CHANNEL_RC_OK;
        }
        if (kind == ClipReqHtml) {
            const UINT32 len = r->common.dataLen;
            uint32_t fs = 0, fe = 0;
            // Разбор CF_HTML — чистый C-хелпер (тестируется из Go): заголовок
            // (байтовые StartFragment/EndFragment) → маркеры → весь payload.
            if (!rdp_cf_html_parse(r->requestedFormatData, len, &fs, &fe) ||
                fs > fe || fe > len) {
                clipDiag("ServerFormatDataResponse: CF_HTML не разобран (len=%u)",
                         (unsigned)len);
                return CHANNEL_RC_OK;
            }
            const uint64_t n = (uint64_t)fe - (uint64_t)fs;
            // QString::fromUtf8 принимает int.
            if (n > (uint64_t)0x7FFFFFFFULL) {
                clipDiag("ServerFormatDataResponse: HTML слишком велик (%u)", (unsigned)len);
                return CHANNEL_RC_OK;
            }
            const QString html = QString::fromUtf8(
                (const char*)r->requestedFormatData + fs, (int)n);
            if (html.isEmpty()) {
                clipDiag("ServerFormatDataResponse: пустой HTML-фрагмент");
                return CHANNEL_RC_OK;
            }
            clipDiag("ServerFormatDataResponse: HTML %u байт из payload %u (fmt=%u)",
                     (unsigned)n, (unsigned)len, (unsigned)fmt);
            QMetaObject::invokeMethod(self, [self, html] {
                QClipboard* cb = QGuiApplication::clipboard();
                if (!cb) return;
                // QMimeData с обоими представлениями: HTML для rich-text
                // приложений и plain text для остальных. QTextDocument разбирает
                // фрагмент либо целый документ и даёт plain text.
                QTextDocument doc;
                doc.setHtml(html);
                const QString plain = doc.toPlainText();
                // Эхо: dataChanged на наш setMimeData гасится хэшем HTML
                // (см. onLocalClipChanged). Только Qt-нить — мьютекс не нужен.
                self->m_lastRemote = plain;
                self->m_lastRemoteHtmlHash = htmlHash(html);
                self->m_lastRemoteImageHash = 0;
                QMimeData* mime = new QMimeData();
                mime->setHtml(html);
                mime->setText(plain);
                cb->setMimeData(mime); // владение переходит QClipboard
            }, Qt::QueuedConnection);
            return CHANNEL_RC_OK;
        }
        // CF_TEXT (ANSI/Latin-1, CRLF) или CF_UNICODETEXT (UTF-16LE, CRLF).
        // CF_TEXT мы запрашиваем только если на сервере не было Unicode-варианта;
        // декодируем Latin-1 — см. комментарий про кодовую страницу ниже.
        const QString s = (kind == ClipReqTextAnsi)
            ? fromLatin1Bytes(r->requestedFormatData, r->common.dataLen)
            : fromUtf16Bytes(r->requestedFormatData, r->common.dataLen);
        QMetaObject::invokeMethod(self, [self, s] {
            QClipboard* cb = QGuiApplication::clipboard();
            if (!cb) return;
            // Запомнили, что этот текст пришёл от сервера: dataChanged на него
            // будет проигнорирован (сверка содержимого в onLocalClipChanged).
            // Поле трогается только из Qt-нити — мьютекс не нужен.
            self->m_lastRemote = s;
            self->m_lastRemoteImageHash = 0; // прошлое эхо-изображение неактуально
            self->m_lastRemoteHtmlHash = 0;
            cb->setText(s);
        }, Qt::QueuedConnection);
        return CHANNEL_RC_OK;
    }
    // CF_UNICODETEXT на проводе использует CRLF (формат Windows-буфера), а Qt
    // на Unix хранит в тексте LF. Без нормализации одиночный перевёл строки
    // уезжал на сервер как LF (в Windows-приложениях строки склеивались), а
    // обратно приезжал CRLF и оставался в локальном буфере. Канон — Remmina
    // (lf2crlf/crlf2lf). Сначала сводим любые готовые CRLF к LF, затем LF→CRLF:
    // так уже-виндовый текст не превращается в CRCRLF (идемпотентно).
    static QString lfToCrlf(const QString& s) {
        QString out = s;
        out.replace(QStringLiteral("\r\n"), QStringLiteral("\n"));
        out.replace(QStringLiteral("\n"), QStringLiteral("\r\n"));
        return out;
    }
    static QString crlfToLf(const QString& s) {
        QString out = s;
        out.replace(QStringLiteral("\r\n"), QStringLiteral("\n"));
        return out;
    }
    // QString → UTF-16 + завершающий NUL — формат CF_UNICODETEXT на проводе.
    // (Ревью MINOR-3: это порядок ХОСТА, а не обязательно LE — на проводе нужен
    // UTF-16LE, и на всех наших платформах x86_64/aarch64 хост и есть LE; на BE
    // потребовался бы bswap. Имя поэтому без «le».)
    static std::string utf16Nul(const QString& s) {
        const QString wire = lfToCrlf(s); // локальный LF → CRLF провода
        const qsizetype n = wire.size();
        std::string out((size_t)(n + 1) * 2, '\0');
        if (n > 0) memcpy(out.data(), wire.utf16(), (size_t)n * 2);
        return out; // хвостовые 2 байта — NUL (строка уже нулевая)
    }
    static QString fromUtf16Bytes(const BYTE* data, UINT32 len) {
        size_t units = (size_t)len / 2;
        if (units == 0) return QString();
        std::u16string tmp(units, u'\0');
        memcpy(tmp.data(), data, units * 2); // через копию: выравнивание data не гарантировано
        if (tmp[units - 1] == 0) units--;    // срезать завершающий NUL
        const QString s = QString::fromUtf16((const char16_t*)tmp.data(), (qsizetype)units);
        return crlfToLf(s); // CRLF провода → локальный LF
    }
    // CF_TEXT на проводе — однобайтовая строка ANSI-кодовой страницы Windows.
    // Точная кодовая страница сервера нам неизвестна (GetACP); межъязыково
    // безопасен только ASCII, поэтому CF_TEXT и рекламируется лишь для него.
    // На приёме декодируем Latin-1: для ASCII это байт-в-байт, для расширенных
    // байтов — предсказуемые символы без потери данных (cp1251-текст придёт
    // «кракозябрами», но Unicode-путь используется всегда, когда сервер его
    // предлагает). Осознанный компромисс: определять кодовую страницу сервера
    // по локали мы не можем.
    static QString fromLatin1Bytes(const BYTE* data, UINT32 len) {
        size_t n = (size_t)len;
        while (n > 0 && data[n - 1] == 0) n--; // завершающий NUL провода
        if (n == 0) return QString();
        const QString s = QString::fromLatin1((const char*)data, (qsizetype)n);
        return crlfToLf(s); // CRLF провода → локальный LF
    }
    // UTF-16 (на проводе/хосте LE) + NUL → однобайтовый CF_TEXT. Годится только
    // для ASCII (CF_TEXT рекламируется лишь тогда); не-ASCII → пустая строка,
    // чтобы не отправить мозаику (вызывающий ответит CB_RESPONSE_FAIL).
    static std::string latin1NulFromUtf16(const std::string& u16) {
        std::string out;
        out.reserve(u16.size() / 2 + 1);
        for (size_t i = 0; i + 1 < u16.size(); i += 2) {
            const uint16_t u = (uint16_t)((uint8_t)u16[i] |
                                          ((uint16_t)(uint8_t)u16[i + 1] << 8));
            if (u == 0) break;
            if (u > 0x7F) return std::string();
            out.push_back((char)u);
        }
        out.push_back('\0');
        return out;
    }

    // --- M41: графический конвейер (EGFX) ---
    // На «голом» пути обработчики EGFX никто не ставит: gdi_graphics_pipeline_init
    // зовётся ТОЛЬКО из обработчика ChannelConnected, на который подписываются
    // официальные клиенты (client/X11/xf_client.c, client/Wayland/wlfreerdp.c) —
    // а мы идём через freerdp_new() и своего обработчика не имели. Без него
    // включённый SupportGraphicsPipeline = сервер переводит вывод в EGFX и шлёт
    // PDU в никуда. Проверено вживую на EGFX-сервере (ревью M41): без обработчиков
    // приходит ПУСТОЙ кадр и ноль обновлений (в виджете — чёрный канвас), с
    // обработчиками картинка доезжает. Поэтому M40 и гасил пайплайн.
    //
    // ВАЖНО про нить (ревью M41 D1, проверено): rdpgfx — это DVC, его создаёт
    // СЕРВЕР (CREATE_REQUEST_PDU), и разбирается он в ОТДЕЛЬНОЙ нити drdynvc
    // (FreeRDP_SynchronousDynamicChannels по умолчанию FALSE). Поэтому и этот
    // обработчик, и onEndPaint под GFX исполняются НЕ на нити-воркере — общие
    // поля обязаны быть атомарными либо под мьютексом.
    static void cbChannelConnected(void* ctx, const ChannelConnectedEventArgs* e) {
        rdpContext* context = (rdpContext*)ctx;
        RdpCanvas* self = context ? ctxCanvas(context) : nullptr;
        if (!e || !e->name) return;
        // Печатаем ВСЕ каналы: по списку видно, предложил ли сервер EGFX
        // (Microsoft::Windows::RDS::Graphics) и что ещё он умеет.
        clipDiag("[chan] %s pInterface=%p", e->name, e->pInterface);
        if (!self) return;
        if (strcmp(e->name, RDPGFX_DVC_CHANNEL_NAME) == 0) {
            {
                std::lock_guard<std::mutex> lk(self->m_gfxMu);
                self->m_gfxIface = (RdpgfxClientContext*)e->pInterface;
            }
            self->tryInitGraphicsPipeline(context); // gdi может быть ещё не готов
        }
        if (strcmp(e->name, DISP_CHANNEL_NAME) == 0) {
            // Канал DisplayControl (динамическое разрешение). custom у disp занят
            // НЕ плагином — клиент хранит там себя (xf_disp.c:560, xf_disp_init);
            // он нужен, чтобы достать this из колбэка caps. Хук caps и контекст —
            // только с галкой «/dynamic-resolution» (как xf_disp.c:562-564).
            DispClientContext* disp = (DispClientContext*)e->pInterface;
            if (disp) {
                // Ревью C (улучшение 1): хук и custom под тем же m_dispMu, что
                // и публикация m_disp — «контекст опубликован» = полностью готов.
                std::lock_guard<std::mutex> lk(self->m_dispMu);
                disp->custom = self;
                if (self->m_flags & RDP_FLAG_DYNAMIC_RESOLUTION)
                    disp->DisplayControlCaps = &RdpCanvas::cbDispCaps;
                self->m_disp = disp;
                self->m_dispReady = false; // ждём DisplayControlCaps от сервера
            }
        }
    }
    // Разрыв канала disp: обнуляем контекст под тем же мьютексом, под которым
    // worker его читает (drainResize), — иначе SendMonitorLayout ушел бы в
    // освобождённый контекст. Как xf_disp_uninit (client/X11/xf_disp.c:581).
    static void cbChannelDisconnected(void* ctx, const ChannelDisconnectedEventArgs* e) {
        rdpContext* context = (rdpContext*)ctx;
        RdpCanvas* self = context ? ctxCanvas(context) : nullptr;
        if (!e || !e->name || !self) return;
        if (strcmp(e->name, RDPGFX_DVC_CHANNEL_NAME) == 0) {
            // M41-остаток (BACKLOG): разрыв GFX-канала (reconnect/редирект)
            // освобождает контекст — сбрасываем флаги, иначе gdi->gfx висел бы
            // на освобождённом RdpgfxClientContext. Это место — ИМЕННО
            // Disconnected: раньше блок стоял в cbChannelConnected и занулял
            // m_gfxIface сразу после установки, из-за чего последующий
            // tryInitGraphicsPipeline (после gdi_init) выходил по !m_gfxIface
            // и EGFX-обработчики не вставали (чёрный канвас), а сброса при
            // разрыве не было вовсе.
            std::lock_guard<std::mutex> lk(self->m_gfxMu);
            self->m_gfxActive = false;
            self->m_gfxIface = nullptr;
        }
        if (strcmp(e->name, DISP_CHANNEL_NAME) == 0) {
            std::lock_guard<std::mutex> lk(self->m_dispMu);
            if (self->m_disp) self->m_disp->custom = nullptr;
            self->m_disp = nullptr;
            self->m_dispReady = false;
        }
    }
    // Сервер прислал пределы DisplayControl (MS-RDPEDISP 2.2.2.3) — с этого момента
    // он принимает layout-запросы (xf_disp в этот момент ставит activated=TRUE,
    // xf_disp.c:538). Сами пределы — только в диагностику: кламп размера делаем
    // протокольными константами 200..8192 (см. rdp_disp_resize_target).
    static UINT cbDispCaps(DispClientContext* c, UINT32 maxNumMonitors,
                           UINT32 maxMonitorAreaFactorA, UINT32 maxMonitorAreaFactorB) {
        RdpCanvas* self = c ? (RdpCanvas*)c->custom : nullptr;
        if (!self) return CHANNEL_RC_OK;
        clipDiag("[disp] caps: maxMonitors=%u areaA=%u areaB=%u — layout принимаются",
                 (unsigned)maxNumMonitors, (unsigned)maxMonitorAreaFactorA,
                 (unsigned)maxMonitorAreaFactorB);
        std::lock_guard<std::mutex> lk(self->m_dispMu);
        self->m_dispReady = true;
        return CHANNEL_RC_OK;
    }
    // Идемпотентно: зовётся и из обработчика канала, и после gdi_init — какой из
    // них случится раньше, зависит от сервера (DVC-канал подключается внутри
    // freerdp_connect, а gdi_init у нас после возврата из connect).
    void tryInitGraphicsPipeline(rdpContext* context) {
        // m_gfxMu — потому что зовут ДВЕ нити: async drdynvc (ChannelConnected)
        // и воркер после gdi_init. Без блокировки сброс из Disconnected мог
        // обнулить m_gfxIface посреди gdi_graphics_pipeline_init.
        std::lock_guard<std::mutex> lk(m_gfxMu);
        if (m_gfxActive || !m_gfxIface || !context || !context->gdi) return;
        if (!gdi_graphics_pipeline_init(context->gdi, m_gfxIface)) {
            clipDiag("[gfx] gdi_graphics_pipeline_init FAILED");
            return; // попробуем ещё раз, когда появится gdi / следующий канал
        }
        m_gfxActive = true;
        clipDiag("[gfx] gdi_graphics_pipeline_init OK — EGFX-обработчики встали");
    }
    // Обёртки над обработчиками, которые поставил gdi_init: считаем трафик,
    // чтобы по живому логу отличить сырые bitmap'ы от сжатых/GFX. Сами обработчики
    // вызываем ВСЕГДА — счётчики не должны менять поведение.
    static BOOL cbBitmapUpdate(rdpContext* c, const BITMAP_UPDATE* u) {
        RdpCanvas* self = c ? ctxCanvas(c) : nullptr;
        if (self && u) {
            self->m_dgBmUpdates++;
            self->m_dgBmRects += u->number;
            for (UINT32 i = 0; i < u->number; i++) {
                self->m_dgBmBytes += u->rectangles[i].bitmapLength;
                if (u->rectangles[i].compressed) self->m_dgBmCompressed++;
                self->m_dgLastBpp = u->rectangles[i].bitsPerPixel;
            }
        }
        pBitmapUpdate saved = self ? self->m_savedBitmapUpdate : nullptr;
        return saved ? saved(c, u) : TRUE;
    }
    static BOOL cbSurfaceBits(rdpContext* c, const SURFACE_BITS_COMMAND* cmd) {
        RdpCanvas* self = c ? ctxCanvas(c) : nullptr;
        if (self && cmd) {
            self->m_dgSbCmds++;
            self->m_dgSbBytes += cmd->bmp.bitmapDataLength;
        }
        pSurfaceBits saved = self ? self->m_savedSurfaceBits : nullptr;
        return saved ? saved(c, cmd) : TRUE;
    }
    // Печать прироста за ~2 с (цикл просыпается не реже 100 мс). Печатаем ПРИРОСТ,
    // а не абсолют: иначе по логу не видно, стало ли лучше.
    void diagTick() {
        if (++m_dgTick < 20) return;
        m_dgTick = 0;
        inDiagTick(); // M41: границы ввода — тот же такт
        if (!getenv("ASBRU_RDPCLIP_LOG")) {
            m_dgBmUpdates = m_dgBmRects = m_dgBmBytes = m_dgBmCompressed = 0;
            m_dgSbCmds = m_dgSbBytes = m_dgEndPaint = 0;
            return;
        }
        // .load() явно: атомики нельзя отдавать в вариативную функцию (копия запрещена)
        clipDiag("[perf2s] gfxa=%d EndPaint=%llu | bitmap %llu upd / %llu rects / %llu KiB (сжатых %llu) "
                 "| surface %llu cmds / %llu KiB | bpp %d",
                 (int)m_gfxActive.load(), (unsigned long long)m_dgEndPaint.load(),
                 (unsigned long long)m_dgBmUpdates.load(), (unsigned long long)m_dgBmRects.load(),
                 (unsigned long long)(m_dgBmBytes.load() / 1024), (unsigned long long)m_dgBmCompressed.load(),
                 (unsigned long long)m_dgSbCmds.load(), (unsigned long long)(m_dgSbBytes.load() / 1024),
                 m_dgLastBpp.load());
        m_dgBmUpdates = m_dgBmRects = m_dgBmBytes = m_dgBmCompressed = 0;
        m_dgSbCmds = m_dgSbBytes = m_dgEndPaint = 0;
    }
    // M41: печать границ ввода. ПРАВИЛО ЧТЕНИЯ (уточнено ревью M42):
    //  * «наш ввод чист» = qt.press ≈ очередь.down ≈ отправлено.down И sentFail=0;
    //    сравнивать по СЕРИИ строк, а не по одной — счётчики обнуляются по
    //    отдельности, и событие между двумя .load() уедет в соседнее окно (±1);
    //  * dupDown НЕ признак дублей: dupDown == autorep — это ЗДОРОВАЯ подпись
    //    легитимного автоповтора Qt (удержание клавиши);
    //  * strayUp может быть и у клавиши без сканкода/печатного text — читать
    //    вместе с очередью, а не само по себе;
    //  * sentFail > 0 означает, что библиотека НЕ отправила (например сервер не
    //    объявил unicode-ввод) — вот тогда «отправлено» ничего не значит.
    void inDiagTick() {
        const uint64_t qp = m_inQtPress.load(), qr = m_inQtRelease.load();
        const uint64_t pd = m_inPushDown.load(), pu = m_inPushUp.load();
        const uint64_t sd = m_inSentDown.load(), su = m_inSentUp.load();
        if (!qp && !qr && !pd && !pu && !sd && !su) return; // ничего не печатаем в тишине
        clipDiag("[in2s] qt: press=%llu rel=%llu autorep=%llu | очередь: down=%llu up=%llu | "
                 "отправлено: down=%llu up=%llu (uni=%llu scan=%llu repeatFlag=%llu fail=%llu) | "
                 "подозр.: dupDown=%llu(=autorep — норма) strayUp=%llu",
                 (unsigned long long)qp, (unsigned long long)qr,
                 (unsigned long long)m_inQtRepeat.load(),
                 (unsigned long long)pd, (unsigned long long)pu,
                 (unsigned long long)sd, (unsigned long long)su,
                 (unsigned long long)m_inSentUni.load(), (unsigned long long)m_inSentScan.load(),
                 (unsigned long long)m_inSentRepeatFlag.load(),
                 (unsigned long long)m_inSentFail.load(),
                 (unsigned long long)m_inDupDown.load(), (unsigned long long)m_inStrayUp.load());
        m_inQtPress = m_inQtRelease = m_inQtRepeat = 0;
        m_inPushDown = m_inPushUp = 0;
        m_inSentDown = m_inSentUp = 0;
        m_inSentUni = m_inSentScan = m_inSentRepeatFlag = m_inSentFail = 0;
        m_inDupDown = m_inStrayUp = 0;
    }

    // --- worker: весь жизненный цикл RDP-клиента ---
    void run() {
        // Ревью MAJOR-1: между стартом потока и его планировщиком вкладку могли
        // закрыть (requestStop → m_stop). Проверяем ДО freerdp_new/connect —
        // иначе закрытие вкладки всё равно тянуло полное рукопожатие.
        if (m_stop) { done(0, ""); return; }
        // Тихий WLog: INFO-шум (gdi_init/VAAPI) не льётся в консоль; WARN/ERROR
        // (сертификат, Kerberos) остаются. Без этого лог подключения уходит в
        // запускающий терминал (konsole), а не в приложение (user-тест наблюд.).
        WLog_SetLogLevel(WLog_GetRoot(), WLOG_WARN);
        freerdp* instance = freerdp_new();
        if (!instance) { done(0, "freerdp_new failed"); return; }
        instance->ContextSize = sizeof(rdpContext);
        instance->PostDisconnect = &RdpCanvas::cbPostDisconnect;

        if (!freerdp_context_new(instance)) {
            done(0, "freerdp_context_new failed"); freerdp_free(instance); return;
        }
        rdpContext* context = instance->context;
        rdpSettings* s = context->settings;

        freerdp_settings_set_string(s, FreeRDP_ServerHostname, m_host.c_str());
        freerdp_settings_set_uint32(s, FreeRDP_ServerPort, (UINT32)m_port);
        freerdp_settings_set_string(s, FreeRDP_Username, m_user.c_str());
        // Домен выставляем ТОЛЬКО при непустом значении: пустая строка уходит
        // в NLA как «\пользователь», и сервер отвечает ERRCONNECT_LOGON_FAILURE
        // (очная проба: без домена аутентификация проходит, с /d: — отказ;
        // поэтому «не работал» ни один вариант sec-галок — дело было не в них).
        if (!m_domain.empty()) {
            freerdp_settings_set_string(s, FreeRDP_Domain, m_domain.c_str());
        }
        freerdp_settings_set_string(s, FreeRDP_Password, m_pass.c_str());
        freerdp_settings_set_uint32(s, FreeRDP_DesktopWidth, (UINT32)(m_w > 0 ? m_w : 1024));
        freerdp_settings_set_uint32(s, FreeRDP_DesktopHeight, (UINT32)(m_h > 0 ? m_h : 768));
        freerdp_settings_set_uint32(s, FreeRDP_ColorDepth, (UINT32)(m_bpp > 0 ? m_bpp : 16));
        freerdp_settings_set_bool(s, FreeRDP_EmbeddedWindow, TRUE);      // без своего окна
        freerdp_settings_set_bool(s, FreeRDP_DesktopResize, TRUE);
        // M37: галки редактора реально доезжают (раньше опции игнорировались).
        // Сертификат — семантика xfreerdp: проверяется, ЕСЛИ не задан
        // /cert-ignore (RDP_FLAG_CERT_IGNORE). Раньше здесь стоял жёсткий TRUE:
        // галка «Игнорировать сертификат» была мёртвой, а проверка не работала
        // никогда. У встроенного канваса нет диалога подтверждения сертификата,
        // поэтому self-signed серверы БЕЗ этой галки теперь не подключатся —
        // это сознательный совместимый с xfreerdp безопасный дефолт, а не
        // регресс (пользователь включает /cert-ignore в редакторе).
        // NLA/TLS/RDP включены; галка ВЫКЛЮЧАЕТ свой тип.
        freerdp_settings_set_bool(s, FreeRDP_IgnoreCertificate,
                                  rdp_ignore_certificate(m_flags) ? TRUE : FALSE);
        freerdp_settings_set_bool(s, FreeRDP_NlaSecurity, !(m_flags & RDP_FLAG_NO_NLA));
        freerdp_settings_set_bool(s, FreeRDP_TlsSecurity, !(m_flags & RDP_FLAG_NO_TLS));
        freerdp_settings_set_bool(s, FreeRDP_RdpSecurity, !(m_flags & RDP_FLAG_NO_RDP));
        if (m_flags & RDP_FLAG_NO_AUTH)
            freerdp_settings_set_bool(s, FreeRDP_Authentication, FALSE);
        if (m_flags & RDP_FLAG_NO_FAST_PATH) {
            freerdp_settings_set_bool(s, FreeRDP_FastPathInput, FALSE);
            freerdp_settings_set_bool(s, FreeRDP_FastPathOutput, FALSE);
        }
        // M40: набор кодеков и конвейер отрисовки — как у эталонных клиентов.
        // БЕЗ этого FreeRDP пишет bitmapCodecCount=0 (libfreerdp/core/capabilities.c
        // rdp_write_bitmap_codecs_capability_set — счётчик растёт только при
        // RemoteFxCodec/NSCodec/JpegCodec/RemoteFxImageCodec), и сервер ОБЯЗАН слать
        // СЫРЫЕ несжатые bitmap'ы: для 3040x2080 это ~24 МиБ на полный экран и
        // десятки-тысячи событий EndPaint на кадр — экран «наливается полосами
        // сверху вниз» (живой симптом пользователя). Плюс канал RDPGFX грузится
        // ТОЛЬКО при SupportGraphicsPipeline=TRUE (client/common/cmdline.c,
        // таблица dynChannels), а без GFX EndPaint зовётся на каждое
        // fastpath-обновление, а не раз на кадр.
        // Так же делают эталонные клиенты: xfreerdp (cmdline.c:1486) и KRDC —
        // в дизассемблере krdc_rdpplugin.so (RdpSession::clientContextStart) он
        // ставит SupportGraphicsPipeline/GfxH264/GfxAVC444/GfxAVC444v2 = TRUE.
        // Именно поэтому на том же сервере KRDC гладкий, а мы были нет.
        // Откат без пересборки, если конкретный сервер не поладит с GFX/H.264:
        // ASBRU_RDP_NO_GFX=1 (тогда поведение прежнее — сырые bitmap'ы).
        const char* noGfx = getenv("ASBRU_RDP_NO_GFX");
        if (!(noGfx && *noGfx && strcmp(noGfx, "0") != 0)) {
            freerdp_set_connection_type(s, CONNECTION_TYPE_AUTODETECT);
            // M41: пайплайн НЕ гасим — обработчики EGFX теперь есть (см.
            // cbChannelConnected/tryInitGraphicsPipeline): подписка на
            // ChannelConnected ставится в run() ДО connect, а gdi_init выполняется
            // до первой возможной доставки DVC-события. Именно этот флаг —
            // единственное расхождение наших настроек с xfreerdp: он ЗАГРУЖАЕТ
            // канал rdpgfx (таблица dynChannels в cmdline.c), а канал — условие
            // того, что сервер вообще переведёт вывод в EGFX (замерено: сырой
            // кадр 3040x2080 = 202 Мбит ≈ 215 мс на 1 Гбит/с, RFX — 4.9 Мбит,
            // AVC444 — 1.4-2.7 Мбит; отсюда «полосы сверху вниз»).
            // Без обработчиков EGFX (состояние до M41) этот флаг давал ЧЁРНЫЙ
            // экран — поэтому раньше гасили; теперь гасить нечего.
            // Откат без пересборки: ASBRU_RDP_NO_GFX=1 (см. ветку else).
        } else {
            clipDiag("connection type: НЕ тронут (ASBRU_RDP_NO_GFX)");
        }
        if (m_flags & RDP_FLAG_RFX)
            freerdp_settings_set_bool(s, FreeRDP_RemoteFxCodec, TRUE);
        if (m_flags & RDP_FLAG_NSC)
            freerdp_settings_set_bool(s, FreeRDP_NSCodec, TRUE);
        // Диагностика кодеков печатается ПОСЛЕ connect (ревью M41 #3): здесь
        // значения — только НАМЕРЕНИЕ, сервер перезапишет их при согласовании
        // (например, без CAPSET_TYPE_BITMAP_CODECS в Demand Active FreeRDP
        // принудительно гасит RemoteFxCodec — capabilities.c).
        if (m_flags & RDP_FLAG_DYNAMIC_RESOLUTION) {
            // FreeRDP для /dynamic-resolution ставит ОБЕ настройки
            // (cmdline.c parse_dynamic_resolution_options). SupportDisplayControl —
            // ключ, по которому freerdp_client_load_addins (M39) грузит канал disp
            // (client/common/cmdline.c, dynChannels: disp ← SupportDisplayControl);
            // сам запрос разрешения при ресайзе окна шлёт drainResize через
            // DispClientContext::SendMonitorLayout (эталон — client/X11/xf_disp.c).
            freerdp_settings_set_bool(s, FreeRDP_DynamicResolutionUpdate, TRUE);
            freerdp_settings_set_bool(s, FreeRDP_SupportDisplayControl, TRUE);
        }
        if (m_flags & RDP_FLAG_FONT_SMOOTH)
            freerdp_settings_set_bool(s, FreeRDP_AllowFontSmoothing, TRUE);
        if (m_flags & RDP_FLAG_ADMIN)
            freerdp_settings_set_bool(s, FreeRDP_ConsoleSession, TRUE);
        if (m_tlsLevel > 0)
            freerdp_settings_set_uint32(s, FreeRDP_TlsSecLevel, (UINT32)m_tlsLevel);
        if (!m_kbd.empty()) { // M38: раскладка по имени → id (libfreerdp3)
            DWORD kid = freerdp_keyboard_get_layout_id_from_name(m_kbd.c_str());
            if (kid != 0) {
                freerdp_settings_set_uint32(s, FreeRDP_KeyboardLayout, kid);
            } else {
                // Ревью M43 D3: нераспознанное имя раньше молчало (а плейсхолдер в
                // редакторе подсказывал несуществующее «en-us») — теперь видно.
                clipDiag("[kbd] раскладка %s не распознана FreeRDP — остаётся раскладка сервера",
                         m_kbd.c_str());
            }
        } else {
            // Ревью M43 D2: раскладку не задали — объявляем ЛОКАЛЬНУЮ, как xfreerdp.
            // После перехода на сканкодный ввод символ переводит раскладка СЕРВЕРА,
            // поэтому «0 = не трогать» означало бы латиницу вместо кириллицы на
            // сервере с другой раскладкой. Пишем ДО connect: раскладка уходит в
            // Client Core Data, которая формируется внутри freerdp_connect.
            const DWORD kid = freerdp_keyboard_init(0);
            if (kid != 0) {
                freerdp_settings_set_uint32(s, FreeRDP_KeyboardLayout, kid);
                clipDiag("[kbd] раскладка не задана — объявляем локальную %#x", (unsigned)kid);
            }
        }
        // M39: гейт загрузки cliprdr в load_addins — именно этот сеттинг (да, он
        // помечен deprecated как ПОЛЕ структуры, но лоадер и плагин читают его
        // через freerdp_settings_get_*). Ставим явно в ОБЕ стороны: дефолт для
        // клиента — TRUE, поэтому без явного FALSE снятая галка ничего не значила бы.
        freerdp_settings_set_bool(s, FreeRDP_RedirectClipboard, m_clipboard ? TRUE : FALSE);
        // M39: аддины (каналы) — см. ensureAddinProvider/cbLoadChannels.
        ensureAddinProvider();
        // M39: перенаправление диска (/drive:). Диск — устройство RDPDR_DTYP_FILESYSTEM
        // в settings->DeviceArray; канал rdpdr грузит load_addins по
        // DeviceRedirection=TRUE. Отсутствие аддина НЕ должно валить connect
        // (load_addins возвращает FALSE → умирает всё подключение), поэтому
        // наличие rdpdr проверяем ДО выставления DeviceRedirection и при его
        // отсутствии диски просто не объявляем. Невалидный путь (нет/не каталог)
        // пропускаем сами — как freerdp_client_add_drive в cmdline.c.
        if (!m_drives.empty() && !staticAddinAvailable(RDPDR_SVC_CHANNEL_NAME)) {
            clipDiag("[drive] аддин %s недоступен — диски не объявлены, "
                     "connect продолжается", RDPDR_SVC_CHANNEL_NAME);
        } else if (!m_drives.empty()) {
            // Сначала отбираем валидные каталоги: DeviceRedirection включаем
            // только если есть что объявлять.
            std::vector<std::pair<std::string, std::string>> valid;
            for (const auto& d : m_drives) {
                struct stat st;
                if (stat(d.second.c_str(), &st) != 0 || !S_ISDIR(st.st_mode)) {
                    clipDiag("[drive] каталог %s недоступен — шэр %s пропущен",
                             d.second.c_str(), d.first.c_str());
                    continue;
                }
                valid.push_back(d);
            }
            if (valid.empty()) {
                clipDiag("[drive] валидных дисков нет — редирект не включён");
            } else if (!freerdp_settings_set_bool(s, FreeRDP_DeviceRedirection, TRUE)) {
                // Настройку выставить не удалось — диски не объявляем (connect
                // важнее): load_addins без DeviceRedirection rdpdr не грузит.
                clipDiag("[drive] DeviceRedirection: set_bool FAILED — диски пропущены");
            } else {
                for (const auto& d : valid) {
                    const char* args[] = { d.first.c_str(), d.second.c_str() };
                    RDPDR_DEVICE* dev = freerdp_device_new(
                        RDPDR_DTYP_FILESYSTEM, ARRAYSIZE(args), args);
                    if (!dev) {
                        clipDiag("[drive] freerdp_device_new FAILED: шэр %s",
                                 d.first.c_str());
                        continue;
                    }
                    // Успешный collection_add забирает владение; при отказе
                    // освобождаем сами (канон cmdline.c).
                    if (!freerdp_device_collection_add(s, dev)) {
                        freerdp_device_free(dev);
                        clipDiag("[drive] collection_add FAILED: шэр %s", d.first.c_str());
                        continue;
                    }
                    clipDiag("[drive] шэр %s → %s", d.first.c_str(), d.second.c_str());
                }
            }
        }
        // M39: звук (rdpsnd). По умолчанию load_addins объявляет rdpsnd с
        // подсистемой sys:fake — локального звука нет. Это ЛЕГАСИ-ПАРИТЕТ:
        // PACMethod_xfreerdp.pm ставит `/sound:sys:alsa` только при явной
        // настройке, а rdesktop-дефолт — `sound:off`. Поэтому реальную
        // подсистему регистрируем ТОЛЬКО когда Go прислал аргумент канала
        // (пользователь выбрал local в options); иначе не трогаем дефолт.
        // Недоступный аддин/бэкенд НЕ должен валить connect: load_addins на
        // неразрешимом канале возвращает FALSE и умирает ВСЁ подключение
        // (см. cbLoadChannels). Проверяем наличие заранее; при промахе
        // остаётся дефолтный sys:fake.
        if (!m_sound.empty()) {
            if (!staticAddinAvailable(RDPSND_CHANNEL_NAME)) {
                clipDiag("[sound] аддин %s недоступен — звук не включён, "
                         "connect продолжается", RDPSND_CHANNEL_NAME);
            } else if (freerdp_static_channel_collection_find(s, RDPSND_CHANNEL_NAME)) {
                clipDiag("[sound] канал %s уже объявлен — настройка %s пропущена",
                         RDPSND_CHANNEL_NAME, m_sound.c_str());
            } else if (!rdpsndSubsystemAvailable(m_sound.c_str())) {
                clipDiag("[sound] подсистема %s недоступна — остаётся sys:fake, "
                         "connect продолжается", m_sound.c_str());
            } else {
                const char* args[] = { RDPSND_CHANNEL_NAME, m_sound.c_str() };
                if (!freerdp_client_add_static_channel(s, ARRAYSIZE(args), args)) {
                    // add не удался — load_addins добавит дефолтный fake.
                    clipDiag("[sound] add_static_channel FAILED (%s) — остаётся sys:fake",
                             m_sound.c_str());
                } else {
                    clipDiag("[sound] rdpsnd → %s", m_sound.c_str());
                }
            }
        }
        instance->LoadChannels = &cbLoadChannels;

        // M41: подписка на ChannelConnected — ДО connect. Так делают эталонные
        // клиенты, и это обязательное условие: без неё мы не узнаем интерфейс
        // rdpgfx, а без интерфейса gdi_graphics_pipeline_init не позвать →
        // сервер уведёт вывод в EGFX и он уйдёт в никуда (проверено вживую).
        // Статические каналы публикуют событие внутри connect, DVC (rdpgfx) —
        // позже, из отдельной нити drdynvc, уже в цикле событий (ревью M41 D1/D3).
        if (PubSub_SubscribeChannelConnected(context->pubSub, &RdpCanvas::cbChannelConnected) < 0)
            clipDiag("[gfx] PubSub_SubscribeChannelConnected FAILED");
        // Разрыв канала disp (сервер закрыл DVC/terminated): обнулить контекст,
        // пока worker не успел туда отправить (см. cbChannelDisconnected).
        if (PubSub_SubscribeChannelDisconnected(context->pubSub, &RdpCanvas::cbChannelDisconnected) < 0)
            clipDiag("[disp] PubSub_SubscribeChannelDisconnected FAILED");

        m_instance = instance;
        regCtx(context, this);
        { std::lock_guard<std::mutex> lk(m_ctxMu); m_context = context; } // M33-г: abort-connect (ДО connect)

        BOOL connOk = freerdp_connect(instance);
        { std::lock_guard<std::mutex> lk(m_ctxMu); m_context = nullptr; } // connect завершён

        if (!connOk) {
            UINT32 le = freerdp_get_last_error(context);
            char buf[256];
            snprintf(buf, sizeof(buf), "%s", freerdp_get_last_error_string(le) ? freerdp_get_last_error_string(le) : "connect failed");
            done(0, buf);
            unregCtx(context);
            freerdp_context_free(instance); freerdp_free(instance); // gdi не init — без gdi_free
            return;
        }

        // M39: интерфейс канала буфера — ТОЛЬКО после успешного connect:
        // pInterface заполняется в post_connect, до connect вернётся NULL.
        // Указатель живёт до disconnect (при terminated канал освобождает
        // контекст), поэтому обнуляем его сразу после freerdp_disconnect.
        if (m_clipboard) {
            m_cliprdr = (CliprdrClientContext*)freerdp_channels_get_static_channel_interface(
                context->channels, CLIPRDR_SVC_CHANNEL_NAME);
            clipDiag("interface cliprdr=%p (ожидается ненулевой)", (void*)m_cliprdr);
            if (m_cliprdr) {
                // Наши колбэки ставим ДО cliprdr_file_context_init: init
                // перезапишет custom (своим CliprdrFileContext) и четыре
                // файловых колбэка (ServerLock/UnlockClipboardData,
                // ServerFileContentsRequest; Response — только с FUSE), но не
                // тронет эти. Канвас наши колбэки находят через c->rdpcontext.
                m_cliprdr->MonitorReady = &RdpCanvas::cbClipMonitorReady;
                m_cliprdr->ServerCapabilities = &RdpCanvas::cbClipServerCaps;
                m_cliprdr->ServerFormatList = &RdpCanvas::cbClipFormatList;
                m_cliprdr->ServerFormatDataRequest = &RdpCanvas::cbClipDataRequest;
                m_cliprdr->ServerFormatDataResponse = &RdpCanvas::cbClipDataResponse;
                if (m_clipFile) {
                    if (cliprdr_file_context_init(m_clipFile, m_cliprdr))
                        m_clipFileReady = true;
                    else // объект не освобождаем: custom уже мог стать file (см. init)
                        clipDiag("cliprdr_file_context_init FAILED — файлы недоступны");
                }
            }
        }

        // GDI + переопределение update-колбэков — ПОСЛЕ успешного connect
        // (PostConnect, как xfreerdp). freerdp 3.31 создаёт context->cache/update
        // только при успешном connect; gdi_init ДО connect падал на
        // brush_cache_register_callbacks (context->cache == NULL) — M33 live-debug
        // «двойной клик RDP — вылет без ошибки» (SEGV, подтверждён в standalone C).
        if (!gdi_init(instance, PIXEL_FORMAT_BGRX32)) {
            done(0, "gdi_init failed");
            // M39-ревью MINOR-1: freerdp_context_free освободит каналы (и контекст
            // cliprdr вместе с ними) — указатель обязан стать нулевым, иначе он
            // висит на освобождённом объекте до конца жизни вкладки.
            // Файловый контекст держит ссылку на cliprdr в file->context —
            // uninit обязателен, пока канал ещё жив.
            if (m_clipFile && m_cliprdr)
                cliprdr_file_context_uninit(m_clipFile, m_cliprdr);
            m_cliprdr = nullptr;
            unregCtx(context); // M39-ревью MINOR-1: реестр g_byCtx держал бы освобождённый ctx
            freerdp_context_free(instance); freerdp_free(instance);
            return;
        }
        m_savedEndPaint = context->update->EndPaint;
        m_savedDesktopResize = context->update->DesktopResize;
        context->update->EndPaint = &RdpCanvas::cbEndPaint;
        context->update->DesktopResize = &RdpCanvas::cbDesktopResize;
        // M41: обработчики EGFX — gdi уже есть, значит если канал rdpgfx пришёл
        // внутри connect, самое время; если придёт позже — инициализирует
        // cbChannelConnected. Плюс счётчики трафика (обёртки над gdi-обработчиками).
        tryInitGraphicsPipeline(context);
        m_savedBitmapUpdate = context->update->BitmapUpdate;
        context->update->BitmapUpdate = &RdpCanvas::cbBitmapUpdate;
        m_savedSurfaceBits = context->update->SurfaceBits;
        context->update->SurfaceBits = &RdpCanvas::cbSurfaceBits;
        // M41: что сервер РЕАЛЬНО прислал в Demand Active и что осталось после
        // согласования. Без CAPSET_TYPE_BITMAP_CODECS сервер не пришлёт Bitmap
        // Codecs, и FreeRDP принудительно гасит RemoteFx → сырые bitmap'ы.
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wdeprecated-declarations" // ReceivedCapabilities — публичное поле без геттера
        // unicodeInput особенно важен: если сервер не объявил поддержку unicode-ввода,
        // библиотека НЕ отправляет наши unicode-события (WLog_WARN "Unicode input not
        // supported by server"), и счётчики «отправлено» будут врать — выглядеть
        // идеально при том, что на провод не ушло ничего.
        clipDiag("[in] fastpath=%d unicodeInput=%d (fastpath=0 => slow-path, флаги идут как есть)",
                 (int)freerdp_settings_get_bool(s, FreeRDP_FastPathInput),
                 (int)freerdp_settings_get_bool(s, FreeRDP_UnicodeInput));
        clipDiag("[nego] ReceivedCapabilities[BITMAP_CODECS=%#x]=%d SURFACE_COMMANDS=%d | "
                 "после согласования RemoteFx=%d NSC=%d GFX=%d codecsFlags=%#x ColorDepth=%u",
                 0x001D,
                 (int)(s->ReceivedCapabilities ? s->ReceivedCapabilities[0x001D] : -1),
                 (int)(s->ReceivedCapabilities ? s->ReceivedCapabilities[0x001C] : -1),
                 (int)freerdp_settings_get_bool(s, FreeRDP_RemoteFxCodec),
                 (int)freerdp_settings_get_bool(s, FreeRDP_NSCodec),
                 (int)freerdp_settings_get_bool(s, FreeRDP_SupportGraphicsPipeline),
                 (unsigned)freerdp_settings_get_codecs_flags(s),
                 (unsigned)freerdp_settings_get_uint32(s, FreeRDP_ColorDepth));
#pragma GCC diagnostic pop

        bool ok = true;
        while (!m_stop && !freerdp_shall_disconnect_context(context)) {
            drainInput(context);
            drainResize(context); // динамическое разрешение: запрос после ресайза окна
            pumpClipboard(); // M39: локальный буфер → серверу (объявление форматов)
            diagTick();      // M41: прирост трафика раз в ~2 с (только с логом)
            HANDLE handles[64];
            DWORD count = freerdp_get_event_handles(context, handles, 64);
            if (count == 0) break;
            DWORD rc = WaitForMultipleObjects(count, handles, FALSE, 100); // дренить ввод
            if (rc == WAIT_FAILED) { ok = false; break; }
            if (rc == WAIT_TIMEOUT) continue;
            if (!freerdp_check_event_handles(context)) { ok = false; break; }
        }

        // Файловый контекст очищаем, пока канал жив: uninit сбрасывает
        // file->context и локальные потоки (после disconnect контекст канала
        // будет освобождён). Сам объект живёт до деструктора канваса.
        if (m_clipFile && m_cliprdr)
            cliprdr_file_context_uninit(m_clipFile, m_cliprdr);
        freerdp_disconnect(instance);
        m_cliprdr = nullptr; // канал освободил контекст при terminated — не держим висячий
        // disp — то же: контекст канала освобождён вместе с каналами.
        { std::lock_guard<std::mutex> lk(m_dispMu); m_disp = nullptr; m_dispReady = false; }
        done(ok ? 1 : 0, ok ? "" : "disconnected");
        unregCtx(context);
        gdi_free(instance); freerdp_context_free(instance); freerdp_free(instance);
    }

    void drainInput(rdpContext* context) {
        rdpInput* input = context ? context->input : nullptr;
        if (!input) return;
        for (;;) {
            InputMsg m;
            {
                QMutexLocker lk(&m_inMu);
                if (m_inputs.empty()) return;
                m = m_inputs.front(); m_inputs.pop_front();
            }
            if (m.kind == InputMsg::KeyUnicode) {
                // MS-RDPBCGR 2.2.8.1.1.3.1.1.2: у TS_UNICODE_KEYBOARD_EVENT определён
                // ТОЛЬКО KBDFLAGS_RELEASE (0x8000); «нет флага» = нажатие.
                // KBD_FLAGS_DOWN (0x4000) здесь НЕ определён (в TS_KEYBOARD_EVENT он
                // значит «клавиша уже была нажата», т.е. автоповтор) — эталонные
                // клиенты шлют на нажатие 0 (xf_keyboard.c, xf_client.c). Мы раньше
                // слали 0x4000: на fast-path библиотека бит отбрасывает, но на
                // slow-path он уходил бы на провод.
                const uint16_t fl = m.down ? 0 : KBD_FLAGS_RELEASE;
                if (m.down) m_inSentDown++; else m_inSentUp++;
                m_inSentUni++;
                // Считаем и ОТКАЗЫ: библиотека молча не отправляет, если сервер не
                // объявил unicode-ввод (WLog_WARN) или ввод пропущен (suspended).
                // Без этого «отправлено» врёт именно в сценарии «клавиши не доходят».
                if (!freerdp_input_send_unicode_keyboard_event(input, fl, (UINT16)(m.code & 0xFFFF)))
                    m_inSentFail++;
            } else if (m.kind == InputMsg::KeyScancode) {
                if (m.down) m_inSentDown++; else m_inSentUp++;
                m_inSentScan++;
                if (m.repeat) m_inSentRepeatFlag++;
                // repeat — настоящий (Qt isAutoRepeat), как у эталонов. Оговорка: на
                // FAST-PATH у сканкодного события бита «уже нажата» нет вовсе
                // (fastpath.h: только RELEASE/EXTENDED/PREFIX_E1), поэтому там флаг
                // не выразим — и у эталонов тоже; работает он только на slow-path.
                if (!freerdp_input_send_keyboard_event_ex(input, m.down != 0, m.repeat != 0, m.code))
                    m_inSentFail++;
            } else {
                freerdp_input_send_mouse_event(input, m.flags, (UINT16)m.x, (UINT16)m.y);
            }
        }
    }

    // worker: отправить накопленный ресайзом запрос динамического разрешения
    // (слот из resizeEvent). Порядок — как xf_disp_sendResize
    // (client/X11/xf_disp.c:165): канал+caps готовы → «размер не менялся» →
    // throttle 500 мс (RESIZE_MIN_DELAY_NS xf_disp.c:44 — серверу дорого
    // применять каждый промежуточный размер, кадр пропадает на реактивацию) →
    // layout ОДНОГО монитора (канвас у нас один, мультимониторная раскладка
    // xf_disp_sendLayout не воспроизводится). SendMonitorLayout зовём под
    // m_dispMu: исключает гонку с обнулением контекста при разрыве канала.
    void drainResize(rdpContext* context) {
        if (!(m_flags & RDP_FLAG_DYNAMIC_RESOLUTION)) return;
        int w = 0, h = 0;
        {
            QMutexLocker lk(&m_resMu);
            if (!m_resHave) return;
            w = m_resW; h = m_resH;
        }
        if (w == m_dw.load() && h == m_dh.load()) {
            QMutexLocker lk(&m_resMu);
            // Ревью C (Б1): гасим слот ТОЛЬКО если там всё ещё (w,h) — новый
            // запрос, пришедший во время нашего чтения, не теряем.
            if (m_resW == w && m_resH == h) m_resHave = false;
            return;
        }
        std::lock_guard<std::mutex> lk(m_dispMu);
        if (!m_disp || !m_dispReady) return; // канал ещё не готов — запрос ждёт в слоте
        const auto now = std::chrono::steady_clock::now();
        if (now - m_resLastSent < std::chrono::milliseconds(500)) return;
        m_resLastSent = now; // до отправки: и неуспешные попытки не превращаются в шторм
        rdpSettings* st = context ? context->settings : nullptr;
        DISPLAY_CONTROL_MONITOR_LAYOUT layout;
        memset(&layout, 0, sizeof(layout));
        layout.Flags = DISPLAY_CONTROL_MONITOR_PRIMARY;
        layout.Left = 0; layout.Top = 0;
        layout.Width = (UINT32)w;
        layout.Height = (UINT32)h;
        // Ориентация и масштабы — из настроек, как xf_disp.c:211-214 (дефолты
        // freerdp: ORIENTATION_LANDSCAPE, по 100); PhysicalWidth/Height — та же
        // формула «75 dpi» из xf_disp.c:216-219.
        layout.Orientation = st ? freerdp_settings_get_uint16(st, FreeRDP_DesktopOrientation) : 0;
        layout.DesktopScaleFactor =
            st ? freerdp_settings_get_uint32(st, FreeRDP_DesktopScaleFactor) : 100;
        layout.DeviceScaleFactor =
            st ? freerdp_settings_get_uint32(st, FreeRDP_DeviceScaleFactor) : 100;
        const double px = 25.4 / 75.0;
        layout.PhysicalWidth = (UINT32)((double)w * px + 0.5);
        layout.PhysicalHeight = (UINT32)((double)h * px + 0.5);
        // Ревью C (улучшение 2): null-проверка ДО троттла — иначе отсутствие
        // функции съедало троттл-окно впустую.
        if (!m_disp->SendMonitorLayout) return;
        const UINT rc = m_disp->SendMonitorLayout(m_disp, 1, &layout);
        clipDiag("[disp] SendMonitorLayout %ux%u rc=%u (текущий десктоп %dx%d)",
                 (unsigned)w, (unsigned)h, (unsigned)rc, m_dw.load(), m_dh.load());
        if (rc != CHANNEL_RC_OK) return; // слот остаётся — повтор после throttle
        QMutexLocker lk2(&m_resMu);
        // Ревью C (Б1): слот гасим только при совпадении — пришедший во время
        // отправки новый запрос (W2) не теряется вслед за отправленным (W1).
        if (m_resW == w && m_resH == h) m_resHave = false;
    }

    static BOOL cbEndPaint(rdpContext* ctx) {
        RdpCanvas* self = ctxCanvas(ctx);
        return self ? self->onEndPaint(ctx) : TRUE;
    }
    static BOOL cbDesktopResize(rdpContext* ctx) {
        RdpCanvas* self = ctxCanvas(ctx);
        return self ? self->onDesktopResize(ctx) : TRUE;
    }
    static void cbPostDisconnect(freerdp* instance) {
        (void)instance;
    }

    void done(int ok, const char* reason) {
        if (m_reported.exchange(true)) return;
        char buf[256];
        snprintf(buf, sizeof(buf), "%s", reason ? reason : "");
        // M33-г: ошибку рисуем на канвасе только если НИКОГДА не подключались (нет
        // кадра). Если кадр уже был, а соединение оборвалось — оставляем последний
        // кадр (не «схлопываем» в ошибку); статус виден в заголовке вкладки.
        if (!ok && !m_frameReported && reason && reason[0]) setError(reason);
        // Тот же обрыв: кадр остаётся, но ч/б — цветной неотличим от живой сессии
        // при красном индикаторе вкладки (user-report 2026-10-04).
        if (!ok && m_frameReported) grayLastFrame();
        rdpGoDone(m_id, ok, buf);
    }

    // Снапшот последнего кадра в градациях серого (asbru_gray_frame, ../grayframe.h).
    // Снапшот, а НЕ обесцвечивание m_img на месте: setFrameRect пишет в m_img до
    // самого выхода воркера (в т.ч. из нити drdynvc при GFX) — поздний кадр не
    // должен вернуть цвет; кроме того, paintEvent читает m_img на Qt-нити, поэтому
    // и снапшот, и флаг красим под m_imgMu.
    void grayLastFrame() {
        {
            QMutexLocker lk(&m_imgMu);
            m_gray = true;
            if (!m_img.isNull()) {
                QImage g = m_img.copy();
                asbru_gray_frame(g.bits(), g.width(), g.height(), (int)g.bytesPerLine());
                m_imgGray = g;
            }
        }
        QMetaObject::invokeMethod(this, [this]{ update(); }, Qt::QueuedConnection);
    }

    int m_id;
    std::thread m_thread;
    std::atomic<bool> m_stop;
    std::atomic<bool> m_reported;
    // Ревью C (Б3): atomic — пишется в setFrameRect (в т.ч. с DVC-нити при
    // GFX), читается в done() воркера без общей блокировки.
    std::atomic<bool> m_frameReported{false}; // А2: первый кадр → Go-сигнал «подключено»
    std::string m_host, m_user, m_domain, m_pass, m_title;
    int m_port, m_w, m_h, m_bpp;
    std::atomic<int> m_dw, m_dh;   // M33-г: атомик (гонка worker-DesktopResize/Qt-fx,cfy)
    bool m_clipboard;
    // M39: буфер обмена (cliprdr). m_cliprdr валиден только между успешным
    // connect и disconnect. m_clipMu — гвардия кэша (пишет Qt-нить, читает
    // воркер); m_clipAnnounce — «объявить серверу форматы»; гашение эха —
    // сверкой с m_lastRemote (см. onLocalClipChanged), а не латчем.
    CliprdrClientContext* m_cliprdr = nullptr;
    // M39: файловый буфер. Создаётся в start() (Qt-нить), инициализируется в
    // воркере после connect (init забирает cliprdr->custom и ставит файловые
    // колбэки), освобождается в деструкторе после join. Все прочие обращения
    // (init/uninit/update_client_data/current_flags/remote_*) — строго в нити
    // воркера: CliprdrFileContext не потокобезопасен (ag-d5u). GUI-нить только
    // кладёт пути в m_clipPathsPending под m_clipMu.
    CliprdrFileContext* m_clipFile = nullptr;
    // init прошёл успешно (пишет воркер после connect, читают Qt-нить/воркер).
    // Пока false, FileGroupDescriptorW не рекламируем: сервер не смог бы забрать
    // содержимое.
    std::atomic<bool> m_clipFileReady{false};
    // Динамическое разрешение (disp). m_disp/m_dispReady — под m_dispMu: пишет
    // колбэк канала (нить drdynvc — disp такой же DVC, как rdpgfx), читает/шлёт
    // worker (drainResize); удержание мьютекса на время SendMonitorLayout
    // исключает отправку в освобождённый контекст при разрыве канала.
    // m_res* — слот «последний запрошенный размер» (last-wins): пишет
    // resizeEvent (Qt-нить), читает/гасит worker. m_resLastSent — только worker
    // (throttle между отправками, как RESIZE_MIN_DELAY_NS в xf_disp.c:44).
    std::mutex m_dispMu;
    DispClientContext* m_disp = nullptr;
    bool m_dispReady = false;
    QMutex m_resMu;
    bool m_resHave = false;
    int m_resW = 0, m_resH = 0;
    std::chrono::steady_clock::time_point m_resLastSent{};
    QMutex m_clipMu;
    std::string m_clipUtf16; // локальный буфер как CF_UNICODETEXT (UTF-16+NUL)
    std::string m_clipDib;   // локальный буфер как CF_DIB (голый DIB, без «BM»)
    std::string m_clipHtml;  // локальный HTML как CF_HTML payload (UTF-8+заголовок)
    // M39: файлы. m_clipPathsPending — «почтовый ящик»: GUI-нить кладёт только
    // ВЫБРАННЫЕ ПУТИ (дешёвое извлечение из QMimeData), а рекурсивный обход,
    // сборку text/uri-list и CLIPRDR_FILELIST делает воркер в pumpClipboard —
    // не в GUI-нити и не под m_clipMu (ag-32m). m_clipFileList — опубликованный
    // список для FileGroupDescriptorW: пишет воркер (вместе с потоками при
    // update_client_data), читает cbClipDataRequest; GUI-нить только сбрасывает
    // его при копировании без файлов. m_clipFilePending — есть необработанные
    // пути. Все поля — под m_clipMu (start() — до запуска воркера, деструктор —
    // после join).
    std::vector<std::string> m_clipPathsPending;
    std::string m_clipFileList;
    bool m_clipFilePending = false;
    bool m_clipHave = false;     // есть текст
    bool m_clipHaveDib = false;  // есть картинка (CF_DIB)
    bool m_clipHaveHtml = false; // есть HTML (CF_HTML)
    bool m_clipHaveFiles = false; // есть локальные файлы (FileGroupDescriptorW)
    bool m_clipTextAscii = false; // текст — чистый ASCII (CF_TEXT рекламируется)
    // Формат, запрошенный у сервера в ServerFormatList: в FORMAT_DATA_RESPONSE
    // идентификатора формата нет (протокол MS-RDPECLIP), без памяти о запросе
    // DIB-ответ нельзя отличить от текстового. Атомик — запас прочности: оба
    // колбэка сейчас на worker, но поле читается и из другого контекста.
    std::atomic<UINT32> m_clipReqFmt{0};
    // Как разбирать ответ (в PDU ответа формат не передаётся): для CF_HTML id
    // назначает сервер, сравнивать с константой нельзя.
    std::atomic<int> m_clipReqKind{ClipReqNone};
    std::atomic<bool> m_clipAnnounce{false};
    std::atomic<bool> m_clipReady{false}; // MonitorReady получен (канал согласован)
    // M41: GFX. m_gfxIface — интерфейс канала rdpgfx из ChannelConnected,
    // m_gfxActive — gdi_graphics_pipeline_init прошёл (EGFX реально пошёл).
    // ВАЖНО (ревью M41 D1, проверено вживую): писать их может НЕ воркер, а
    // async-нить drdynvc — DVC-канал создаётся сервером (CREATE_REQUEST_PDU) и
    // разбирается в отдельной нити (FreeRDP_SynchronousDynamicChannels по
    // умолчанию FALSE, drdynvc_main.c), поэтому И обработчик канала, И onEndPaint
    // при GFX исполняются на ней. Отсюда атомики ниже. Раньше (bitmap-путь) всё
    // было на воркере — гонки принесены M41.
    // ВАЖНО: поля, а не файловые переменные — RDP-вкладок может быть несколько.
    // m_gfxMu — гвардия m_gfxIface и перехода m_gfxActive: пишет async-нить
    // drdynvc (cbChannelConnected/cbChannelDisconnected), читает воркер после
    // gdi_init (tryInitGraphicsPipeline). m_gfxActive дополнительно читается в
    // diagTick, поэтому остаётся атомиком.
    std::mutex m_gfxMu;
    RdpgfxClientContext* m_gfxIface = nullptr; // под m_gfxMu
    std::atomic<bool> m_gfxActive{false};
    pBitmapUpdate m_savedBitmapUpdate = nullptr;
    pSurfaceBits m_savedSurfaceBits = nullptr;
    // M41: счётчики для живой диагностики (только при ASBRU_RDPCLIP_LOG) — чтобы
    // видеть, каким путём идут пиксели: сырые bitmap'ы (десятки тысяч КиБ/с) или
    // сжатые/GFX. Инкремент и печать — в одной нити-воркере, атомики не нужны.
    // Атомарные: инкрементит одна нить (воркер или async drdynvc), а обнуляет и
    // печатает — воркер в diagTick (ревью M41 D1).
    std::atomic<uint64_t> m_dgBmUpdates{0}, m_dgBmRects{0}, m_dgBmBytes{0}, m_dgBmCompressed{0};
    std::atomic<uint64_t> m_dgSbCmds{0}, m_dgSbBytes{0}, m_dgEndPaint{0};
    std::atomic<int> m_dgLastBpp{0};
    int m_dgTick = 0; // только воркер
    // M41: диагностика ВВОДА (только при ASBRU_RDPCLIP_LOG) — счётчики по границам:
    // сколько событий дошло от Qt, сколько легло в очередь, сколько ушло в RDP.
    // Разница между ними и локализует источник дублей, если они есть.
    std::atomic<uint64_t> m_inQtPress{0}, m_inQtRelease{0}, m_inQtRepeat{0};
    std::atomic<uint64_t> m_inPushDown{0}, m_inPushUp{0};
    std::atomic<uint64_t> m_inSentDown{0}, m_inSentUp{0};
    std::atomic<uint64_t> m_inSentUni{0}, m_inSentScan{0}, m_inSentRepeatFlag{0};
    std::atomic<uint64_t> m_inDupDown{0}, m_inStrayUp{0};
    std::atomic<uint64_t> m_inSentFail{0}; // отправка отвергнута библиотекой
    // Каким путём ушло НАЖАТИЕ каждой удерживаемой клавиши — чтобы отпускание
    // ушло тем же (см. key(); только Qt-нить, мьютекс не нужен).
    // M43: map<Qt::Key, отправленный сканкод> — код нужен на ОТПУСКАНИИ (см. key()).
    std::map<int, uint32_t> m_keyScancode;
    std::map<int, uint32_t> m_keyUnicode;
    // Текст, пришедший от сервера, — для гашения эха сверкой содержимого
    // (только Qt-нить: и запись, и чтение в onLocalClipChanged).
    QString m_lastRemote;
    // Хэш картинки, пришедшей от сервера (0 — эха не было). Только Qt-нить:
    // пишет лямбда cbClipDataResponse, читает onLocalClipChanged.
    uint64_t m_lastRemoteImageHash = 0;
    // Хэш HTML, пришедшего от сервера (0 — эха не было). Только Qt-нить:
    // пишет лямбда cbClipDataResponse, читает onLocalClipChanged. Гасит
    // dataChanged от нашего setMimeData (html + plain text).
    uint64_t m_lastRemoteHtmlHash = 0;
    unsigned int m_flags = 0;   // M37: RDP_FLAG_* из галок редактора
    // M39: перенаправляемые каталоги ((share, path)); задаются в start()
    // (Qt-нить), читаются воркером в run() до/во время connect.
    std::vector<std::pair<std::string, std::string>> m_drives;
    // M39: звук — аргумент канала rdpsnd ("sys:alsa", …). Пусто — звук не
    // включаем (дефолт FreeRDP sys:fake). Задаётся в start(), читается воркером.
    std::string m_sound;
    int m_tlsLevel = 0;         // M37: /tls-seclevel:N (0 — не задано)
    std::string m_kbd;          // M38: /kbd:NAME (раскладка)
    freerdp* m_instance;
    rdpContext* m_context;             // только для abort-connect (под m_ctxMu)
    std::mutex m_ctxMu;                // M33-г: гвардия m_context (гонка stop/connect)
    std::mutex m_errMu;                // M33-г: гвардия m_errMsg (гонка worker/paint)
    std::string m_errMsg;              // M33-г: текст ошибки на канвасе
    pEndPaint m_savedEndPaint;
    pDesktopResize m_savedDesktopResize;
    QMutex m_imgMu;
    QImage m_img;              // M40: ПОСТОЯННЫЙ буфер кадра (пересоздаётся только на ресайзе)
    std::atomic<bool> m_needFull{true}; // M40: следующий кадр копировать целиком
    // Обрыв при показанном кадре: ч/б снапшот последнего кадра — рисуется вместо
    // m_img (см. grayLastFrame). Под m_imgMu: пишет done (воркер), читает paintEvent.
    QImage m_imgGray;
    bool m_gray = false;
    QMutex m_inMu;
    std::deque<InputMsg> m_inputs;
    // M39-fullscreen: полноэкранный режим вкладки. Пока включён, одиночный Esc
    // перехватывается (rdpGoFullscreen) и не уходит в сессию; m_escHeld — чтобы
    // «съеденный» press не дал серверу парный release.
    bool m_fullscreen = false;
    bool m_escHeld = false;
};



RdpCanvas* rdpViewFor(int id) {
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_views.find(id);
    return it == g_views.end() ? nullptr : it->second;
}
RdpCanvas* ctxCanvas(rdpContext* ctx) {
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_byCtx.find(ctx);
    return it == g_byCtx.end() ? nullptr : it->second;
}
void regCtx(rdpContext* ctx, RdpCanvas* c) {
    std::lock_guard<std::mutex> lk(g_mu);
    g_byCtx[ctx] = c;
}
void unregCtx(rdpContext* ctx) {
    std::lock_guard<std::mutex> lk(g_mu);
    g_byCtx.erase(ctx);
}

extern "C" {

// --- little-endian чтение/запись полей BMP (заголовки на проводе LE) ---
static uint16_t rdclip_le16(const uint8_t* p) {
    return (uint16_t)((uint16_t)p[0] | ((uint16_t)p[1] << 8));
}
static uint32_t rdclip_le32(const uint8_t* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}
static void wrclip_le16(uint8_t* p, uint16_t v) {
    p[0] = (uint8_t)(v & 0xFF);
    p[1] = (uint8_t)((v >> 8) & 0xFF);
}
static void wrclip_le32(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)(v & 0xFF);
    p[1] = (uint8_t)((v >> 8) & 0xFF);
    p[2] = (uint8_t)((v >> 16) & 0xFF);
    p[3] = (uint8_t)((v >> 24) & 0xFF);
}

// rdp_dib_strip_bmp_header — срезать BITMAPFILEHEADER у BMP-файла, полученного
// от QImageWriter("BMP"): на провод CF_DIB обязан уйти ГОЛЫЙ DIB. Проверяем
// только магию «BM» и минимальную длину (14 + хотя бы один байт DIB); полную
// валидность DIB проверять не нужно — его и так сформировал Qt. Чистая функция,
// контракт проверяется Go-обёрткой (cgo в _test.go запрещён).
int rdp_dib_strip_bmp_header(const uint8_t *bmp, uint32_t len, uint32_t *outDibLen)
{
    if (!bmp || len <= 14) return 0;
    if (bmp[0] != 'B' || bmp[1] != 'M') return 0;
    if (outDibLen) *outDibLen = len - 14;
    return 1;
}

// rdp_dib_pixel_offset — смещение пикселей внутри DIB. Формула из MSDN
// (BITMAPINFOHEADER/BITMAPV5HEADER) и Remmina (rdp_cliprdr.c): после заголовка
// идёт таблица цветов, у BITMAPINFOHEADER с BI_BITFIELDS — ещё три DWORD-маски,
// у V5 — встроенный ICC-профиль (bV5ProfileSize). Смещение пикселей в BMP-файле
// (bfOffBits) = 14 + это значение.
uint32_t rdp_dib_pixel_offset(const uint8_t *dib, uint32_t len)
{
    if (!dib || len < 40) return 0;
    const uint32_t biSize = rdclip_le32(dib);
    // Стандартные размеры: 40 (INFOHEADER), 52/56 (V2/V3), 108 (V4), 124 (V5).
    // Всё, что меньше 40 или выходит за буфер, — не DIB.
    if (biSize < 40 || biSize > len) return 0;
    const uint16_t bitCount = rdclip_le16(dib + 14);
    if (bitCount != 1 && bitCount != 4 && bitCount != 8 &&
        bitCount != 16 && bitCount != 24 && bitCount != 32)
        return 0;
    const uint32_t compression = rdclip_le32(dib + 16);
    const uint32_t clrUsed = rdclip_le32(dib + 32);
    // Таблица цветов. По умолчанию для <= 8 бит — 2^bitCount записей; biClrUsed
    // (если задан) её сужает. Для > 8 бит таблица не обязательна, но biClrUsed
    // может описывать реально присутствующую (обычно 0).
    uint64_t entries = clrUsed;
    if (bitCount <= 8 && clrUsed == 0) entries = 1ULL << bitCount;
    uint64_t off = (uint64_t)biSize + entries * 4ULL;
    // BI_BITFIELDS = 3: у BITMAPINFOHEADER маски (R/G/B) идут ПОСЛЕ заголовка;
    // у V4/V5 они уже поля заголовка. Плюс 12 только для 40-байтного заголовка.
    if (compression == 3 && biSize == 40) off += 12ULL;
    // BITMAPV5HEADER: bV5ProfileSize лежит по смещению 116; профиль идёт после
    // таблицы цветов (MSDN: «color table, profile data, bitmap bits»).
    if (biSize >= 124) off += (uint64_t)rdclip_le32(dib + 116);
    if (off > (uint64_t)len) return 0; // усечённый заголовок/палитра/профиль
    return (uint32_t)off;
}

// rdp_bmp_from_dib — синтезировать BITMAPFILEHEADER и скопировать DIB. Именно
// этого не хватает сервер→локальному направлению: QImage::fromData("BMP")
// требует файл, а cliprdr присылает голый DIB.
uint32_t rdp_bmp_from_dib(const uint8_t *dib, uint32_t len,
                          uint8_t *out, uint32_t outCap)
{
    if (!dib || !out) return 0;
    const uint32_t pixOff = rdp_dib_pixel_offset(dib, len);
    if (pixOff == 0) return 0;
    const uint64_t total = 14ULL + (uint64_t)len;
    if (total > 0xFFFFFFFFULL || (uint64_t)outCap < total) return 0;
    out[0] = 'B';
    out[1] = 'M';
    wrclip_le32(out + 2, (uint32_t)total);       // bfSize
    wrclip_le16(out + 6, 0);                      // bfReserved1
    wrclip_le16(out + 8, 0);                      // bfReserved2
    wrclip_le32(out + 10, 14u + pixOff);          // bfOffBits — уже с заголовком
    memcpy(out + 14, dib, len);
    return (uint32_t)total;
}

// --- M39: CF_HTML и CF_TEXT ---
// Заголовок CF_HTML по MSDN «HTML Clipboard Format»: Version:0.9 и четыре поля
// (StartHTML/EndHTML/StartFragment/EndFragment) фиксированной ширины 10 цифр.
// Фиксированная ширина делает длину заголовка постоянной (105 байт), поэтому
// смещения можно считать ДО записи заголовка, а StartHTML всегда 105.
static const char kCfHtmlStartMarker[] = "<!--StartFragment-->";
static const char kCfHtmlEndMarker[] = "<!--EndFragment-->";
static const uint32_t kCfHtmlHeaderLen = 105;

// Байтовый поиск подстроки (memmem нет в C++17).
static const uint8_t* rdclip_memfind(const uint8_t* hay, uint32_t hayLen,
                                     const char* needle, uint32_t needleLen)
{
    if (!hay || !needle || needleLen == 0 || hayLen < needleLen) return nullptr;
    for (uint32_t i = 0; i + needleLen <= hayLen; i++)
        if (memcmp(hay + i, needle, needleLen) == 0) return hay + i;
    return nullptr;
}

// Десятичное поле заголовка CF_HTML (строго с начала строки и до \r/\n).
static int rdclip_header_u32(const uint8_t* p, uint32_t len,
                             const char* key, uint32_t* out)
{
    const uint32_t klen = (uint32_t)strlen(key);
    for (uint32_t i = 0; i + klen <= len; i++) {
        if (i != 0 && p[i - 1] != '\n') continue; // ключ только в начале строки
        if (memcmp(p + i, key, klen) != 0) continue;
        uint32_t j = i + klen;
        uint64_t v = 0;
        uint32_t digits = 0;
        while (j < len && p[j] >= '0' && p[j] <= '9') {
            v = v * 10ULL + (uint64_t)(p[j] - '0');
            if (v > 0xFFFFFFFFULL) return 0;
            j++;
            digits++;
        }
        if (digits == 0) return 0;
        if (j < len && p[j] != '\r' && p[j] != '\n') return 0;
        if (out) *out = (uint32_t)v;
        return 1;
    }
    return 0;
}

// rdp_text_is_ascii — CF_TEXT на проводе это ANSI-строка (системная кодовая
// страница Windows). UTF-8 байты >= 0x80 в ней дают мозаику, поэтому такой
// текст рекламируем только как CF_UNICODETEXT. Пустой текст — ASCII.
int rdp_text_is_ascii(const uint8_t *s, uint32_t len)
{
    if (!s && len != 0) return 0;
    for (uint32_t i = 0; i < len; i++)
        if (s[i] & 0x80) return 0;
    return 1;
}

// rdp_cf_html_build — обернуть UTF-8 HTML в CF_HTML. Маркеры StartFragment/
// EndFragment, если они уже есть в HTML, задают границы фрагмента; иначе
// фрагмент — весь документ. Смещения БАЙТОВЫЕ, так что многобайтовый UTF-8
// (кириллица, emoji) не сдвигает конец фрагмента.
uint32_t rdp_cf_html_build(const uint8_t *html, uint32_t len,
                           uint8_t *out, uint32_t outCap)
{
    if (!html || len == 0 || !out) return 0;
    if (len > 0xFFFFFFFFu - kCfHtmlHeaderLen) return 0;
    const uint32_t total = kCfHtmlHeaderLen + len;
    if (outCap < total) return 0;

    uint32_t fragStart = 0, fragEnd = len;
    const uint32_t smLen = (uint32_t)(sizeof(kCfHtmlStartMarker) - 1);
    const uint32_t emLen = (uint32_t)(sizeof(kCfHtmlEndMarker) - 1);
    const uint8_t* sp = rdclip_memfind(html, len, kCfHtmlStartMarker, smLen);
    const uint8_t* ep = rdclip_memfind(html, len, kCfHtmlEndMarker, emLen);
    if (sp && ep) {
        const uint32_t s = (uint32_t)(sp - html) + smLen;
        const uint32_t e = (uint32_t)(ep - html);
        if (s <= e) { fragStart = s; fragEnd = e; }
    }

    const int n = snprintf((char*)out, (size_t)outCap,
        "Version:0.9\r\n"
        "StartHTML:%010u\r\n"
        "EndHTML:%010u\r\n"
        "StartFragment:%010u\r\n"
        "EndFragment:%010u\r\n",
        (unsigned)kCfHtmlHeaderLen, (unsigned)total,
        (unsigned)(kCfHtmlHeaderLen + fragStart),
        (unsigned)(kCfHtmlHeaderLen + fragEnd));
    if (n != (int)kCfHtmlHeaderLen) return 0;
    // snprintf поставил NUL в out[105]; HTML перекрывает его и идёт подряд.
    memcpy(out + kCfHtmlHeaderLen, html, len);
    return total;
}

// rdp_cf_html_parse — достать фрагмент из CF_HTML. Порядок доверия:
//   1) StartFragment/EndFragment из заголовка (с проверкой диапазона);
//   2) маркеры-комментарии (сервер мог не заполнить смещения);
//   3) голый HTML целиком (сервер прислал фрагмент без заголовка).
// Если заголовок ЯВНО есть («Version:»), но испорчен, — отказ: иначе в буфер
// попал бы текст самого заголовка.
int rdp_cf_html_parse(const uint8_t *payload, uint32_t len,
                      uint32_t *outStart, uint32_t *outEnd)
{
    if (!payload || len == 0 || !outStart || !outEnd) return 0;

    uint32_t s = 0, e = 0;
    const int hs = rdclip_header_u32(payload, len, "StartFragment:", &s);
    const int he = rdclip_header_u32(payload, len, "EndFragment:", &e);
    // s >= 1: настоящий фрагмент всегда идёт после заголовка.
    if (hs && he && s >= 1 && s <= e && e <= len) {
        *outStart = s;
        *outEnd = e;
        return 1;
    }

    const uint32_t smLen = (uint32_t)(sizeof(kCfHtmlStartMarker) - 1);
    const uint32_t emLen = (uint32_t)(sizeof(kCfHtmlEndMarker) - 1);
    const uint8_t* sp = rdclip_memfind(payload, len, kCfHtmlStartMarker, smLen);
    const uint32_t fs = sp ? (uint32_t)(sp - payload) + smLen : 0;
    uint32_t fe = len;
    if (fs <= len) {
        const uint8_t* ep = rdclip_memfind(payload + fs, len - fs,
                                           kCfHtmlEndMarker, emLen);
        if (ep) fe = (uint32_t)(ep - payload);
    }
    if (sp || fe != len) {
        if (fs <= fe && fe <= len) {
            *outStart = fs;
            *outEnd = fe;
            return 1;
        }
    }

    if (len >= 8 && memcmp(payload, "Version:", 8) == 0) return 0;

    *outStart = 0;
    *outEnd = len;
    return 1;
}

// --- M39: файлы в буфере обмена (CLIPRDR_FILELIST) ---
// text/uri-list: FreeRDP-парсер (client_cliprdr_file.c, cliprdr_local_stream_update)
// режет вход по "\r\n", снимает префикс file:/// и делает URL-decode. Поэтому
// кодируем каждый БАЙТ UTF-8 (пробелы, '#', '?', '%', кириллицу) как %XX, а
// незарезервированные символы и '/' оставляем как есть — иначе путь с пробелом
// или решёткой развалился бы на несколько «файлов».
static int rdclip_uri_unreserved(uint8_t c)
{
    return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
           (c >= '0' && c <= '9') ||
           c == '-' || c == '.' || c == '_' || c == '~' || c == '/';
}

static uint64_t rdclip_uri_encoded_len(const char *p)
{
    uint64_t n = 0;
    for (const uint8_t* s = (const uint8_t*)p; *s; s++)
        n += rdclip_uri_unreserved(*s) ? 1ULL : 3ULL;
    return n;
}

// ag-q2t: FIFO и unix-сокет в файловый буфер не переносятся. winpr
// FindNextFileA пропускает FIFO у ДЕТЕЙ каталога, а для КОРНЕВОГО URI
// make_synthetic_file (FindFirstFileW) вообще не создаёт handle — эталонный
// клиент тогда не шлёт список файлов. Наш обход, наоборот, писал корень-не-
// каталог дескриптором как обычный файл: сервер запрашивал содержимое, и
// winpr_fopen(fifo, "rb") (client_cliprdr_file.c) блокировался до появления
// писателя — воркер висел навсегда (ни ввода, ни кадров).
//
// Фильтровать нужно ОБА списка (uri-list и CLIPRDR_FILELIST): их порядок
// обязан совпадать, иначе listIndex дескрипторов разойдётся с записями
// cliprdr_file_context, который строит их из того же uri-списка. Несуществующий
// путь НЕ отбрасываем: FreeRDP добавляет его append_entry без проверки.
static int rdclip_root_not_transferable(const char *path)
{
    struct stat st;
    if (stat(path, &st) != 0) return 0; // нет пути — оставляем как FreeRDP
    return S_ISFIFO(st.st_mode) || S_ISSOCK(st.st_mode);
}

uint32_t rdp_uri_list_build(const char *const *paths, uint32_t count,
                            uint8_t *out, uint32_t outCap)
{
    if (!paths || count == 0) return 0;
    // Первый проход — точный размер; out == NULL означает «только посчитать».
    uint64_t need = 0;
    for (uint32_t i = 0; i < count; i++) {
        if (!paths[i] || !paths[i][0]) continue;
        if (rdclip_root_not_transferable(paths[i])) continue;
        need += 7ULL /* "file://" */ + rdclip_uri_encoded_len(paths[i]) + 2ULL /* CRLF */;
    }
    if (need == 0 || need > 0xFFFFFFFFULL) return 0;
    if (!out) return (uint32_t)need;
    if ((uint64_t)outCap < need) return 0;

    static const char kHex[] = "0123456789ABCDEF";
    uint8_t* w = out;
    const uint8_t* end = out + need;
    for (uint32_t i = 0; i < count; i++) {
        const char* p = paths[i];
        if (!p || !p[0]) continue;
        if (rdclip_root_not_transferable(p)) continue;
        memcpy(w, "file://", 7);
        w += 7;
        for (const uint8_t* s = (const uint8_t*)p; *s; s++) {
            if (rdclip_uri_unreserved(*s)) {
                *w++ = *s;
            } else {
                *w++ = '%';
                *w++ = (uint8_t)kHex[*s >> 4];
                *w++ = (uint8_t)kHex[*s & 0x0F];
            }
        }
        *w++ = '\r';
        *w++ = '\n';
    }
    return (uint32_t)(w == end ? need : 0);
}

static void wrclip_le64(uint8_t* p, uint64_t v)
{
    for (int i = 0; i < 8; i++) p[i] = (uint8_t)((v >> (8 * i)) & 0xFF);
}

// FILEDESCRIPTORW (MS-RDPECLIP / winpr/shell.h) занимает ровно 592 байта.
static const uint32_t kClipFdDescriptorLen = 592;
static const uint32_t kClipFdAttributes    = 0x00000004; // FD_ATTRIBUTES
static const uint32_t kClipFdCreatetime    = 0x00000008; // FD_CREATETIME
static const uint32_t kClipFdAccesstime    = 0x00000010; // FD_ACCESSTIME
static const uint32_t kClipFdWritestime    = 0x00000020; // FD_WRITESTIME
static const uint32_t kClipFdFilesize      = 0x00000040; // FD_FILESIZE
static const uint32_t kClipFdProgressui    = 0x00004000; // FD_PROGRESSUI
static const uint32_t kClipAttrDirectory   = 0x00000010; // FILE_ATTRIBUTE_DIRECTORY
static const uint32_t kClipAttrNormal      = 0x00000080; // FILE_ATTRIBUTE_NORMAL

// Unix-время (секунды) → FILETIME (100 нс с 1601-01-01). 0 — «нет времени».
static uint64_t rdclip_filetime(int64_t sec)
{
    if (sec <= 0) return 0;
    return ((uint64_t)sec + 11644473600ULL) * 10000000ULL;
}

// UTF-8 → UTF-16LE, не более capUnits код-юнитов (буфер дескриптора уже
// обнулён: NUL-терминатор получается сам, если не заполнить последний слот).
static uint32_t rdclip_utf16le(uint8_t* dst, uint32_t capUnits,
                               const uint8_t* s, uint32_t len)
{
    uint32_t i = 0, n = 0;
    while (i < len && n < capUnits) {
        uint32_t cp = 0xFFFD, adv = 1;
        const uint8_t c = s[i];
        if (c < 0x80) { cp = c; adv = 1; }
        else if ((c & 0xE0) == 0xC0 && i + 1 < len) {
            cp = ((uint32_t)(c & 0x1F) << 6) | (s[i + 1] & 0x3F);
            if (cp < 0x80) cp = 0xFFFD; else adv = 2;
        } else if ((c & 0xF0) == 0xE0 && i + 2 < len) {
            cp = ((uint32_t)(c & 0x0F) << 12) |
                 ((uint32_t)(s[i + 1] & 0x3F) << 6) | (s[i + 2] & 0x3F);
            if (cp < 0x800) cp = 0xFFFD; else adv = 3;
        } else if ((c & 0xF8) == 0xF0 && i + 3 < len) {
            cp = ((uint32_t)(c & 0x07) << 18) |
                 ((uint32_t)(s[i + 1] & 0x3F) << 12) |
                 ((uint32_t)(s[i + 2] & 0x3F) << 6) | (s[i + 3] & 0x3F);
            if (cp < 0x10000 || cp > 0x10FFFF) cp = 0xFFFD; else adv = 4;
        }
        i += adv;
        if (cp <= 0xFFFF) {
            if (cp >= 0xD800 && cp <= 0xDFFF) cp = 0xFFFD; // одиночный суррогат
            wrclip_le16(dst + n * 2, (uint16_t)cp);
            n++;
        } else {
            cp -= 0x10000;
            if (n + 1 >= capUnits) {
                // Не хватает места на суррогатную пару — U+FFFD вместо обрыва.
                wrclip_le16(dst + n * 2, 0xFFFD);
                n++;
            } else {
                wrclip_le16(dst + n * 2, (uint16_t)(0xD800 + (cp >> 10)));
                wrclip_le16(dst + (n + 1) * 2, (uint16_t)(0xDC00 + (cp & 0x3FF)));
                n += 2;
            }
        }
    }
    return n;
}

// Лист пути: последний компонент без завершающих '/'. Копируется в out, чтобы
// вызывающий получил NUL-строку (в FILEDESCRIPTORW cFileName — только имя, не
// полный путь).
static void rdclip_leaf_name(const char* path, char* out, size_t cap)
{
    if (!out || cap == 0) return;
    out[0] = '\0';
    if (!path) return;
    size_t end = strlen(path);
    while (end > 1 && path[end - 1] == '/') end--;
    size_t start = end;
    while (start > 0 && path[start - 1] != '/') start--;
    size_t n = end - start;
    if (n >= cap) n = cap - 1;
    memcpy(out, path + start, n);
    out[n] = '\0';
}

// ag-73o: порт ValidFileNameComponent (winpr/libwinpr/file/file.c:1177 —
// winpr делегирует её из synthetic_file.c:1409 перед отправкой каждого
// компонента имени). winpr при невалидном имени роняет конверсию ВСЕГО списка,
// и эталонный клиент файл не отправляет; в комментарии winpr прямо сказано, что
// такие имена «роняют некоторые оболочки Windows при вставке».
//
// Правила (сравнение по ASCII, регистронезависимое; байты UTF-8 ≥0x80 в них не
// участвуют, поэтому проверка по UTF-8-байтам эквивалентна проверке по WCHAR):
//  - запрещённые символы: < > : " / \ | ? *;
//  - зарезервированные имена DOS-устройств РОВНО из 3 или 4 символов:
//    CON, PRN, AUX, NUL, COM0..COM9, LPT0..LPT9.
//
// Завершающиеся точку/пробел winpr НЕ отсекает, как и не отсекает "CON.txt"
// (там точное совпадение длины). Повторяем именно поведение winpr: цель —
// паритет с эталонным клиентом (не отправлять то, что он не отправляет), а не
// более строгая Windows-валидация — иначе мы молча теряли бы файлы, которые
// xfreerdp отправляет.
static int rdclip_ascii_eq_ci(const char* s, size_t n, const char* lit)
{
    const size_t litLen = strlen(lit);
    if (litLen != n) return 0;
    for (size_t i = 0; i < n; i++) {
        char a = s[i], b = lit[i];
        if (a >= 'A' && a <= 'Z') a = (char)(a - 'A' + 'a');
        if (b >= 'A' && b <= 'Z') b = (char)(b - 'A' + 'a');
        if (a != b) return 0;
    }
    return 1;
}

static int rdclip_name_component_valid(const char* name)
{
    if (!name) return 0; // winpr: NULL → FALSE
    const size_t n = strlen(name);
    if (n == 3) {
        if (rdclip_ascii_eq_ci(name, n, "con") ||
            rdclip_ascii_eq_ci(name, n, "prn") ||
            rdclip_ascii_eq_ci(name, n, "aux") ||
            rdclip_ascii_eq_ci(name, n, "nul"))
            return 0;
    } else if (n == 4 &&
               (rdclip_ascii_eq_ci(name, 3, "com") ||
                rdclip_ascii_eq_ci(name, 3, "lpt")) &&
               name[3] >= '0' && name[3] <= '9') {
        return 0;
    }
    for (size_t i = 0; i < n; i++) {
        switch (name[i]) {
        case '<': case '>': case ':': case '"':
        case '/': case '\\': case '|': case '?': case '*':
            return 0;
        default:
            break;
        }
    }
    return 1;
}

static int rdclip_filelist_descriptor(uint8_t* out, const char* leaf,
                                   const struct stat* st, int isDir)
{
    if (!out) return 0;
    memset(out, 0, kClipFdDescriptorLen);
    const uint32_t flags = kClipFdAttributes | kClipFdCreatetime |
                           kClipFdAccesstime | kClipFdWritestime |
                           kClipFdFilesize | kClipFdProgressui;
    wrclip_le32(out + 0, flags); // dwFlags
    // clsid (16), sizel (8), pointl (8) остаются нулевыми.
    wrclip_le32(out + 36, isDir ? kClipAttrDirectory : kClipAttrNormal);

    uint64_t size = 0;
    if (st) {
        wrclip_le64(out + 40, rdclip_filetime((int64_t)st->st_ctime));  // creation
        wrclip_le64(out + 48, rdclip_filetime((int64_t)st->st_atime));  // access
        wrclip_le64(out + 56, rdclip_filetime((int64_t)st->st_mtime));  // write
        if (!isDir) size = (uint64_t)st->st_size;
    }
    wrclip_le32(out + 64, (uint32_t)(size >> 32)); // nFileSizeHigh
    wrclip_le32(out + 68, (uint32_t)(size & 0xFFFFFFFF)); // nFileSizeLow

    // cFileName — WCHAR[260], 520 байт. Оставляем последний слот под NUL.
    const uint8_t* s = (const uint8_t*)(leaf ? leaf : "");
    rdclip_utf16le(out + 72, 259, s, (uint32_t)strlen((const char*)s));
    return 1;
}

// Пределы сборки CLIPRDR_FILELIST (ag-32m). Копирование корня проекта,
// домашнего каталога или node_modules не должно подвешивать UI и раздувать
// память: обход бросается по числу записей, полезным байтам или времени.
// 65536 × 592 Б ≈ 37 МиБ, поэтому байтовый предел (32 МиБ) срабатывает раньше
// — оба предела реальны. Значение 0 в аргументе *_limits означает «без предела»
// (нужно тестам).
static const uint32_t kClipFileListMaxItems = 65536;
static const uint32_t kClipFileListMaxBytes = 32u * 1024u * 1024u;
static const uint32_t kClipFileListMaxMs    = 5000;
// Отдельный предел на суммарную длину относительных путей (cFileName): байтовый
// предел считает только 592-байтные дескрипторы, а std::string-имена копятся
// помимо них. Действует всегда — это защита от патологического дерева имён.
static const uint64_t kClipFileListMaxNameBytes = 64ULL * 1024 * 1024;

// Причины отказа rdp_cliprdr_file_list_alloc* — kRdpClipList* из bridge.h.

// Собранный элемент: имя (relative path в cFileName) + снимок stat.
struct RdclipFileItem {
    std::string name;
    struct stat st;
    int have;
    int isDir;
};

// Один обход дерева копит элементы в vector; сериализация — вторым шагом без
// повторного stat (раньше было ДВА полных обхода: подсчёт длины и запись).
struct RdclipCollect {
    std::vector<RdclipFileItem> items;
    uint32_t maxItems;
    uint32_t maxBytes;
    std::chrono::steady_clock::time_point deadline;
    bool haveDeadline;
    uint64_t nameBytes;
    int reason;
};

static int rdclip_collect_add(RdclipCollect* c, const char* name,
                              const struct stat* st, int isDir)
{
    if (c->reason != kRdpClipListOk) return 0;
    if (c->maxItems && c->items.size() >= (size_t)c->maxItems) {
        c->reason = kRdpClipListTooMany;
        return 0;
    }
    if (c->haveDeadline && std::chrono::steady_clock::now() > c->deadline) {
        c->reason = kRdpClipListTimeout;
        return 0;
    }
    if (c->maxBytes) {
        const uint64_t after = 4ULL +
            ((uint64_t)c->items.size() + 1ULL) * kClipFdDescriptorLen;
        if (after > (uint64_t)c->maxBytes) {
            c->reason = kRdpClipListTooBig;
            return 0;
        }
    }
    const uint64_t nameLen = name ? (uint64_t)strlen(name) : 0;
    if (c->nameBytes + nameLen > kClipFileListMaxNameBytes) {
        c->reason = kRdpClipListTooBig;
        return 0;
    }
    c->nameBytes += nameLen;
    RdclipFileItem it;
    it.name = name ? name : "";
    it.have = st != nullptr;
    it.isDir = isDir;
    if (st) it.st = *st;
    else memset(&it.st, 0, sizeof(it.st));
    c->items.push_back(std::move(it));
    return 1;
}

// Pre-order DFS. Порядок детей — readdir; FreeRDP (winpr FindNextFile) использует
// тот же readdir, поэтому listIndex совпадает. FIFO пропускаются (winpr тоже);
// корневые FIFO/сокеты отсекает вызывающий (rdclip_root_not_transferable).
// rel — путь ОТНОСИТЕЛЬНО родителя корневого элемента, как его кладёт в
// cFileName официальный Windows-клиент (wf_cliprdr_process_filename считает
// pathLen по последнему '\' корня и режет по нему всех потомков): для
// /tmp/root/a.txt это "root\a.txt", для одиночного файла — "a.txt".
//
// component — имя ЭТОГО элемента (последний компонент пути), а не
// относительный путь rel: winpr проверяет именно компоненты (backslash в
// cFileName — разделитель и сам по себе недопустимым не считается). Невалидное
// имя роняет весь список.
static int rdclip_collect_walk(const char* path, const char* rel,
                               const char* component, int depth,
                               RdclipCollect* c)
{
    if (!path || !rel || !component || depth > 64) {
        if (c->reason == kRdpClipListOk) c->reason = kRdpClipListIoError;
        return 0;
    }
    if (!rdclip_name_component_valid(component)) {
        if (c->reason == kRdpClipListOk) c->reason = kRdpClipListInvalidName;
        return 0;
    }
    struct stat st;
    const int have = stat(path, &st) == 0;
    const int isDir = have && S_ISDIR(st.st_mode);
    if (!rdclip_collect_add(c, rel, have ? &st : nullptr, isDir)) return 0;
    if (!isDir) return 1;

    DIR* d = opendir(path);
    if (!d) { // FreeRDP: add_directory вернёт FALSE → update_client_data тоже
        if (c->reason == kRdpClipListOk) c->reason = kRdpClipListIoError;
        return 0;
    }
    struct dirent* de;
    while ((de = readdir(d)) != nullptr) {
        if (strcmp(de->d_name, ".") == 0 || strcmp(de->d_name, "..") == 0)
            continue;
        std::string child(path);
        if (child.empty() || child.back() != '/') child += '/';
        child += de->d_name;
        struct stat cst;
        if (stat(child.c_str(), &cst) != 0)
            continue; // winpr FindNextFile пропускает записи с неудачным stat
        if (S_ISFIFO(cst.st_mode))
            continue; // и FIFO
        std::string childRel(rel);
        if (!childRel.empty() && childRel.back() != '\\') childRel += '\\';
        childRel += de->d_name;
        if (!rdclip_collect_walk(child.c_str(), childRel.c_str(), de->d_name,
                                 depth + 1, c)) {
            closedir(d);
            return 0;
        }
    }
    closedir(d);
    return 1;
}

// Собрать CLIPRDR_FILELIST одним обходом в malloc-буфер (владелец —
// вызывающий, освобождать rdp_free). maxItems/maxBytes/maxMs == 0 — без
// соответствующего предела. *outLen — длина результата, *outReason — причина
// отказа (kRdpClipList*), при успехе 0.
static uint8_t* rdclip_file_list_alloc_limits(
        const char *const *paths, uint32_t count,
        uint32_t maxItems, uint32_t maxBytes, uint32_t maxMs,
        uint32_t* outLen, int* outReason)
{
    if (outLen) *outLen = 0;
    if (outReason) *outReason = kRdpClipListOk;
    if (!paths || count == 0) {
        if (outReason) *outReason = kRdpClipListEmpty;
        return nullptr;
    }

    RdclipCollect c;
    c.maxItems = maxItems;
    c.maxBytes = maxBytes;
    c.haveDeadline = maxMs != 0;
    if (c.haveDeadline)
        c.deadline = std::chrono::steady_clock::now() +
                     std::chrono::milliseconds(maxMs);
    c.nameBytes = 0;
    c.reason = kRdpClipListOk;

    for (uint32_t i = 0; i < count; i++) {
        if (!paths[i] || !paths[i][0]) continue;
        // ag-q2t: корневой FIFO/сокет отбрасываем ДО обхода — дескриптор на него
        // заставил бы сервер запросить содержимое, а winpr_fopen(FIFO) вешает
        // воркер. Фильтр тот же, что в rdp_uri_list_build (порядок совпадает).
        if (rdclip_root_not_transferable(paths[i])) continue;
        char root_rel[1024];
        rdclip_leaf_name(paths[i], root_rel, sizeof(root_rel));
        if (!rdclip_collect_walk(paths[i], root_rel, root_rel, 0, &c)) break;
    }
    if (c.reason != kRdpClipListOk) {
        if (outReason) *outReason = c.reason;
        return nullptr;
    }
    if (c.items.empty()) {
        if (outReason) *outReason = kRdpClipListEmpty;
        return nullptr;
    }

    const uint64_t total =
        4ULL + (uint64_t)c.items.size() * kClipFdDescriptorLen;
    if (total > 0xFFFFFFFFULL) {
        if (outReason) *outReason = kRdpClipListTooBig;
        return nullptr;
    }
    uint8_t* buf = (uint8_t*)calloc(1, (size_t)total);
    if (!buf) {
        if (outReason) *outReason = kRdpClipListIoError;
        return nullptr;
    }
    wrclip_le32(buf, (uint32_t)c.items.size()); // cItems
    for (size_t i = 0; i < c.items.size(); i++) {
        const RdclipFileItem& it = c.items[i];
        if (!rdclip_filelist_descriptor(
                buf + 4 + i * kClipFdDescriptorLen,
                it.name.c_str(), it.have ? &it.st : nullptr, it.isDir)) {
            free(buf);
            if (outReason) *outReason = kRdpClipListIoError;
            return nullptr;
        }
    }
    if (outLen) *outLen = (uint32_t)total;
    return buf;
}

uint8_t* rdp_cliprdr_file_list_alloc_limits(
        const char *const *paths, uint32_t count,
        uint32_t maxItems, uint32_t maxBytes, uint32_t maxMs,
        uint32_t* outLen, int* outReason)
{
    return rdclip_file_list_alloc_limits(paths, count, maxItems, maxBytes,
                                         maxMs, outLen, outReason);
}

uint8_t* rdp_cliprdr_file_list_alloc(const char *const *paths, uint32_t count,
                                     uint32_t* outLen, int* outReason)
{
    return rdclip_file_list_alloc_limits(paths, count,
        kClipFileListMaxItems, kClipFileListMaxBytes, kClipFileListMaxMs,
        outLen, outReason);
}

void rdp_free(void* p)
{
    free(p);
}

// rdp_ignore_certificate — FreeRDP_IgnoreCertificate из маски галок.
// Семантика xfreerdp: по умолчанию сертификат ПРОВЕРЯЕТСЯ, и только
// /cert-ignore (RDP_FLAG_CERT_IGNORE) отключает проверку. Чистая функция —
// контракт проверяется Go-обёрткой (cgo в _test.go запрещён).
int rdp_ignore_certificate(unsigned int flags)
{
    return (flags & RDP_FLAG_CERT_IGNORE) ? TRUE : FALSE;
}

// rdp_wheel_step — кодирование колеса для RDP. Вращение в RDP лежит в младших
// 9 битах флагов указателя (FreeRDP: WheelRotationMask 0x01FF): биты 0..7 —
// величина, кратная 120 (WHEEL_DELTA), бит 0x0100 — «отрицательное вращение».
// Раньше выставлялся только бит знака, а величина терялась (ноль): вверх уходило
// вращение 0 (сервер не крутит), вниз — 0x100, который в маске читается как
// ненулевое вращение. Отсюда живой симптом «вниз работает, вверх нет».
// Накопление нужно для плавных устройств: приходит дробь щелчка.
// Возвращает 0, если целого щелчка ещё не набралось (событие не отправляем).
uint16_t rdp_wheel_step(int32_t *accum, int32_t dy)
{
    if (!accum) return 0;
    *accum += dy;
    int32_t notches = *accum / 120; // усечение к нулю — знак сохраняется
    if (notches == 0) return 0;
    if (notches > 2) notches = 2;   // 240 — максимум, влезающий в поле величины
    if (notches < -2) notches = -2;
    // Излишек от клампа НЕ выбрасываем, а оставляем в накопителе: канон FreeRDP
    // разбивает большую величину на несколько событий, а не срезает её.
    *accum -= notches * 120;
    uint16_t flags = PTR_FLAGS_WHEEL;
    if (notches < 0) flags |= PTR_FLAGS_WHEEL_NEGATIVE;
    const int32_t rot = (notches < 0 ? -notches : notches) * 120;
    // Отрицательное вращение — 9-битное ДОПОЛНЕНИЕ ДО ДВУХ, а не «знак+величина»
    // (FreeRDP client/X11/xf_client.c:1079-1083: `PTR_FLAGS_WHEEL_NEGATIVE |
    // (0x100 - 0x78)`). Сервер читает младшие 9 бит как знаковое число: для
    // -120 в поле лежит 0x88 (0x188 = 392 - 512 = -120).
    flags |= (uint16_t)((notches < 0 ? (0x100 - rot) : rot) & 0xFF);
    return flags;
}

// rdp_hwheel_step — горизонтальное колесо (M40-остаток): кодирование как у
// вертикального (9-битное дополнение до двух), но флаг PTR_FLAGS_HWHEEL;
// накапливание дробных щелчков — то же.
uint16_t rdp_hwheel_step(int32_t *accum, int32_t dx)
{
    const uint16_t f = rdp_wheel_step(accum, dx);
    if (f == 0) return 0;
    // Флаг оси ЗАМЕНЯЕМ: WHEEL(0x0200) → HWHEEL(0x0400); величина общая.
    return (uint16_t)((f & ~PTR_FLAGS_WHEEL) | PTR_FLAGS_HWHEEL);
}

// rdp_disp_resize_target — целевой размер десктопа для динамического разрешения
// (см. bridge.h). Физический размер виджета с клампом в протокольные пределы
// провода: сервер обязан отклонить layout с стороной вне 200..8192
// (DISPLAY_CONTROL_MIN/MAX_MONITOR_WIDTH/HEIGHT, freerdp/channels/disp.h).
// Верх совпадает с kMaxSidePx — кадр такого размера setFrameRect примет; низ
// важен на маленьких окнах (иначе сервер молча отвергнет запрос целиком).
int rdp_disp_resize_target(int widgetW, int widgetH, double dpr,
                           int curW, int curH, int *outW, int *outH)
{
    if (widgetW <= 0 || widgetH <= 0) return 0;
    if (dpr <= 0.0) dpr = 1.0; // DPR ещё неизвестен — логические пиксели как физические
    // Округление к ближайшему (у Qt размер логический, физический = ×DPR).
    int w = (int)(widgetW * dpr + 0.5);
    int h = (int)(widgetH * dpr + 0.5);
    if (w < DISPLAY_CONTROL_MIN_MONITOR_WIDTH) w = DISPLAY_CONTROL_MIN_MONITOR_WIDTH;
    if (h < DISPLAY_CONTROL_MIN_MONITOR_HEIGHT) h = DISPLAY_CONTROL_MIN_MONITOR_HEIGHT;
    if (w > DISPLAY_CONTROL_MAX_MONITOR_WIDTH) w = DISPLAY_CONTROL_MAX_MONITOR_WIDTH;
    if (h > DISPLAY_CONTROL_MAX_MONITOR_HEIGHT) h = DISPLAY_CONTROL_MAX_MONITOR_HEIGHT;
    if (w == curW && h == curH) return 0; // сервер уже рисует этот размер
    if (outW) *outW = w;
    if (outH) *outH = h;
    return 1;
}

int rdp_create(void) {
    int id;
    RdpCanvas* c;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        id = g_nextId++;
        c = new RdpCanvas(id);
        g_views[id] = c;
    }
    return id;
}

void* rdp_native(int id) {
    RdpCanvas* c = rdpViewFor(id);
    return c ? static_cast<void*>(c) : nullptr;
}

void rdp_start(int id, const char* host, int port, const char* user,
               const char* domain, const char* pass, const char* title,
               int w, int h, int bpp, int clipboard,
               unsigned int flags, int tlsSecLevel, const char* kbd,
               const char* sound,
               const char* const* driveShares, const char* const* drivePaths,
               int driveCount) {
    RdpCanvas* c = rdpViewFor(id);
    if (!c) return;
    // M39: копируем диски в std::vector на Qt-нити (строки Go освободит
    // вызывающий сразу после возврата rdp_start).
    std::vector<std::pair<std::string, std::string>> drives;
    if (driveShares && drivePaths && driveCount > 0) {
        drives.reserve((size_t)driveCount);
        for (int i = 0; i < driveCount; i++) {
            const char* sh = driveShares[i];
            const char* pa = drivePaths[i];
            if (sh && pa) drives.emplace_back(sh, pa);
        }
    }
    c->start(host ? host : "", port,
             user ? user : "", domain ? domain : "", pass ? pass : "",
             title ? title : "", w, h, bpp, clipboard, flags, tlsSecLevel,
             kbd ? kbd : "", sound ? sound : "", drives);
}

void rdp_stop(int id) {
    RdpCanvas* c = rdpViewFor(id);
    if (c) c->requestStop();
}

// M39-fullscreen: включить/выключить перехват Esc полноэкранного режима вкладки
// (см. bridge.h). Только Qt-нить.
void rdp_set_fullscreen(int id, int on) {
    RdpCanvas* c = rdpViewFor(id);
    if (c) c->setFullscreen(on != 0);
}

void rdp_destroy(int id) {
    RdpCanvas* c = nullptr;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        auto it = g_views.find(id);
        if (it != g_views.end()) { c = it->second; g_views.erase(it); }
    }
    if (!c) return;
    c->requestStop();
    // НЕ join'им здесь: worker может быть заблокирован в removeTab->Destroy
    // (mainthreadW) из самого worker-потока — синхронный join = взаимоблок.
    // deleteLater удалит канвас на Qt-ните ПОСЛЕ выхода worker (деструктор join).
    c->deleteLater();
}

} // extern "C"
