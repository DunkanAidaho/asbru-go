package method

import "testing"

func TestPVESpec(t *testing.T) {
	d, ok := FromDisplay("Proxmox VE (PVE)")
	if !ok {
		t.Fatalf("PVE не найден в реестре")
	}
	sp := SpecFor(d)
	if sp.Client != ClientEmbedded {
		t.Errorf("клиент PVE = %v, ожидался embedded", sp.Client)
	}
	// Fix-раунд 1 (санкция контроллера): у PVE нет терминала и командных
	// полей (prepend/postpend/send string/проброс) — как у RDP/VNC.
	if sp.Commandable {
		t.Errorf("PVE не должен быть командным методом (Commandable=true)")
	}
	if len(sp.AuthTypes) != 2 || sp.AuthTypes[0] != AuthUserpass {
		t.Errorf("auth: %v", sp.AuthTypes)
	}
	found := false
	for _, f := range sp.Flags {
		if f.Setting == "verifyTLS" {
			found = true
			if f.Tokens[0] != "verify-tls" {
				t.Errorf("токен %v", f.Tokens)
			}
		}
	}
	if !found {
		t.Errorf("нет галки verifyTLS")
	}
	if !TogglePresent("verify-tls", OptionToggle{Setting: "verifyTLS", Tokens: []string{"verify-tls"}}) {
		t.Errorf("TogglePresent не видит verify-tls")
	}
}
