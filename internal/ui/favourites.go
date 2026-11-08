package ui

// M31: панель Favourites — данные. Favourites — соединения с favourite=1.
// (Показ — вкладка «Избранные» боковой панели, #1; диалог showFavourites удалён
// как мёртвый код.)

import (
	"sort"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// favouriteEntry — соединение-избранное (uuid + имя) для панели Favourites.
type favouriteEntry struct {
	UUID string
	Name string
}

// favouriteEntries — соединения с favourite=1, по имени asc (детерминированно).
func favouriteEntries(envs map[string]config.Environment) []favouriteEntry {
	var out []favouriteEntry
	for uuid, e := range envs {
		if e.Favourite == 1 && e.Name != "" {
			out = append(out, favouriteEntry{uuid, e.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].UUID < out[j].UUID // M31-ревью #7: детерминизм при дублях имён
	})
	return out
}
