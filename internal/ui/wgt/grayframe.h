#ifndef ASBRU_GRAY_FRAME_H
#define ASBRU_GRAY_FRAME_H

#include <stdint.h>

// Обрыв сессии RDP/VNC: последний кадр остаётся во вкладке (M33-г), но цветной
// кадр неотличим от живого подключения — при красном индикаторе вкладки сессия
// кажется живой (user-report 2026-10-04). Кадр на канвасе переводится в
// градации серого этим чистым хелпером — общим для rdpview и vncview, чтобы
// формула яркости не разъехалась между виджетами. Тестируется из Go через
// обёртки gray.go (cgo в _test.go запрещён — см. wheel.go).

#ifdef __cplusplus
extern "C" {
#endif

// asbru_gray_frame — обесцветить кадр 32bpp in place.
// Макет пикселя — [B, G, R, X] (X не трогается): так выглядят scanLine'ы
// QImage::Format_RGB32 на поддерживаемых платформах (x86_64/aarch64 —
// little-endian; та же оговорка, что у utf16Nul в rdpview.cpp), и в том же
// порядке отдают фреймбуфер libfreerdp (BGRX32) и libvncclient (BGRA).
// Яркость — формула Qt qGray (ITU-R BT.601): (r*11 + g*16 + b*5) / 32.
// w/h — размер кадра в пикселях; stride — байт на строку (может быть больше
// w*4: добивочные байты строки не имеют отношения к кадру и не трогаются).
// Пустой буфер или stride < w*4 — отказ без изменения входа.
static inline void asbru_gray_frame(uint8_t *bgra, int w, int h, int stride) {
    if (!bgra || w <= 0 || h <= 0 || stride < w * 4) return;
    for (int y = 0; y < h; y++) {
        uint8_t *line = bgra + (size_t)y * (size_t)stride;
        for (int x = 0; x < w; x++) {
            uint8_t *px = line + (size_t)x * 4;
            const int gray = (px[0] * 5 + px[1] * 16 + px[2] * 11) >> 5;
            px[0] = px[1] = px[2] = (uint8_t)gray;
        }
    }
}

#ifdef __cplusplus
}
#endif

#endif // ASBRU_GRAY_FRAME_H
