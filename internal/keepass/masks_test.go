package keepass

import "testing"

// ParseMask: `<field|путь>`; поле регистронезависимо; легаси-поля.
func TestParseMask(t *testing.T) {
	cases := []struct {
		mask  string
		field string
		path  string
		ok    bool
	}{
		{"<password|/Group/Entry>", "password", "/Group/Entry", true},
		{"<PASSWORD|/Group/Entry>", "password", "/Group/Entry", true},
		{"<Username|/Group/Entry>", "username", "/Group/Entry", true},
		{"<url|/Group/Entry>", "url", "/Group/Entry", true},
		{"<title|/Group/Entry>", "title", "/Group/Entry", true},
		{"<bogus|/Group/Entry>", "", "", false},
		{"<password|>", "", "", false},
		{"<password>", "", "", false},
		{"просто текст", "", "", false},
	}
	for _, c := range cases {
		f, p, ok := ParseMask(c.mask)
		if ok != c.ok || f != c.field || p != c.path {
			t.Errorf("ParseMask(%q) = (%q, %q, %v), want (%q, %q, %v)",
				c.mask, f, p, ok, c.field, c.path, c.ok)
		}
	}
}

type fakeResolver struct {
	fields map[string]string
}

func (f fakeResolver) Field(field, path string) (string, error) {
	return f.fields[field+"|"+path], nil
}

// ReplaceMasks: подстановка всех масок через резолвер.
func TestReplaceMasks(t *testing.T) {
	r := fakeResolver{fields: map[string]string{
		"password|/Group/Entry": "s3cret",
		"username|/Group/Entry": "root",
	}}
	in := "<username|/Group/Entry>@<password|/Group/Entry>"
	out, err := ReplaceMasks(in, r)
	if err != nil {
		t.Fatal(err)
	}
	if out != "root@s3cret" {
		t.Fatalf("ReplaceMasks = %q", out)
	}
}
