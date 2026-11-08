package ui

import (
	"reflect"
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/method"
)

// M30: -L/-R/-D разбор и обратная сборка.
func TestForwardRows(t *testing.T) {
	got := forwardRows("-L 8080:localhost:80 -R22:10.0.0.1:22 -D1080 -l root -p 2222")
	want := []forwardRow{
		{Kind: "L", Spec: "8080:localhost:80"},
		{Kind: "R", Spec: "22:10.0.0.1:22"},
		{Kind: "D", Spec: "1080"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("forwardRows:\n got %#v\nwant %#v", got, want)
	}
	// attached-форма и без -L/-R/-D.
	if !reflect.DeepEqual(forwardRows("-L8080:localhost:80"), []forwardRow{{Kind: "L", Spec: "8080:localhost:80"}}) {
		t.Fatalf("attached: %#v", forwardRows("-L8080:localhost:80"))
	}
	if got := forwardRows("-p 22 -l user"); len(got) != 0 {
		t.Fatalf("нет forwards: %#v", got)
	}
}

func TestApplyForwardRows(t *testing.T) {
	// убрать существующий, добавить структурированный; прочее сохраняется.
	got := applyForwardRows("-L 8080:localhost:80 -p 22",
		[]forwardRow{{Kind: "L", Spec: "9090:localhost:90"}, {Kind: "D", Spec: "1080"}})
	if !reflect.DeepEqual(method.SplitArgs(got), []string{"-p", "22", "-L", "9090:localhost:90", "-D", "1080"}) {
		t.Fatalf("apply: %q", got)
	}
	// пустые rows → только не-forward токены.
	if got := applyForwardRows("-L 8080:localhost:80 -p 22", nil); got != "-p 22" {
		t.Fatalf("empty rows: %q", got)
	}
	// invalid kind → отброшен.
	if got := applyForwardRows("", []forwardRow{{Kind: "X", Spec: "1:2:3"}, {Kind: "L", Spec: "1:2:3"}}); got != "-L 1:2:3" {
		t.Fatalf("invalid kind: %q", got)
	}
}

// M30-ревью: кавычки пробельных значений (-o/… ) сохраняются через method.JoinArgs
// (иначе SplitArgs развалил бы "ProxyCommand=ssh -W %h:%p jump" на куски).
func TestApplyForwardRowsQuotePreservation(t *testing.T) {
	got := applyForwardRows(`-o "ProxyCommand=ssh -W %h:%p jump"`, nil)
	if got != `-o "ProxyCommand=ssh -W %h:%p jump"` {
		t.Fatalf("-o quote: %q", got)
	}
	got2 := applyForwardRows(`-r "disk:share=/home/my share"`, nil)
	if got2 != `-r "disk:share=/home/my share"` {
		t.Fatalf("-r quote: %q", got2)
	}
	// с forward тоже сохраняем кавычки других токенов.
	got3 := applyForwardRows(`-L 8080:localhost:80 -o "A=B C"`, []forwardRow{{Kind: "D", Spec: "1080"}})
	if got3 != `-o "A=B C" -D 1080` {
		t.Fatalf("mixed: %q", got3)
	}
}

// MINOR-8: голый -L без spec, за которым отдельная опция — её НЕ съедать.
func TestApplyForwardRowsBareLKeepsNextOption(t *testing.T) {
	got := applyForwardRows("-L -o foo", nil)
	if got != "-o foo" {
		t.Fatalf("got %q, want %q (-L голый, -o foo сохраняются)", got, "-o foo")
	}
	// спецификация (не-флаг после -L) убирается вместе с -L.
	got = applyForwardRows("-L 8080:localhost:80", nil)
	if got != "" {
		t.Fatalf("got %q, want %q (-L и его spec убираются)", got, "")
	}
}
