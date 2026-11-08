package rdpview

// Картинки в буфере обмена RDP (M39-остаток). На проводе CF_DIB/CF_DIBV5 — это
// ГОЛЫЙ DIB (BITMAPINFOHEADER/V5 + палитра + пиксели), а QImageWriter("BMP")
// пишет полноценный BMP-файл: 14-байтный BITMAPFILEHEADER + DIB. Математика
// заголовка чистая, поэтому проверяется здесь; сама Qt-обвязка (cliprdr
// колбэки, QClipboard) — только cgo-сборкой и трассировкой кода: C++-стенда нет.

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func le16(b []byte, off int, v uint16) { binary.LittleEndian.PutUint16(b[off:], v) }
func le32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }

// bitmapInfoHeader — синтетический BITMAPINFOHEADER (biSize = 40).
func bitmapInfoHeader(bitCount uint16, compression, clrUsed uint32) []byte {
	b := make([]byte, 40)
	le32(b, 0, 40) // biSize
	le32(b, 4, 2)  // biWidth
	le32(b, 8, 2)  // biHeight
	le16(b, 12, 1) // biPlanes
	le16(b, 14, bitCount)
	le32(b, 16, compression)
	le32(b, 20, 0) // biSizeImage
	le32(b, 32, clrUsed)
	return b
}

// bitmapV5Header — синтетический BITMAPV5HEADER (biSize = 124); profileSize
// лежит по смещению 116.
func bitmapV5Header(bitCount uint16, compression, clrUsed, profileSize uint32) []byte {
	b := make([]byte, 124)
	le32(b, 0, 124) // bV5Size
	le32(b, 4, 2)   // width
	le32(b, 8, 2)   // height
	le16(b, 12, 1)  // planes
	le16(b, 14, bitCount)
	le32(b, 16, compression)
	le32(b, 32, clrUsed)
	le32(b, 116, profileSize) // bV5ProfileSize
	return b
}

func bmpFile(dib []byte) []byte {
	b := make([]byte, 14+len(dib))
	b[0], b[1] = 'B', 'M'
	copy(b[14:], dib)
	return b
}

// Локальный→сервер: QImageWriter даёт BMP-файл, на провод уходит только DIB
// (14-байтный BITMAPFILEHEADER срезается).
func TestDIBFromBMPStripsFileHeader(t *testing.T) {
	dib := append(bitmapInfoHeader(24, 0, 0), 0x11, 0x22, 0x33, 0x44)
	bmp := bmpFile(dib)

	got, ok := dibFromBmp(bmp)
	if !ok {
		t.Fatal("валидный BMP отвергнут")
	}
	if !bytes.Equal(got, dib) {
		t.Fatalf("DIB после среза не совпал: got %d байт, want %d", len(got), len(dib))
	}
	if len(got) != len(bmp)-14 {
		t.Fatalf("длина DIB = %d, ожидалось len(BMP)-14 = %d", len(got), len(bmp)-14)
	}
}

func TestDIBFromBMPRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"пусто", nil},
		{"короче заголовка", []byte("BM")},
		{"ровно 14 байт без DIB", []byte("BM")},
		{"не BMP-магия", append([]byte("XX"), bmpFile(bitmapInfoHeader(24, 0, 0))[2:]...)},
	}
	for _, c := range cases {
		if _, ok := dibFromBmp(c.in); ok {
			t.Errorf("%s: отвергнуто быть не должно было — принято", c.name)
		}
	}
}

// Усечённый файл: заголовок «BM», но DIB-части нет — тоже отказ.
func TestDIBFromBMPRejectsHeaderOnly(t *testing.T) {
	hdr := make([]byte, 14)
	hdr[0], hdr[1] = 'B', 'M'
	if _, ok := dibFromBmp(hdr); ok {
		t.Fatal("BMP без DIB принят")
	}
}

// Сервер→локальный: cliprdr присылает ГОЛЫЙ DIB (CF_DIB/CF_DIBV5), а
// QImage::fromData ждёт BMP-файл — синтезируем 14-байтный BITMAPFILEHEADER.
// bfOffBits обязан указывать на пиксели ВНУТРИ DIB: biSize + палитра +
// (12 для BI_BITFIELDS при BITMAPINFOHEADER) + bV5ProfileSize (у V5).
func TestDIBPixelOffset(t *testing.T) {
	cases := []struct {
		name string
		dib  []byte
		want uint32
	}{
		{"24bpp BI_RGB", bitmapInfoHeader(24, 0, 0), 40},
		{"32bpp BI_RGB", bitmapInfoHeader(32, 0, 0), 40},
		{"8bpp, палитра по умолчанию (256 цветов)", bitmapInfoHeader(8, 0, 0), 40 + 256*4},
		{"8bpp, clrUsed=16", bitmapInfoHeader(8, 0, 16), 40 + 16*4},
		{"4bpp, палитра по умолчанию (16 цветов)", bitmapInfoHeader(4, 0, 0), 40 + 16*4},
		{"32bpp BI_BITFIELDS (+3 маски после заголовка)", bitmapInfoHeader(32, 3, 0), 40 + 12},
		{"16bpp BI_BITFIELDS (+3 маски)", bitmapInfoHeader(16, 3, 0), 40 + 12},
		// У BITMAPV5HEADER маски уже внутри заголовка: +12 НЕ добавляется.
		{"V5 32bpp BI_BITFIELDS + profile", bitmapV5Header(32, 3, 0, 20), 124 + 20},
		{"V5 8bpp палитра + profile", bitmapV5Header(8, 0, 0, 16), 124 + 256*4 + 16},
	}
	for _, c := range cases {
		// Дополняем до валидного DIB (заголовок + палитра/маски/профиль +
		// пиксели): сама функция отвергает усечённые буферы (см. тест ниже).
		dib := c.dib
		if need := int(c.want) + 4; len(dib) < need {
			dib = append(dib, make([]byte, need-len(dib))...)
		}
		if got := dibPixelOffset(dib); got != c.want {
			t.Errorf("%s: offset=%d, ожидалось %d", c.name, got, c.want)
		}
	}
}

func TestDIBPixelOffsetRejectsTruncated(t *testing.T) {
	if got := dibPixelOffset(nil); got != 0 {
		t.Errorf("nil: offset=%d, ожидался 0", got)
	}
	// biSize=40, но в буфере 39 байт.
	short := make([]byte, 39)
	le32(short, 0, 40)
	if got := dibPixelOffset(short); got != 0 {
		t.Errorf("усечённый заголовок: offset=%d, ожидался 0", got)
	}
	// Заголовок обещает 8bpp-палитру на 256 цветов, но палитры нет.
	if got := dibPixelOffset(bitmapInfoHeader(8, 0, 0)); got != 0 {
		t.Errorf("нет палитры: offset=%d, ожидался 0", got)
	}
	// V5 объявляет biSize=124, а буфер короче.
	v5short := make([]byte, 40)
	le32(v5short, 0, 124)
	if got := dibPixelOffset(v5short); got != 0 {
		t.Errorf("усечённый V5: offset=%d, ожидался 0", got)
	}
	// Абсурдный clrUsed: смещение не должно переполниться в валидное.
	if got := dibPixelOffset(bitmapInfoHeader(8, 0, 0xFFFFFFFF)); got != 0 {
		t.Errorf("clrUsed=0xFFFFFFFF: offset=%d, ожидался 0", got)
	}
}

func TestBMPFromDIBPrependsFileHeader(t *testing.T) {
	dib := append(bitmapInfoHeader(24, 0, 0), 0xAA, 0xBB, 0xCC, 0xDD)
	bmp, ok := bmpFromDib(dib)
	if !ok {
		t.Fatal("валидный DIB отвергнут")
	}
	wantLen := 14 + len(dib)
	if len(bmp) != wantLen {
		t.Fatalf("длина BMP=%d, ожидалось %d", len(bmp), wantLen)
	}
	if bmp[0] != 'B' || bmp[1] != 'M' {
		t.Fatalf("магия BMP = %q, ожидалось \"BM\"", bmp[:2])
	}
	if got := binary.LittleEndian.Uint32(bmp[2:6]); got != uint32(wantLen) {
		t.Errorf("bfSize=%d, ожидалось %d", got, wantLen)
	}
	if got := binary.LittleEndian.Uint16(bmp[6:8]); got != 0 {
		t.Errorf("bfReserved1=%d, ожидался 0", got)
	}
	if got := binary.LittleEndian.Uint16(bmp[8:10]); got != 0 {
		t.Errorf("bfReserved2=%d, ожидался 0", got)
	}
	if got := binary.LittleEndian.Uint32(bmp[10:14]); got != 14+40 {
		t.Errorf("bfOffBits=%d, ожидалось %d", got, 14+40)
	}
	if !bytes.Equal(bmp[14:], dib) {
		t.Fatal("DIB внутри BMP повреждён")
	}
}

func TestBMPFromDIBOffBitsWithPalette(t *testing.T) {
	dib := append(bitmapInfoHeader(8, 0, 0), make([]byte, 256*4+4)...)
	bmp, ok := bmpFromDib(dib)
	if !ok {
		t.Fatal("DIB с палитрой отвергнут")
	}
	want := uint32(14 + 40 + 256*4)
	if got := binary.LittleEndian.Uint32(bmp[10:14]); got != want {
		t.Fatalf("bfOffBits=%d, ожидалось %d", got, want)
	}
}

func TestBMPFromDIBRejectsTruncated(t *testing.T) {
	cases := []struct {
		name string
		dib  []byte
	}{
		{"пусто", nil},
		{"короче заголовка", make([]byte, 39)},
		{"8bpp без палитры", bitmapInfoHeader(8, 0, 0)},
		{"V5 без заявленной длины", func() []byte {
			b := make([]byte, 40)
			le32(b, 0, 124)
			return b
		}()},
	}
	for _, c := range cases {
		if _, ok := bmpFromDib(c.dib); ok {
			t.Errorf("%s: DIB принят, хотя должен быть отвергнут", c.name)
		}
	}
}

func TestBMPRoundtripPreservesDIB(t *testing.T) {
	dib := append(bitmapInfoHeader(24, 0, 0), 0x01, 0x02, 0x03, 0x04)
	bmp, ok := bmpFromDib(dib)
	if !ok {
		t.Fatal("bmpFromDib отверг валидный DIB")
	}
	got, ok := dibFromBmp(bmp)
	if !ok {
		t.Fatal("dibFromBmp отверг собранный BMP")
	}
	if !bytes.Equal(got, dib) {
		t.Fatal("roundtrip DIB→BMP→DIB изменил данные")
	}
}
