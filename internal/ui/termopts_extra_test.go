package ui

import "testing"

// M27: cursorShapeInt — строка → enum QTermWidget.
func TestCursorShapeInt(t *testing.T) {
	cases := map[string]int{"block": 0, "underline": 1, "ibeam": 2, "": 0, "bogus": 0}
	for in, want := range cases {
		if got := cursorShapeInt(in); got != want {
			t.Fatalf("cursorShapeInt(%q) = %d, want %d", in, got, want)
		}
	}
}

// M27: colorToRGB — GTK RGBA #rrrrggggbbbb → "r,g,b".
func TestColorToRGB(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"#cc62cc62cc62", "204,204,204"},
		{"#000000000000", "0,0,0"},
		{"#ffffffffffff", "255,255,255"},
		{"", "0,0,0"},
		{"junk", "0,0,0"},
	}
	for _, c := range cases {
		if got := colorToRGB(c.in); got != c.want {
			t.Fatalf("colorToRGB(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
