package vncview

// Обрыв VNC: последний кадр остаётся во вкладке (M33-г), но цветной кадр
// неотличим от живой сессии — при красном индикаторе вкладки подключение
// кажется живым (user-report 2026-10-04). Кадр на канвасе обесцвечивается
// чистым C-хелпером asbru_gray_frame (../grayframe.h); здесь — его контракт
// для vncview-моста (формула общая с rdpview, объявление — своё в bridge.h).

import "testing"

// qGray — эталонная формула Qt (ITU-R BT.601): (r*11 + g*16 + b*5) / 32.
func qGray(b, g, r byte) byte { return byte((int(r)*11 + int(g)*16 + int(b)*5) / 32) }

func TestGrayFrameDesaturatesToLuminance(t *testing.T) {
	// Пиксели [B,G,R,X]: красный, зелёный, синий, белый, чёрный. Яркости каналов
	// различны (87/127/39) — перепутанные местами B/G/R дадут другие числа.
	cases := []struct{ b, g, r byte }{
		{0, 0, 255},
		{0, 255, 0},
		{255, 0, 0},
		{255, 255, 255},
		{0, 0, 0},
	}
	buf := make([]byte, len(cases)*4)
	for i, c := range cases {
		buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3] = c.b, c.g, c.r, 0xFF
	}
	grayFrame(buf, len(cases), 1, len(cases)*4)
	for i, c := range cases {
		want := qGray(c.b, c.g, c.r)
		b, g, r, x := buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3]
		if b != want || g != want || r != want {
			t.Errorf("пиксель %d (B=%d G=%d R=%d): стал BGR=%d,%d,%d, ожидался серый %d",
				i, c.b, c.g, c.r, b, g, r, want)
		}
		if x != 0xFF {
			t.Errorf("пиксель %d: старший байт (X) изменён: %d", i, x)
		}
	}
}

func TestGrayFrameKeepsStridePadding(t *testing.T) {
	// Один пиксель на строку при stride 8: добивные байты строки не трогаем.
	buf := []byte{
		0, 0, 255, 0xFF, 0xAA, 0xBB, 0xCC, 0xDD, // строка 0 (красный) + padding
		0, 255, 0, 0xFF, 0x11, 0x22, 0x33, 0x44, // строка 1 (зелёный) + padding
	}
	grayFrame(buf, 1, 2, 8)
	if buf[0] != qGray(0, 0, 255) || buf[1] != qGray(0, 0, 255) || buf[2] != qGray(0, 0, 255) {
		t.Errorf("пиксель строки 0 не обесцвечен: %v", buf[0:4])
	}
	if buf[8] != qGray(0, 255, 0) || buf[9] != qGray(0, 255, 0) || buf[10] != qGray(0, 255, 0) {
		t.Errorf("пиксель строки 1 не обесцвечен: %v", buf[8:12])
	}
	pad := append(append([]byte{}, buf[4:8]...), buf[12:16]...)
	want := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0x11, 0x22, 0x33, 0x44}
	for i := range want {
		if pad[i] != want[i] {
			t.Fatalf("добивочные байты stride затронуты: %v, ожидалось %v", pad, want)
		}
	}
}

func TestGrayFrameIgnoresDegenerateInput(t *testing.T) {
	grayFrame(nil, 4, 4, 64) // пустой буфер — отказ без паники
	// stride меньше w*4 — не наш макет кадра: не трогаем вовсе.
	buf := []byte{0, 0, 255, 0xFF}
	grayFrame(buf, 1, 1, 2)
	if buf[0] != 0 || buf[1] != 0 || buf[2] != 255 {
		t.Errorf("кадр с недостаточным stride изменён: %v", buf)
	}
}
