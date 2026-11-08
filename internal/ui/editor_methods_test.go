package ui

// Комбо «Тип» редактора обязан показывать ВСЕ диалекты реестра методов.
// Живой инцидент 2026-10-14: PVE добавили в реестр (method/displayNames),
// но methodNames — курированный список — про новый диалект не знал, и
// «Proxmox VE (PVE)» не появился в списке типа. Связка реестр↔комбо — тест.

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/method"
)

func TestMethodNamesCoverRegistry(t *testing.T) {
	have := map[string]bool{}
	for _, n := range methodNames {
		have[n] = true
	}
	for _, d := range method.AllDialects() {
		n := method.DisplayName(d)
		if !have[n] {
			t.Errorf("диалект %q (%s) есть в реестре, но отсутствует в комбо «Тип»", n, d)
		}
	}
	if len(methodNames) < len(method.AllDialects()) {
		t.Errorf("в комбо %d пунктов, в реестре %d — комбо обрезан?",
			len(methodNames), len(method.AllDialects()))
	}
}

func TestMethodNamesContainPVE(t *testing.T) {
	// Прямой тест живого симптома: тип «Proxmox VE (PVE)» выбираем в редакторе.
	for _, n := range methodNames {
		if n == "Proxmox VE (PVE)" {
			return
		}
	}
	t.Errorf("«Proxmox VE (PVE)» отсутствует в списке типов редактора")
}
