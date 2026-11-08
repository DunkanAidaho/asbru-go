#ifndef ASBRU_QT_WIDGET_BRIDGE_H
#define ASBRU_QT_WIDGET_BRIDGE_H

// Тонкий C-мост к QTermWidget (телетайп-режим, P0 по плану M8):
// вывод сессии пишется в pty-slave виджета, нажатия пользователя
// возвращаются сигналом sendData.

#ifdef __cplusplus
extern "C" {
#endif

typedef void (*qtw_data_cb)(int id, const char *data, int n);
typedef void (*qtw_title_cb)(int id, const char *title);
typedef void (*qtw_closed_cb)(int id);
typedef void (*qtw_resize_cb)(int id, int cols, int rows);
// M28: правый клик по терминалу (для контекстного меню).
typedef void (*qtw_menu_cb)(int id, int x, int y);

// Создать TelTerminalWidget; все колбэки вызываются из Qt-нити.
int  qtw_create(int start_teletype, qtw_data_cb on_data, qtw_title_cb on_title, qtw_closed_cb on_closed, qtw_resize_cb on_resize);
// Вывод сессии → в эмулятор (запись в pty-slave).
void qtw_feed(int id, const char *data, int n);
// Родитель (QWidget*) из Qt-дерева miqt.
void qtw_attach(int id, void *parent_native);
// Нативный указатель виджета (для обёртки miqt / вставки во вкладки).
void *qtw_native(int id);
// Киношный мини-меню (miqt-обёртка не имеет QMenu::addAction): пункты
// добавляются и exec-ится с позиции; возврат — выбранный QAction* или null.
void *qtw_menu_new(void);
void *qtw_menu_item(void *menu, const char *text);
void *qtw_menu_submenu(void *menu, const char *title); // М36: вложенное подменю
void *qtw_menu_exec(void *menu, int x, int y);
void qtw_menu_free(void *menu);
// Показать терминал отдельным окном (top-level) — для e2e до вкладок.
void qtw_show(int id);
void qtw_set_size(int id, int cols, int rows);
void qtw_destroy(int id);

// M27: Look&Feel (QTermWidget API) — примитивные аргументы, вызовы с Qt-нити.
void qtw_set_font(int id, const char *font);          // "Monospace 9"
void qtw_set_colorscheme(int id, const char *name);   // имя или путь .colorscheme
void qtw_set_cursor_shape(int id, int shape);          // 0=block,1=underline,2=ibeam
void qtw_set_history_size(int id, int lines);          // строк истории
void qtw_set_scrollbar(int id, int pos);               // 0=no,1=left,2=right
void qtw_set_opacity(int id, double level);            // 0..1
void qtw_set_word_chars(int id, const char *chars);    // символы «слова»
void qtw_set_auto_close(int id, int v);                // 0/1

// M28: поведение терминала (копирование/вставка из буфера обмена).
void qtw_copy(int id);    // copy selection → clipboard (QTermWidget::copyClipboard)
void qtw_paste(int id);   // paste clipboard → terminal (QTermWidget::pasteClipboard)
void qtw_find(int id);    // показать поисковую панель (QTermWidget::toggleShowSearchBar)
// Установить колбэк контекстного меню (правый клик; вызывается с Qt-нити).
void qtw_set_menu_cb(int id, qtw_menu_cb cb);

#ifdef __cplusplus
}
#endif

#endif
