// TerminalWidget — QTermWidget в телетайп-режиме: сессия Ásbrú (Go)
// пишет вывод в pty-slave виджета, нажатия возвращает сигнал sendData.
#include <unistd.h>
#include <sys/ioctl.h>
#include <map>
#include <mutex>
#include <QString>
#include <QByteArray>
#include <QMenu>
#include <QPoint>
#include <QContextMenuEvent>
#include <QWheelEvent>
#include <QApplication>
#include <QFontInfo>
#include <QFontDatabase>
#include <qtermwidget6/qtermwidget.h>

#include "bridge.h"

// monoFont — гарантированный моно-шрифт. QFontDatabase::systemFont(FixedFont)
// на части систем возвращает НЕ моно (напр. «Noto Sans») → QTermWidget ругается
// «variable-width font». Перебираем известные моно-семьи, берём первую
// fixedPitch; иначе фолбэк на системный FixedFont + TypeWriter.
static QFont monoFont()
{
    static const char *families[] = {
        "Consolas", "DejaVu Sans Mono", "Liberation Mono", "Noto Sans Mono",
        "JetBrains Mono", "Fira Mono", "Source Code Pro", "monospace",
    };
    for (const char *fam : families) {
        QFont f(QString::fromLatin1(fam));
        f.setStyleHint(QFont::TypeWriter);
        if (QFontInfo(f).fixedPitch())
            return f;
    }
    QFont f = QFontDatabase::systemFont(QFontDatabase::FixedFont);
    f.setStyleHint(QFont::TypeWriter);
    return f;
}

namespace {

struct TermHandle {
    QTermWidget *widget;
    qtw_resize_cb on_resize;
    int slaveFd;
    qtw_data_cb on_data;
    qtw_title_cb on_title;
    qtw_closed_cb on_closed;
    qtw_menu_cb on_menu; // M28: правый клик (контекстное меню)
};

std::map<int, TermHandle> g_handles;
std::mutex g_mu; // защита g_handles: qtw_feed зовётся из Go-горутин (ревью H2)
// Диапазоны id РАЗНЫЕ у терминалов, RDP- и VNC-канвасов: три независимых
// счётчика стартовали с 1, Go-сторона кладёт id в общие tabID/tabEmbed —
// записи затирали друг друга (симптом: вкладка не закрывается, останавливался
// чужой клиент). См. также rdpview/vncview.
constexpr int kTermIdBase = 1;
int g_nextId = kTermIdBase;

class TerminalWidget : public QTermWidget
{
    Q_OBJECT
public:
    TerminalWidget() : QTermWidget(0, nullptr)
    {
        // Очный дебаг «variable-width font in the terminal»: QTermWidget ругается,
        // пока не применился qtw_set_font (после создания). Ставим моно-шрифт
        // сразу — иначе в начале сессии несколько предупреждений в терминал.
        setTerminalFont(monoFont());
    }
    ~TerminalWidget() override = default;

    int registeredId = -1;

protected:
    void resizeEvent(QResizeEvent *e) override
    {
        QTermWidget::resizeEvent(e);
        emitResize(); // размер эмулятора уже применён к его pty — зеркалим в сессию (M11)
    }

private:
    void emitResize()
    {
        int fd = getPtySlaveFd();
        struct winsize ws = {};
        if (fd >= 0 && ioctl(fd, TIOCGWINSZ, &ws) == 0) {
            std::lock_guard<std::mutex> lk(g_mu);
            auto it = g_handles.find(registeredId);
            if (it != g_handles.end() && it->second.on_resize)
                it->second.on_resize(registeredId, ws.ws_col, ws.ws_row);
        }
    }

private slots:
    void fwdData(const char *data, int n)
    {
        std::lock_guard<std::mutex> lk(g_mu);
        auto it = g_handles.find(registeredId);
        if (it != g_handles.end() && it->second.on_data)
            it->second.on_data(registeredId, data, n);
    }
    void fwdTitle()
    {
        std::lock_guard<std::mutex> lk(g_mu);
        auto it = g_handles.find(registeredId);
        if (it != g_handles.end() && it->second.on_title) {
            QByteArray utf8 = title().toUtf8(); // текущий заголовок (OSC-титул)
            it->second.on_title(registeredId, utf8.constData());
        }
    }
    void fwdFinished()
    {
        std::lock_guard<std::mutex> lk(g_mu);
        auto it = g_handles.find(registeredId);
        if (it != g_handles.end() && it->second.on_closed)
            it->second.on_closed(registeredId);
    }
    // M28: правый клик → колбэк для контекстного меню.
    void fwdMenu(const QPoint &p)
    {
        // Ревью M28 C1: НЕ держим g_mu при вызове on_menu — вложенный
        // modal-exec QMenu + qtw_copy ре-аквирит тот же mutex на этой нити
        // (нерекурсивный std::mutex → дедлок). Копируем колбэк под замком,
        // вызываем вне.
        qtw_menu_cb cb = nullptr;
        {
            std::lock_guard<std::mutex> lk(g_mu);
            auto it = g_handles.find(registeredId);
            if (it != g_handles.end())
                cb = it->second.on_menu;
        }
        if (cb) {
            // Ревью M28 I1 + user-тест #2: через event filter мы получаем
            // QContextMenuEvent::globalPos() — уже глобальные (QMenu::exec).
            cb(registeredId, p.x(), p.y());
        }
    }

private:
    // m_pixelAccum — накопленные пиксели плавного скролла (см. eventFilter).
    double m_pixelAccum = 0;

public:
    // user-тест #2: правый клик ловится event filter'ом на обёртке И её детях
    // (внутренний TerminalDisplay QTermWidget съедает QContextMenuEvent, поэтому
    // customContextMenuRequested обёртки не срабатывает). Глобальные коорд.
    bool eventFilter(QObject *watched, QEvent *ev) override
    {
        // M44: плавная прокрутка (тачпад, hi-res колесо, Wayland) приходит
        // событиями БЕЗ angleDelta — только с pixelDelta. QTermWidget такие не
        // разбирает: Konsole::TerminalDisplay::wheelEvent первой строкой делает
        //   if (ev->angleDelta().y() == 0) return;      // TerminalDisplay.cpp:2729
        // то есть дальше (история / синтез стрелок для TUI / mouse-reporting,
        // там же 2741-2758) такое событие не идёт. Живьём это значит: на
        // плавном устройстве колесо не прокручивает вообще ничего. Копим
        // пиксели и раз в «щелчок» (высота строки × строк на щелчок) отдаём
        // эквивалент дискретного angleDelta=±120 — дальше работает штатная
        // логика виджета, включая TUI-мышь и режим без истории.
        // Дискретное колесо (angleDelta != 0) не затронуто.
        if (ev->type() == QEvent::Wheel) {
            auto *we = static_cast<QWheelEvent*>(ev);
            if (we->angleDelta().isNull() && !we->pixelDelta().isNull()) {
                QWidget *w = qobject_cast<QWidget*>(watched);
                const int lineH = w ? qMax(1, w->fontMetrics().height()) : 16;
                int wheel = QApplication::wheelScrollLines();
                if (wheel < 1) wheel = 1;
                const int step = lineH * wheel;
                m_pixelAccum += we->pixelDelta().y();
                const int notches = int(m_pixelAccum / step);
                m_pixelAccum -= notches * step;
                if (notches != 0) {
                    QWheelEvent tr(we->position(), we->globalPosition(), QPoint(),
                                   QPoint(0, notches * 120), we->buttons(), we->modifiers(),
                                   we->phase(), we->inverted(), we->source(), we->pointingDevice());
                    QApplication::sendEvent(watched, &tr);
                }
                // Граница жеста — остаток (до step-1 px) не переносим в следующий
                // жест: сбрасываем ПОСЛЕ обработки кадра, иначе кадр с phase=End,
                // несущий свой pixelDelta, сам же и заводит остаток. На X11 и у
                // большинства мышей phase = NoScrollPhase — там сброса нет.
                if (we->phase() == Qt::ScrollBegin || we->phase() == Qt::ScrollEnd) {
                    m_pixelAccum = 0;
                }
                // Поглощаем в любом случае: до нас такое событие не доходило ни
                // до виджета, ни до родителя. Горизонтальный pixel-only скролл
                // (pixelDelta.x при нулевом y) эффекта не даёт — как и апстрим,
                // который такой кадр не пробрасывает дальше.
                return true;
            }
        }
        if (ev->type() == QEvent::ContextMenu) {
            auto *ce = static_cast<QContextMenuEvent*>(ev);
            fwdMenu(ce->globalPos());
            return true; // событие обработано, не даём дефолтное меню/проброс
        }
        return QTermWidget::eventFilter(watched, ev);
    }
};

} // namespace

extern "C" {

int qtw_create(int start_teletype,
               qtw_data_cb on_data, qtw_title_cb on_title, qtw_closed_cb on_closed,
               qtw_resize_cb on_resize)
{
    TerminalWidget *w = new TerminalWidget();
    int id;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        id = g_nextId++; // замок только на выделение id и вставку
    }
    w->registeredId = id;

    TermHandle h;
    h.widget = w;
    h.slaveFd = -1;
    h.on_data = on_data;
    h.on_title = on_title;
    h.on_closed = on_closed;
    h.on_resize = on_resize;
    h.on_menu = nullptr;
    if (start_teletype) {
        w->startTerminalTeletype(); // вне замка: сигналы не должны мёртвить
        h.slaveFd = ::dup(w->getPtySlaveFd());
    }
    {
        std::lock_guard<std::mutex> lk(g_mu);
        g_handles[id] = h;
    }

    // Классический macro-SIGNAL: без шаблонного Functor-вывода.
    QObject::connect(w, SIGNAL(sendData(const char*,int)), w, SLOT(fwdData(const char*,int)));
    // QTermWidget::titleChanged() — БЕЗ аргументов (иначе «No such signal
    // TerminalWidget::titleChanged(QString)»); текущий заголовок берём title().
    QObject::connect(w, SIGNAL(titleChanged()), w, SLOT(fwdTitle()));
    QObject::connect(w, SIGNAL(finished()), w, SLOT(fwdFinished()));
    // M28 / user-тест #2: правый клик → контекстное меню. Ловим event filter'ом
    // на обёртке И её детях: внутренний TerminalDisplay QTermWidget съедает
    // QContextMenuEvent, поэтому customContextMenuRequested обёртки не срабатывает.
    w->installEventFilter(w);
    for (QWidget *ch : w->findChildren<QWidget*>())
        ch->installEventFilter(w);
    return id;
}

void qtw_set_menu_cb(int id, qtw_menu_cb cb)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.on_menu = cb;
}

void qtw_feed(int id, const char *data, int n)
{
    int fd = -1;
    {
        std::lock_guard<std::mutex> lk(g_mu);
        auto it = g_handles.find(id);
        if (it == g_handles.end() || it->second.slaveFd < 0)
            return;
        // dup ПОД замком: qtw_destroy может закрыть (и ОС — переиспользовать
        // номер) slaveFd сразу после разблокировки. Копия ссылается на тот же
        // открытый pty-slave, но имеет СВОЙ дескриптор — записать в чужой
        // переиспользованный fd невозможно. Держать g_mu на всё write нельзя:
        // write в pty блокируется при полном буфере, а qtw_destroy ждёт замок.
        fd = ::dup(it->second.slaveFd);
    }
    if (fd < 0)
        return;
    // Полная запись: частичные write и EINTR не теряют вывод (ревью H1).
    const int chunk = 4096;
    int off = 0;
    while (off < n) {
        int want = (n - off < chunk) ? (n - off) : chunk;
        ssize_t w = ::write(fd, data + off, (size_t)want);
        if (w > 0) {
            off += (int)w;
        } else if (w < 0 && errno == EINTR) {
            continue;
        } else {
            break; // EAGAIN/закрыто: не зависаем, оставшийся кусок отбрасывается
        }
    }
    ::close(fd);
}

void qtw_attach(int id, void *parent_native)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    QWidget *p = reinterpret_cast<QWidget *>(parent_native);
    it->second.widget->setParent(p);
}

void *qtw_native(int id)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return nullptr;
    return it->second.widget;
}

void qtw_show(int id)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setGeometry(100, 100, 800, 500);
    it->second.widget->show();
}

void qtw_set_size(int id, int /*cols*/, int /*rows*/)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setTerminalSizeHint(true);
}

void *qtw_menu_new(void)
{
    return new QMenu();
}

void *qtw_menu_item(void *menu, const char *text)
{
    QMenu *m = reinterpret_cast<QMenu *>(menu);
    QAction *a = m->addAction(QString::fromUtf8(text));
    return a;
}

// М36: подменю (вложенный QMenu) — пункты добавляются через qtw_menu_item(подменю).
void *qtw_menu_submenu(void *menu, const char *title)
{
    QMenu *m = reinterpret_cast<QMenu *>(menu);
    return m->addMenu(QString::fromUtf8(title));
}

void *qtw_menu_exec(void *menu, int x, int y)
{
    QMenu *m = reinterpret_cast<QMenu *>(menu);
    QAction *a = m->exec(QPoint(x, y));
    return a;
}

void qtw_menu_free(void *menu)
{
    delete reinterpret_cast<QMenu *>(menu);
}

void qtw_destroy(int id)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    if (it->second.slaveFd >= 0)
        ::close(it->second.slaveFd);
    it->second.widget->deleteLater();
    g_handles.erase(it);
}

// M27: Look&Feel (все вызовы on Qt-ните; g_mu защищает g_handles).
void qtw_set_font(int id, const char *font)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    QString spec = (font && *font) ? QString::fromUtf8(font).trimmed() : QString();
    // Пустая/незаданная спецификация → системный моно-шрифт (иначе QTermWidget
    // берёт переменно-ширинный дефолт и ругается: «variable-width font»).
    QFont f;
    if (spec.isEmpty()) {
        it->second.widget->setTerminalFont(monoFont());
        return;
    }
    // Робастный разбор "Family Name 12": семья (в т.ч. многословная) + размер,
    // иначе вся строка — семья. user-тест #3 (SSH: наложение — не-моно шрифт).
    int sp = spec.lastIndexOf(' ');
    bool szOk = false;
    int sz = 0;
    if (sp > 0) {
        sz = spec.mid(sp + 1).toInt(&szOk);
        if (szOk && sz > 0)
            f.setFamily(spec.left(sp));
        else
            f.setFamily(spec);
    } else {
        f.setFamily(spec);
    }
    if (szOk && sz > 0)
        f.setPointSize(sz);
    // Гарантируем моно-шрифт (у QTermWidget метрики ячеек по fixedPitch): если
    // семья не fixed-pitch — системный моно-шрифт (наследование дефолтного профиля).
    if (!QFontInfo(f).fixedPitch())
        f = monoFont();
    f.setStyleHint(QFont::TypeWriter);
    it->second.widget->setTerminalFont(f);
}

void qtw_set_colorscheme(int id, const char *name)
{
    if (!name || !*name)
        return;
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setColorScheme(QString::fromUtf8(name));
}

void qtw_set_cursor_shape(int id, int shape)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setKeyboardCursorShape(
        static_cast<Konsole::Emulation::KeyboardCursorShape>(shape));
}

void qtw_set_history_size(int id, int lines)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setHistorySize(lines);
}

void qtw_set_scrollbar(int id, int pos)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setScrollBarPosition(
        static_cast<QTermWidgetInterface::ScrollBarPosition>(pos));
}

void qtw_set_opacity(int id, double level)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setTerminalOpacity(level);
}

void qtw_set_word_chars(int id, const char *chars)
{
    if (!chars || !*chars)
        return;
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setWordCharacters(QString::fromUtf8(chars));
}

void qtw_set_auto_close(int id, int v)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->setAutoClose(v == 1);
}

// M28: копирование выделения / вставка из буфера (QTermWidget API).
void qtw_copy(int id)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->copyClipboard();
}

void qtw_paste(int id)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->pasteClipboard();
}

// M36: показать поисковую панель QTermWidget (toggleShowSearchBar — public slot).
void qtw_find(int id)
{
    std::lock_guard<std::mutex> lk(g_mu);
    auto it = g_handles.find(id);
    if (it == g_handles.end())
        return;
    it->second.widget->toggleShowSearchBar();
}

} // extern "C"

#include "widget.moc"
