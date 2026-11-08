package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// COSMETIC-4: FlexInt должен принимать !!float (напр. timeout: 1.5 → truncate 1),
// а не игнорировать и отдавать 0.
func TestFlexIntFloat(t *testing.T) {
	var f FlexInt
	if err := yaml.Unmarshal([]byte("1.5"), &f); err != nil {
		t.Fatal(err)
	}
	if f != 1 {
		t.Fatalf("FlexInt(1.5) = %d, want 1 (truncate)", f)
	}
	// целые/строки/були — как раньше.
	for _, c := range []struct {
		in   string
		want FlexInt
	}{
		{"5", 5}, {`"3"`, 3}, {"true", 1}, {"false", 0},
	} {
		var g FlexInt
		if err := yaml.Unmarshal([]byte(c.in), &g); err != nil || g != c.want {
			t.Fatalf("FlexInt(%s) = %d/%v, want %d", c.in, g, err, c.want)
		}
	}
}
