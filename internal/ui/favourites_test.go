package ui

// M31: панель Favourites — данные (соединения с favourite=1), сортировка по имени.

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestFavouriteEntries(t *testing.T) {
	envs := map[string]config.Environment{
		"a": {Name: "web", Favourite: 1},
		"b": {Name: "db", Favourite: 0},
		"c": {Name: "gw", Favourite: 1},
	}
	got := favouriteEntries(envs)
	if len(got) != 2 {
		t.Fatalf("len=%d want 2", len(got))
	}
	// по имени asc: gw, web
	if got[0].UUID != "c" || got[0].Name != "gw" || got[1].UUID != "a" || got[1].Name != "web" {
		t.Fatalf("got=%v want [gw web]", got)
	}
	// пусто/без favourite
	if n := len(favouriteEntries(map[string]config.Environment{})); n != 0 {
		t.Fatalf("пусто: %d", n)
	}
}
