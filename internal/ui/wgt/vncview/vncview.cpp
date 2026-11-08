// VncCanvas — встроенный VNC-клиент (libvncclient 0.9.15) в Qt-канвасе вкладки.
// Потоки: Qt-нить создаёт канвас и получает события ввода; std::thread (worker)
// владеет сокетом и циклом (ConnectToRFBServer -> InitialiseRFBConnection ->
// WaitForMessage+HandleRFBServerMessage), копирует фреймбуфер [BGRA] в QImage и
// пробрасывает repaint на Qt-нить. Ввод от Qt-событий ставится в очередь и
// дренится worker-циклом (один поток на сокет — нет гонки чтение/запись).
#include <QWidget>
#include <QImage>
#include <QPainter>
#include <QPaintEvent>
#include <QMouseEvent>
#include <QKeyEvent>
#include <QEvent>
#include <QWheelEvent>
#include <QMetaObject>
#include <QString>
#include <QColor>
#include <QFont>
#include <QMutex>
#include <QMutexLocker>
#include <QGuiApplication>
#include <QClipboard>
#include <atomic>
#include <thread>
#include <mutex>
#include <chrono>
#include <deque>
#include <string>
#include <cstring>
#include <cstdlib>
#include <cstdint>
#include <cstdio>
#include <map>
#include <sys/socket.h>   // shutdown()/SHUT_RDWR — abort сокета по стоп (M33-г)
#include <sys/select.h>   // select() в прерываемом connect (см. connectTcpStopAware)
#include <netdb.h>        // getaddrinfo — свой connect вместо libvncclient
#include <errno.h>
#include <unistd.h>       // close()
#include <rfb/rfbclient.h>
#include <rfb/rfbproto.h> // rfbVeNCrypt/rfbVncAuth/rfbNoAuth — VeNCrypt-режим

#include "bridge.h"

// X11 keysym (XK_*) берутся из /usr/include/rfb/keysym.h, который включается
// транзитивно через <rfb/rfbclient.h>.

namespace {

// Таймаут TCP-connect VNC-клиента (сек): не зависнуть на недостижимом хосте вместо
// OS default ~2 мин (M33-г «разблокировка socket при закрытии вкладки»).
static const unsigned int kConnectTimeoutSec = 15;

// Верхняя граница фреймбуфера (байт) — защита от DoS/целочисленного переполнения:
// размер задаёт СЕРВЕР (u16 width/height) и по умолчанию считается в int.
// 8192x8192x4 (~256 МБ) покрывает даже 8K-экраны, но не даёт зарезервировать ~17 ГБ.
static const uint64_t kMaxFrameBytes = 8192ULL * 8192ULL * 4ULL;

struct InputMsg {
    enum Kind { Key, Ptr } kind;
    unsigned int k; int down;
    int x, y, mask;
};

// Переопределяемо через Q_OBJECT-квоты не нужно: ввод обрабатываем нативными
// надстроечными методами; repaint — через functor-invokeMethod (без moc).
// Тег для rfbClientSetClientData/rfbClientGetClientData: важен только адрес
// (уникальность), значение не используется.
static char g_selfTag;

class VncCanvas : public QWidget {
public:
    explicit VncCanvas(int id) : QWidget(), m_id(id), m_stop(false),
        m_reported(false), m_w(0), m_h(0) {
        setFocusPolicy(Qt::StrongFocus);
    }
    ~VncCanvas() override {
        requestStop();
        if (m_thread.joinable()) m_thread.join();
        // M33-г security: стереть пароль при уничтожении (менеджер учёток).
        m_pass.assign(m_pass.size(), '\0');
    }

    void start(const std::string& host, int port, const std::string& user,
               const std::string& pass, const std::string& title,
               const std::string& security) {
        // Ревью MINOR-6: защита от повторного вызова — второй std::thread в
        // joinable-поле это std::terminate, плюс дубль подписки на QClipboard.
        if (m_thread.joinable()) return;
        m_host = host; m_port = port; m_user = user; m_pass = pass; m_title = title;
        m_security = security;
        m_stop = false; m_reported = false;
        // M39: буфер обмена. Подписка на Qt-буфер — здесь, т.е. ОБЯЗАТЕЛЬНО из
        // Qt-нити (как и весь vnc_start): QClipboard живёт в Qt-нити, а контекст
        // соединения `this` отключает его автоматически при уничтожении канваса.
        // Для VNC буфер включаем всегда (в легаси-опциях vnc его не было).
        if (QClipboard* cb = QGuiApplication::clipboard())
            QObject::connect(cb, &QClipboard::dataChanged, this,
                             [this]{ onLocalClipChanged(); });
        m_thread = std::thread([this]{ run(); });
    }

    void requestStop() {
        m_stop = true;
        // M33-г «разблокировка socket при закрытии вкладки»: shutdown сокета прерывает
        // блокирующий read (handshake/цикл), чтобы worker вышел быстро, а не ждал
        // таймаут. Под m_sockMu (исключаем гонку с cleanup, который обнуляет m_client
        // перед rfbClientCleanup).
        std::lock_guard<std::mutex> lk(m_sockMu);
        if (m_client && m_client->sock != RFB_INVALID_SOCKET)
            shutdown(m_client->sock, SHUT_RDWR);
    }

    // M33-г: ошибка подключения рисуется на канвасе (вместо QMessageBox).
    void setError(const std::string& msg) {
        { std::lock_guard<std::mutex> lk(m_errMu); m_errMsg = msg; }
        QMetaObject::invokeMethod(this, [this]{ update(); }, Qt::QueuedConnection);
    }

    // join из Qt-нити безопасен: цикл итерации через WaitForMessage(200ms),
    // так что worker выходит в пределах ~одного таймаута (или после закрытия
    // сокета при disconnect). Повторный join (из деструктора) — no-op (guard).
    void joinThread() { if (m_thread.joinable()) m_thread.join(); }

    // M39-fullscreen: приложение вошло/вышло из полноэкранного режима вкладки
    // (см. vnc_set_fullscreen). Только Qt-нить.
    void setFullscreen(bool on) {
        m_fullscreen = on;
        if (!on) m_escHeld = false;
    }

    // worker-нить: копирует фреймбуфер в QImage, планирует repaint на Qt-нить.
    void setFrame(const uint8_t* bgra, int w, int h) {
        // M33-г security: не копировать фреймбуфер при гигантских размерах (страховка
        // поверх лимита в mallocFrame — сервер мог прислать огромные width/height).
        if (bgra && (uint64_t)w * (uint64_t)h * 4 > kMaxFrameBytes) return;
        {
            QMutexLocker lk(&m_imgMu);
            if (bgra && w > 0 && h > 0) {
                m_img = QImage(bgra, w, h, w * 4, QImage::Format_RGB32).copy();
                m_w = w; m_h = h;
            }
        }
        // А2: первый фреймбуфер = клиент подключился → сигнал «подключено» в Go
        // (иначе вкладка висит на «подключение…» — ревью #2).
        if (!m_frameReported && bgra && w > 0 && h > 0) {
            m_frameReported = true;
            vncGoFrame(m_id, w, h);
        }
        QMetaObject::invokeMethod(this, [this]{ update(); }, Qt::QueuedConnection);
    }

    void pushKey(unsigned int ksym, int down) {
        InputMsg m; m.kind = InputMsg::Key; m.k = ksym; m.down = down;
        QMutexLocker lk(&m_inMu); m_inputs.push_back(m);
    }
    void pushPtr(int x, int y, int mask) {
        InputMsg m; m.kind = InputMsg::Ptr; m.x = x; m.y = y; m.mask = mask;
        QMutexLocker lk(&m_inMu); m_inputs.push_back(m);
    }

protected:
    void paintEvent(QPaintEvent*) override {
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
        if (m_img.isNull()) { p.fillRect(rect(), QColor(0, 0, 0)); return; }
        // Сглаживание: без него масштабирование идёт ближайшим соседом и
        // изображение «квадратит» (как было в RDP). Пропорции пока НЕ сохраняем:
        // fx/fy (координаты ввода) рассчитаны на растяжение кадра на весь
        // виджет — переход к letterbox требует правки и fx/fy, а проверить это
        // без живого VNC-сервера нельзя (в бэклог).
        p.setRenderHint(QPainter::SmoothPixmapTransform, true);
        // Обрыв при показанном кадре: рисуем ч/б снапшот последнего кадра
        // (см. grayLastFrame), а не живой буфер — цветной кадр выглядел как
        // живая сессия при красном индикаторе вкладки (user-report 2026-10-04).
        if (m_gray && !m_imgGray.isNull()) {
            p.drawImage(rect(), m_imgGray);
        } else {
            p.drawImage(rect(), m_img);
        }
    }

    int fx(int x) const { return (width() > 0 && m_w > 0) ? x * m_w / width() : 0; }
    int fy(int y) const { return (height() > 0 && m_h > 0) ? y * m_h / height() : 0; }

    void mousePressEvent(QMouseEvent* e) override { sendMouse(e); }
    void mouseReleaseEvent(QMouseEvent* e) override { sendMouse(e); }
    void mouseMoveEvent(QMouseEvent* e) override { sendMouse(e); }
    void wheelEvent(QWheelEvent* e) override {
        // Ревью C (Б2, VNC — низший приоритет): горизонтальное колесо
        // (angleDelta.x при нулевом y) и pixel-only smooth-scroll
        // трактовались как «скролл вниз»; шлём только вертикаль с ненулевым y.
        if (e->angleDelta().y() == 0) return;
        int mask = (e->angleDelta().y() > 0) ? rfbButton4Mask : rfbButton5Mask;
        pushPtr(fx(e->position().x()), fy(e->position().y()), mask);
    }
    // Клавиатура сессии: см. подробный комментарий в rdpview.cpp — глобальные
    // QShortcut главного окна (WindowShortcut) срабатывают при активном окне
    // независимо от фокуса и съедают клавишу до канваса (F2 «переименовать»,
    // Ctrl+V/Ctrl+C из секции «Terminal»). ShortcutOverride — штатный способ
    // сказать Qt «отдай клавишу мне как обычное нажатие».
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
            vncGoFullscreen(m_id);
            return;
        }
        // Новый press обычной клавиши: «съеденный» ранее Esc уже не актуален —
        // иначе его поздний release мог бы проглотить release этой клавиши.
        m_escHeld = false;
        sendKey(e);
    }
    void keyReleaseEvent(QKeyEvent* e) override {
        if (m_escHeld && e->key() == Qt::Key_Escape) {
            m_escHeld = false;
            return;
        }
        sendKey(e);
    }

private:
    static int buttonsMask(Qt::MouseButtons b) {
        int m = 0;
        if (b & Qt::LeftButton) m |= rfbButton1Mask;
        if (b & Qt::RightButton) m |= rfbButton2Mask;
        if (b & Qt::MiddleButton) m |= rfbButton3Mask;
        return m;
    }
    void sendMouse(QMouseEvent* e) {
        pushPtr(fx(e->position().x()), fy(e->position().y()), buttonsMask(e->buttons()));
    }
    void sendKey(QKeyEvent* e) {
        pushKey(keysymFromKey(e), e->type() == QEvent::KeyPress ? 1 : 0);
    }
    // Ctrl+буква на НЕЛАТИНСКОЙ раскладке. e->key() — это символ активной
    // раскладки (на ЙЦУКЕН физическая клавиша A даёт Qt::Key_Ф), поэтому
    // диапазон Qt::Key_A..Z его не ловит, а text() при Ctrl непечатен: клавиша
    // молча терялась (keysym 0 — M39). Возвращаем Latin-позицию физической
    // клавиши по стандартной раскладке ЙЦУКЕН↔QWERTY (Windows/ГОСТ): сервер
    // применит зажатый Ctrl к базовому keysym буквы.
    //
    // Почему таблица, а не nativeScanCode/nativeVirtualKey: scanCode зависит от
    // платформы (X11 keycode = evdev+8, Wayland — evdev, Windows — scancode), а
    // nativeVirtualKey на X11/Wayland — это keysym ТЕКУЩЕЙ раскладки
    // (Cyrillic_ef для «ф»), т.е. ровно та же проблема, без живого сервера
    // переносимого «физического» кода Qt не даёт. ОГРАНИЧЕНИЕ: покрыта только
    // русская раскладка; украинская/греческая/арабская и т.п. остаются без
    // маппинга (прежнее поведение — keysym 0), пока не появятся проверенные
    // таблицы. Расширять наугад не стали.
    static int cyrillicLatinLetter(int key) {
        // e->key() для кириллицы — Unicode-код (0x0410..0x044F; ё/Ё сюда не
        // входят — это клавиша «`», латинской буквы у неё нет, поэтому не
        // мапим). Приводим к верхнему регистру и мапим на QWERTY-позицию.
        int cp = key;
        if (cp >= 0x0430 && cp <= 0x044F) cp -= 0x20; // а..я → А..Я
        switch (cp) {
            case 0x0419: return 'q'; // й
            case 0x0426: return 'w'; // ц
            case 0x0423: return 'e'; // у
            case 0x041A: return 'r'; // к
            case 0x0415: return 't'; // е
            case 0x041D: return 'y'; // н
            case 0x0413: return 'u'; // г
            case 0x0428: return 'i'; // ш
            case 0x0429: return 'o'; // щ
            case 0x0417: return 'p'; // з
            case 0x0424: return 'a'; // ф
            case 0x042B: return 's'; // ы
            case 0x0412: return 'd'; // в
            case 0x0410: return 'f'; // а
            case 0x041F: return 'g'; // п
            case 0x0420: return 'h'; // р
            case 0x041E: return 'j'; // о
            case 0x041B: return 'k'; // л
            case 0x0414: return 'l'; // д
            case 0x042F: return 'z'; // я
            case 0x0427: return 'x'; // ч
            case 0x0421: return 'c'; // с
            case 0x041C: return 'v'; // м
            case 0x0418: return 'b'; // и
            case 0x0422: return 'n'; // т
            case 0x042C: return 'm'; // ь
        }
        return 0;
    }
    unsigned int keysymFromKey(QKeyEvent* e) const {
        QChar c = e->text().isEmpty() ? QChar() : e->text().at(0);
        if (!c.isNull() && c.isPrint()) return (unsigned int)c.unicode();
        // M39 (BACKLOG «keysym Ctrl+буква»): при зажатом Ctrl e->text()
        // непечатен (), до switch дело не доходило — клавиша молча
        // терялась. VNC передаёт модификаторы ОТДЕЛЬНЫМИ событиями (Ctrl down
        // уже ушёл), поэтому достаточно БАЗОВОГО keysym буквы/цифры — сервер
        // применит зажатые модификаторы сам.
        if (e->modifiers() & Qt::ControlModifier) {
            const int k = e->key();
            if (k >= Qt::Key_0 && k <= Qt::Key_9) {
                return 0x30 + (unsigned)(k - Qt::Key_0);
            }
            int base = 0;
            if (k >= Qt::Key_A && k <= Qt::Key_Z) {
                base = 'a' + (k - Qt::Key_A);
            } else {
                base = cyrillicLatinLetter(k); // нелатинская (ЙЦУКЕН) раскладка
            }
            if (base != 0) {
                const bool upper = (e->modifiers() & Qt::ShiftModifier) != 0;
                return upper ? (unsigned)(base - 'a' + 'A') : (unsigned)base;
            }
        }
        switch (e->key()) {
            case Qt::Key_Backspace: return XK_BackSpace;
            case Qt::Key_Tab: return XK_Tab;
            case Qt::Key_Return: case Qt::Key_Enter: return XK_Return;
            case Qt::Key_Escape: return XK_Escape;
            case Qt::Key_Delete: return XK_Delete;
            case Qt::Key_Insert: return XK_Insert;
            case Qt::Key_Home: return XK_Home;
            case Qt::Key_End: return XK_End;
            case Qt::Key_PageUp: return XK_Page_Up;
            case Qt::Key_PageDown: return XK_Page_Down;
            case Qt::Key_Left: return XK_Left;
            case Qt::Key_Right: return XK_Right;
            case Qt::Key_Up: return XK_Up;
            case Qt::Key_Down: return XK_Down;
            case Qt::Key_Shift: return XK_Shift_L;
            case Qt::Key_Control: return XK_Control_L;
            case Qt::Key_Alt: return XK_Alt_L;
            case Qt::Key_AltGr: return XK_ISO_Level3_Shift;
            case Qt::Key_Meta: return XK_Meta_L;
            case Qt::Key_CapsLock: return XK_Caps_Lock;
            case Qt::Key_Super_L: return XK_Super_L;
            case Qt::Key_Super_R: return XK_Super_R;
            // M33-г «полировка ввода»: спец-клавиши, которых не было.
            case Qt::Key_ScrollLock: return XK_Scroll_Lock;
            case Qt::Key_NumLock: return XK_Num_Lock;
            case Qt::Key_Pause: return XK_Pause;
            case Qt::Key_Menu: return XK_Menu;
        }
        if (e->key() >= Qt::Key_F1 && e->key() <= Qt::Key_F12)
            return 0xffbe + (unsigned int)(e->key() - Qt::Key_F1);
        return 0;
    }

    // TCP-connect с прерыванием по m_stop. ConnectClientToTcpAddr6WithTimeout
    // отмену не умеет: сокет создаётся внутри и до возврата недоступен
    // requestStop, поэтому закрытие вкладки ждало бы до 15 с (kConnectTimeoutSec).
    // Делаем тот же non-blocking connect сами: ждём writable шагами по 200 мс и
    // на каждом шаге проверяем m_stop; по стопу сокет закрываем и выходим, не
    // начиная handshake (семантика как у freerdp_abort_connect_context в RDP).
    // ОГРАНИЧЕНИЕ: getaddrinfo (DNS) не прерывается — обычно это миллисекунды,
    // но на «висящем» резолвере ожидание возможно (как и в libvncclient).
    rfbSocket connectTcpStopAware() {
        struct addrinfo hints;
        memset(&hints, 0, sizeof(hints));
        hints.ai_family = AF_UNSPEC;
        hints.ai_socktype = SOCK_STREAM;
        char portbuf[16];
        snprintf(portbuf, sizeof(portbuf), "%d", m_port);
        struct addrinfo* res = nullptr;
        if (getaddrinfo(m_host.c_str(), portbuf, &hints, &res) != 0 || !res)
            return RFB_INVALID_SOCKET;
        // Тот же общий предел, что был у ConnectClientToTcpAddr6WithTimeout.
        const auto deadline = std::chrono::steady_clock::now() +
                              std::chrono::seconds(kConnectTimeoutSec);
        rfbSocket result = RFB_INVALID_SOCKET;
        for (struct addrinfo* ai = res; ai && !m_stop; ai = ai->ai_next) {
            rfbSocket sk = ::socket(ai->ai_family, ai->ai_socktype, ai->ai_protocol);
            if (sk == RFB_INVALID_SOCKET) continue;
            SetNonBlocking(sk);
            const int rc = ::connect(sk, ai->ai_addr, ai->ai_addrlen);
            bool connected = (rc == 0);
            if (rc < 0 && errno == EINPROGRESS) {
                while (!m_stop) {
                    if (std::chrono::steady_clock::now() >= deadline) break;
                    fd_set wfds;
                    FD_ZERO(&wfds);
                    FD_SET(sk, &wfds);
                    struct timeval tv;
                    tv.tv_sec = 0;
                    tv.tv_usec = 200000; // шаг проверки m_stop
                    const int sr = ::select((int)sk + 1, nullptr, &wfds, nullptr, &tv);
                    if (sr < 0) {
                        if (errno == EINTR) continue;
                        break;
                    }
                    if (sr == 0) continue; // шаг истёк — снова проверяем стоп
                    int err = 0;
                    socklen_t elen = sizeof(err);
                    if (::getsockopt(sk, SOL_SOCKET, SO_ERROR, &err, &elen) < 0 || err != 0)
                        break;
                    connected = true;
                    break;
                }
            }
            if (connected && !m_stop) { result = sk; break; }
            ::close(sk);
            if (m_stop) break;
        }
        freeaddrinfo(res);
        return result;
    }

    // --- worker: весь жизненный цикл клиента ---
    void run() {
        rfbClient* cl = rfbGetClient(8, 3, 4);
        if (!cl) { done(0, "rfbGetClient failed"); return; }
        cl->programName = "asbru-go";      // статическая строка, не освобождаем
        cl->serverPort = m_port;
        // connectTimeout — это таймаут select() ПОСЛЕ connect() (см. rfbclient.h),
        // он НЕ ограничивает блокирующий TCP-connect. Держим на дефолте (0).
        cl->connectTimeout = 0;
        // M33-г «разблокировка socket при закрытии вкладки»: ConnectClientToTcpAddr6-
        // WithTimeout ограничивает блокирующий connect() (не зависнуть на недостижимом
        // хосте вместо OS default ~2 мин), затем SetBlocking возвращает сокет в режим
        // как у дефолтного ConnectToRFBServer. readTimeout — bounded read для half-open/
        // handshake, чтобы worker не вешал на покинутом/подвисшем сокете.
        cl->readTimeout = 30;
        cl->canHandleNewFBSize = 1;
        // Пинним 32bpp true-colour [B,G,R,0] (BGRA) до handshake → QImage RGB32.
        cl->format.bitsPerPixel = 32;
        cl->format.depth = 24;
        cl->format.bigEndian = 0;
        cl->format.trueColour = 1;
        cl->format.redMax = 255; cl->format.greenMax = 255; cl->format.blueMax = 255;
        cl->format.redShift = 16; cl->format.greenShift = 8; cl->format.blueShift = 0;
        cl->MallocFrameBuffer = &VncCanvas::cbMalloc;
        cl->GetPassword = &VncCanvas::cbPassword;
        // M39: буфер обмена — оба колбэка (UTF-8 для ExtendedClipboard,
        // Latin-1 для серверов без него).
        cl->GotXCutTextUTF8 = &VncCanvas::cbCutTextUTF8;
        cl->GotXCutText = &VncCanvas::cbCutText;
        // Своё «я» для колбэков — через ШТАТНЫЙ API библиотеки, а НЕ через
        // cl->clientData: rfbClientCleanup обходит clientData как связный список
        // и free()-ит каждый узел (vncviewer.c), поэтому запись туда указателя на
        // QWidget приводила к free() живого объекта → «free(): invalid size» и
        // SIGABRT при завершении ЛЮБОЙ VNC-сессии (проверено опытом: контроль
        // выживает, с чужим указателем — abort 134; баг достался от M33).
        // rfbClientSetClientData сам аллоцирует узел и сам его освобождает.
        rfbClientSetClientData(cl, &g_selfTag, this);

        // VeNCrypt-режим (включаемая опция шифрования встроенного VNC).
        // libvncclient при сам собранном GnuTLS/OpenSSL сам согласует rfbVeNCrypt(19)
        // как «приемлемый», если clientAuthSchemes==NULL (дефолт): клиент берёт первый
        // приемлемый сервис-тип в списке сервера. Чтобы на ВЕРХНЕМ уровне форсировать
        // шифрование (и не деградировать в незашифрованный VncAuth/NoAuth) —
        // ограничиваем набор схем:
        //   auto     — NULL схем (текущее поведение: VeNCrypt, если сервер его шлёт;
        //              иначе VncAuth/NoAuth — без отказа от plain-коннектов);
        //   vencrypt — ТОЛЬКО rfbVeNCrypt на верхнем уровне: сервер без VeNCrypt на
        //              верхнем уровне не подключится. ВАЖНО: SetClientAuthSchemes НЕ
        //              гейтит внутренний выбор под-типа VeNCrypt (ReadVeNCryptSecurityType).
        //              Если сервер предложил VeNCrypt, но только без TLS-подтипов —
        //              см. fail-closed проверку после InitialiseRFBConnection (ниже),
        //              иначе VNC-пароль ушёл бы в plaintext (даунгрейд на под-уровне).
        //   none     — БЕЗ VeNCrypt (только VncAuth/NoAuth): явный запрет.
        applySecurity(cl);

        {
            const rfbSocket sk = connectTcpStopAware();
            if (sk == RFB_INVALID_SOCKET) {
                // Пустой reason при m_stop — отмена пользователем, а не ошибка.
                done(0, m_stop ? "" : "cannot connect");
                cleanup(cl);
                return;
            }
            SetBlocking(sk);
            if (m_stop) { // вкладку закрыли, пока шёл connect
                ::close(sk);
                done(0, "");
                cleanup(cl);
                return;
            }
            cl->sock = sk;
        }
        { std::lock_guard<std::mutex> lk(m_sockMu); m_client = cl; } // M33-г: abort по стоп
        // Stop мог прийти в окне между проверкой и публикацией m_client:
        // shutdown из requestStop прерывает и блокирующий handshake (recv).
        if (m_stop) { done(0, ""); cleanup(cl); return; }
        if (!InitialiseRFBConnection(cl)) {
            done(0, m_stop ? "" : "handshake failed");
            cleanup(cl);
            return;
        }
        // Стоп во время handshake, который всё же успел завершиться: отменённая
        // сессия не должна ни сообщать «подключено», ни слать кадр.
        if (m_stop) { done(0, ""); cleanup(cl); return; }

        // RECVIEW-РЕВЬЮ: vencrypt-режим обязан быть ШИФРОВАННЫМ. clientAuthSchemes
        // (SetClientAuthSchemes) гейтит только ВЕРХНЕУРОВНЕВЫЙ security-тип
        // (rfbclient.c:524); Внутренний выбор под-аутентификации VeNCrypt
        // (ReadVeNCryptSecurityType, tls_gnutls.c:322) его НЕ консультирует. Если сервер
        // предложил VeNCrypt(19), но в его списке под-типов только НЕзашифрованные
        // VncAuth(2)/NoAuth(1)/VeNCryptPlain(256) — HandleVeNCryptAuth возвращает TRUE
        // БЕЗ установки TLS (tls_gnutls.c:516-519), а HandleVncAuth шлёт пароль в
        // plaintext. Это MITM-доступно (под-список идёт ДО TLS). Фейлим закрыто:
        // если vencrypt, но TLS-сессия не поднята — отказываемся от соединения.
        // (Не может «отозвать» уже отправленный пароль — это detect-and-fail-closed,
        // для жёсткой гарантии нужен форк libvncclient; документируем в embed.go.)
        if (m_security == "vencrypt" && cl->tlsSession == nullptr) {
            done(0, "VeNCrypt: server offered no encrypted sub-auth (TLS)");
            cleanup(cl);
            return;
        }
        setFrame(cl->frameBuffer, cl->width, cl->height);

        bool ok = true;
        while (!m_stop) {
            drain(cl);
            sendClipboard(cl); // M39: локальный буфер → серверу
            int rc = WaitForMessage(cl, 200000); // 200ms: даёт дренить ввод на выходе
            if (rc < 0) { ok = false; break; }
            if (rc > 0) {
                if (!HandleRFBServerMessage(cl)) { ok = false; break; }
                if (cl->frameBuffer) setFrame(cl->frameBuffer, cl->width, cl->height);
            }
        }
        // Выход по m_stop — это отмена/закрытие вкладки, а не успешное
        // завершение: цикл выше выходит с ok=true, и раньше такая сессия
        // сообщала done(ok=1), т.е. «подключено» после стопа.
        if (m_stop) ok = false;
        done(ok ? 1 : 0, ok ? "" : "disconnected");
        cleanup(cl);
    }

    void drain(rfbClient* cl) {
        for (;;) {
            InputMsg m;
            {
                QMutexLocker lk(&m_inMu);
                if (m_inputs.empty()) return;
                m = m_inputs.front(); m_inputs.pop_front();
            }
            if (m.kind == InputMsg::Key) {
                if (m.k) SendKeyEvent(cl, m.k, m.down != 0);
            } else {
                SendPointerEvent(cl, m.x, m.y, m.mask);
            }
        }
    }

    // VeNCrypt-режим: настраивает набор сервис-типов (SetClientAuthSchemes).
    // Сравнение по точному значению; пусто/мусор → auto (дефолт, NULL схем —
    // libvncclient сам согласует VeNCrypt, если сервер его предлагает).
    void applySecurity(rfbClient* cl) {
        const std::string& s = m_security;
        if (s == "vencrypt") {
            const uint32_t schemes[] = { rfbVeNCrypt, 0 };
            SetClientAuthSchemes(cl, schemes, -1); // 0-терминатор, size<0 → посчитать
        } else if (s == "none") {
            const uint32_t schemes[] = { rfbVncAuth, rfbNoAuth, 0 };
            SetClientAuthSchemes(cl, schemes, -1);
        }
        // auto / "" — не трогаем: clientAuthSchemes остаётся NULL (дефолт).
    }

    // --- M39: буфер обмена (текст, оба направления) ---
    // Направление «локальный → сервер». Qt::dataChanged приходит В QT-НИТИ, а
    // SendClientCutText* можно звать только из нити-воркера (libvncclient не
    // потокобезопасен) — поэтому текст кладём в очередь под mutex, а воркер
    // отправляет его в своей итерации (латентность ≤200 мс, как у ввода).
    void onLocalClipChanged() {
        QClipboard* cb = QGuiApplication::clipboard();
        if (!cb) return;
        const QString t = cb->text();
        if (t.isEmpty()) return; // очистку буфера не транслируем
        // Эхо: ровно этот текст мы только что выставили из сервера — обратно не
        // шлём. Сверка СОДЕРЖИМОГО, а не одноразовый латч: латч «съел» бы
        // следующее настоящее копирование пользователя, если платформа не
        // эмитит dataChanged на наш setText (такой путь есть, напр. Wayland без
        // seat), и не подавил бы повторный сигнал на одно и то же значение.
        if (t == m_lastRemote) return;
        const QByteArray u8 = t.toUtf8();
        // Фолбэк Latin-1 (plain ClientCutText) имеет смысл только если текст в
        // Latin-1 ПРЕДСТАВИМ: иначе на сервер ушли бы '?' — необратимая порча
        // (повтора не будет, буфер уже сброшен), поэтому такой фолбэк не шлём.
        const bool latin1ok = isLatin1Text(t);
        const QByteArray l1 = latin1ok ? t.toLatin1() : QByteArray();
        // Last-wins: неотправленное перезаписывается — мосту нужен конечный
        // текст буфера, а не история (в отличие от очереди ввода m_inputs).
        QMutexLocker lk(&m_clipMu);
        m_clipUtf8.assign(u8.constData(), (size_t)u8.size());
        m_clipLatin1.assign(l1.constData(), (size_t)l1.size());
        m_clipLatin1Ok = latin1ok;
        m_clipHave = true;
    }
    // Воркер: отдать накопленный локальный буфер серверу.
    void sendClipboard(rfbClient* cl) {
        std::string u8, l1;
        bool latin1ok = false;
        {
            QMutexLocker lk(&m_clipMu);
            if (!m_clipHave) return;
            u8.swap(m_clipUtf8); l1.swap(m_clipLatin1);
            latin1ok = m_clipLatin1Ok;
            m_clipHave = false;
        }
        if (u8.empty()) return;
        // UTF-8 понимает только сервер с ExtendedClipboard — иначе Latin-1
        // (документация SendClientCutTextUTF8 прямо предписывает этот фолбэк).
        char* p = const_cast<char*>(u8.data());
        if (SendClientCutTextUTF8(cl, p, (int)u8.size())) return;
        // Фолбэк только для Latin-1-представимого текста (см. onLocalClipChanged).
        if (!latin1ok || l1.empty()) return;
        SendClientCutText(cl, const_cast<char*>(l1.data()), (int)l1.size());
    }
    // Направление «сервер → локальный». Колбэк зовётся В НИТИ-ВОРКЕРЕ, поэтому
    // в Qt-буфер пишем только через invokeMethod с контекстом `this` (вызов
    // отменяется, если канвас успели уничтожить).
    static void cbCutText(rfbClient* cl, const char* text, int len) {
        deliverCutText(cl, text, len, false);
    }
    static void cbCutTextUTF8(rfbClient* cl, const char* text, int len) {
        deliverCutText(cl, text, len, true);
    }
    static void deliverCutText(rfbClient* cl, const char* text, int len, bool utf8) {
        VncCanvas* self = selfOf(cl);
        if (!self || !text || len <= 0) return;
        const QByteArray data(text, len);
        const QString s = utf8 ? QString::fromUtf8(data) : QString::fromLatin1(data);
        QMetaObject::invokeMethod(self, [self, s]{
            QClipboard* cb = QGuiApplication::clipboard();
            if (!cb) return;
            // Запомнили, что этот текст пришёл от сервера: dataChanged на него
            // будет проигнорирован (сверка содержимого в onLocalClipChanged).
            // Поле трогается только из Qt-нити — мьютекс не нужен.
            self->m_lastRemote = s;
            cb->setText(s);
        }, Qt::QueuedConnection);
    }

    // Представим ли текст в Latin-1 (все кодовые единицы <= 0xFF): только тогда
    // осмыслен фолбэк plain ClientCutText, иначе на провод ушли бы '?'.
    // (QString::isLatin1() есть не во всех сборках Qt6 — проверяем сами.)
    static bool isLatin1Text(const QString& s) {
        const ushort* u = s.utf16(); // в этой сборке Qt6 utf16() → const ushort*
        for (qsizetype i = 0; i < s.size(); i++) {
            if (u[i] > 0xFF) return false;
        }
        return true;
    }

    // Своё «я» из колбэка (см. rfbClientSetClientData в run()).
    static VncCanvas* selfOf(rfbClient* cl) {
        return cl ? (VncCanvas*)rfbClientGetClientData(cl, &g_selfTag) : nullptr;
    }

    static rfbBool cbMalloc(rfbClient* cl) {
        VncCanvas* self = selfOf(cl);
        return self ? self->mallocFrame(cl) : 0;
    }
    rfbBool mallocFrame(rfbClient* cl) {
        if (!cl) return 0;
        // M33-г security: размер фреймбуфера приходит от СЕРВЕРА (u16 up to 65535),
        // а при bpp=32 считается в int — cl->width*cl->height*4 переполняется
        // (напр. 32768x32769 -> 131072) и calloc выделит слишком мало → heap overflow.
        // Считаем в 64-бит и ограничиваем сверху (защита и от DoS-аллокации ~17 ГБ).
        uint64_t sz = (uint64_t)cl->width * (uint64_t)cl->height * 4ULL;
        if (cl->width <= 0 || cl->height <= 0 || sz == 0 || sz > kMaxFrameBytes) return 0;
        if (cl->frameBuffer) { free(cl->frameBuffer); cl->frameBuffer = nullptr; }
        cl->frameBuffer = (uint8_t*)calloc((size_t)sz, 1);
        return cl->frameBuffer ? TRUE : FALSE;
    }
    static char* cbPassword(rfbClient* cl) {
        VncCanvas* self = selfOf(cl);
        return strdup(self ? self->m_pass.c_str() : "");
    }

    void cleanup(rfbClient* cl) {
        if (!cl) return;
        { std::lock_guard<std::mutex> lk(m_sockMu); m_client = nullptr; } // M33-г: stop не тронет cl
        if (cl->frameBuffer) { free(cl->frameBuffer); cl->frameBuffer = nullptr; }
        rfbClientCleanup(cl);
    }

    void done(int ok, const char* reason) {
        if (m_reported.exchange(true)) return;
        char buf[256];
        snprintf(buf, sizeof(buf), "%s", reason ? reason : "");
        // M33-г: ошибку рисуем на канвасе только если НИКОГДА не подключались (нет
        // кадра). Если кадр уже был, а соединение оборвалось — оставляем последний
        // кадр; статус виден в заголовке вкладки.
        if (!ok && !m_frameReported && reason && reason[0]) setError(reason);
        // Тот же обрыв: кадр остаётся, но ч/б — цветной неотличим от живой сессии
        // при красном индикаторе вкладки (user-report 2026-10-04).
        if (!ok && m_frameReported) grayLastFrame();
        vncGoDone(m_id, ok, buf);
    }

    // Снапшот последнего кадра в градациях серого (asbru_gray_frame, ../grayframe.h).
    // Снапшот, а НЕ обесцвечивание m_img на месте: setFrame кладёт в m_img новый
    // кадр до самого выхода воркера — поздний кадр не должен вернуть цвет; кроме
    // того, paintEvent читает m_img на Qt-нити, поэтому и снапшот, и флаг красим
    // под m_imgMu.
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
    bool m_frameReported = false; // А2: первый кадр → Go-сигнал «подключено»
    std::string m_host, m_user, m_pass, m_title;
    std::string m_security; // VeNCrypt-режим: "auto"(дефолт)/"vencrypt"/"none"
    int m_port;
    std::atomic<int> m_w, m_h;   // M33-г: атомик (гонка worker-setFrame/Qt-fx,fy)
    std::mutex m_sockMu;         // M33-г: гвардия m_client (гонка stop/cleanup)
    rfbClient* m_client = nullptr; // для abort-connect (shutdown socket) при stop
    std::mutex m_errMu;          // M33-г: гвардия m_errMsg (гонка worker/paint)
    std::string m_errMsg;        // M33-г: текст ошибки на канвасе
    QMutex m_imgMu;
    QImage m_img;
    // Обрыв при показанном кадре: ч/б снапшот последнего кадра — рисуется вместо
    // m_img (см. grayLastFrame). Под m_imgMu: пишет done (воркер), читает paintEvent.
    QImage m_imgGray;
    bool m_gray = false;
    QMutex m_inMu;
    std::deque<InputMsg> m_inputs;
    // M39: буфер обмена. m_clipMu — гвардия кэша (m_clipUtf8/m_clipLatin1/
    // m_clipLatin1Ok/m_clipHave): пишет Qt-нить, читает воркер. Гашение эха —
    // сверкой с m_lastRemote (см. onLocalClipChanged), а не латчем.
    QMutex m_clipMu;
    std::string m_clipUtf8, m_clipLatin1;
    bool m_clipLatin1Ok = false;
    bool m_clipHave = false;
    // Текст, пришедший от сервера, — для гашения эха сверкой содержимого.
    // Только Qt-нить (и запись, и чтение в onLocalClipChanged).
    QString m_lastRemote;
    // M39-fullscreen: полноэкранный режим вкладки. Пока включён, одиночный Esc
    // перехватывается (vncGoFullscreen) и не уходит в сессию; m_escHeld — чтобы
    // «съеденный» press не дал серверу парный release.
    bool m_fullscreen = false;
    bool m_escHeld = false;
};

std::mutex g_mu;
std::map<int, VncCanvas*> g_views;
// Свой диапазон id (см. widget.cpp) — иначе коллизия с терминалами/RDP.
int g_nextId = 2000000;

VncCanvas* viewFor(int id) {
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_views.find(id);
    return it == g_views.end() ? nullptr : it->second;
}

} // namespace

extern "C" {

int vnc_create(void) {
    int id;
    VncCanvas* c;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        id = g_nextId++;
        c = new VncCanvas(id);
        g_views[id] = c;
    }
    return id;
}

void* vnc_native(int id) {
    VncCanvas* c = viewFor(id);
    return c ? static_cast<void*>(c) : nullptr;
}

void vnc_start(int id, const char* host, int port, const char* user,
               const char* pass, const char* title, const char* security) {
    VncCanvas* c = viewFor(id);
    if (!c) return;
    // copy сразу, исходные C-строки Go освободит после вызова.
    c->start(host ? host : "", port,
             user ? user : "", pass ? pass : "", title ? title : "",
             security ? security : "auto");
}

void vnc_stop(int id) {
    VncCanvas* c = viewFor(id);
    if (c) c->requestStop();
}

// M39-fullscreen: включить/выключить перехват Esc полноэкранного режима вкладки
// (см. bridge.h). Только Qt-нить.
void vnc_set_fullscreen(int id, int on) {
    VncCanvas* c = viewFor(id);
    if (c) c->setFullscreen(on != 0);
}

void vnc_destroy(int id) {
    VncCanvas* c = nullptr;
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
