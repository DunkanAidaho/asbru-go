package ui

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/secretservice"
)

// #4: keepassPickerItems — "Title (username)" → uuid, сортировка, отброс без uuid.
func TestKeepassPickerItems(t *testing.T) {
	entries := []secretservice.Entry{
		{UUID: "u2", Title: "web", Username: "admin"},
		{UUID: "u1", Title: "db", Username: ""},
		{UUID: "u3", Title: "", Username: "svc"},    // title пуст → username
		{UUID: "", Title: "no-uuid", Username: "x"}, // отбрасывается
	}
	got := keepassPickerItems(entries)
	want := []keepassPickerItem{
		{Label: "db", UUID: "u1"},
		{Label: "svc", UUID: "u3"},
		{Label: "web (admin)", UUID: "u2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pick:\n got %#v\nwant %#v", got, want)
	}
}

// #4: vncSecurityValues + валидация.
func TestVNCSecurityValues(t *testing.T) {
	vals := vncSecurityValues()
	if len(vals) != 3 || vals[0] != "auto" || vals[1] != "vencrypt" || vals[2] != "none" {
		t.Fatalf("values: %v", vals)
	}
	for _, v := range []string{"auto", "vencrypt", "none"} {
		if !validVNCEncryption(v) {
			t.Errorf("valid: %q", v)
		}
	}
	for _, v := range []string{"", "AUTO", "vencrypt-tls", "bogus"} {
		if validVNCEncryption(v) {
			t.Errorf("invalid принят: %q", v)
		}
	}
}
