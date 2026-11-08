package ui

import (
	"bytes"
	"image/png"
	"testing"
)

// TestEmbeddedAppIcons проверяет, что в бинарник попал полный набор размеров
// и что каждый PNG декодируется с заявленными габаритами. Тест не требует
// Qt/дисплея — только встроенные данные.
func TestEmbeddedAppIcons(t *testing.T) {
	want := map[string][2]int{
		"asbru-icon-16.png":  {16, 16},
		"asbru-icon-24.png":  {24, 24},
		"asbru-icon-32.png":  {32, 32},
		"asbru-icon-48.png":  {48, 48},
		"asbru-icon-64.png":  {64, 64},
		"asbru-icon-128.png": {128, 128},
		"asbru-icon-256.png": {256, 256},
		"asbru-icon-512.png": {512, 512},
	}
	for name, size := range want {
		data, err := iconPNGs.ReadFile("assets/" + name)
		if err != nil {
			t.Fatalf("%s: нет во встроенном наборе: %v", name, err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: PNG не декодируется: %v", name, err)
		}
		if b := img.Bounds(); b.Dx() != size[0] || b.Dy() != size[1] {
			t.Fatalf("%s: размер %dx%d, ожидался %dx%d", name, b.Dx(), b.Dy(), size[0], size[1])
		}
	}
}
