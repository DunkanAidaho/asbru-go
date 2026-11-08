package embed

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// M37: контракт Go ↔ C++. Битмаска, которую Go собирает в RDPParams.Flags(),
// декодируется в rdpview.cpp — значения обязаны совпадать, иначе галки будут
// включать ЧУЖИЕ настройки (тихая порча вместо ошибки). Тест читает bridge.h и
// сверяет каждый #define с константой Go.
func TestRDPFlagsMatchBridgeHeader(t *testing.T) {
	const header = "../ui/wgt/rdpview/bridge.h"
	src, err := os.ReadFile(header)
	if err != nil {
		t.Fatalf("bridge.h: %v", err)
	}
	re := regexp.MustCompile(`(?m)^\s*#define\s+(RDP_FLAG_[A-Z_]+)\s+\(1u\s*<<\s*(\d+)\)`)
	found := map[string]uint32{}
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("%s: %v", m[1], err)
		}
		if n > 31 {
			t.Fatalf("%s: бит %d вне 32-битной маски", m[1], n)
		}
		found[m[1]] = 1 << uint(n)
	}
	if len(found) == 0 {
		t.Fatal("в bridge.h нет ни одного RDP_FLAG_* — маска не описана")
	}

	pairs := []struct {
		goName  string
		cName   string
		goValue uint32
	}{
		{"FlagCertIgnore", "RDP_FLAG_CERT_IGNORE", FlagCertIgnore},
		{"FlagNoNLA", "RDP_FLAG_NO_NLA", FlagNoNLA},
		{"FlagNoTLS", "RDP_FLAG_NO_TLS", FlagNoTLS},
		{"FlagNoRDP", "RDP_FLAG_NO_RDP", FlagNoRDP},
		{"FlagNoAuth", "RDP_FLAG_NO_AUTH", FlagNoAuth},
		{"FlagNoFastPath", "RDP_FLAG_NO_FAST_PATH", FlagNoFastPath},
		{"FlagRFX", "RDP_FLAG_RFX", FlagRFX},
		{"FlagNSC", "RDP_FLAG_NSC", FlagNSC},
		{"FlagDynamicResolution", "RDP_FLAG_DYNAMIC_RESOLUTION", FlagDynamicResolution},
		{"FlagFontSmooth", "RDP_FLAG_FONT_SMOOTH", FlagFontSmooth},
		{"FlagAdmin", "RDP_FLAG_ADMIN", FlagAdmin},
	}
	seen := map[uint32]string{}
	for _, p := range pairs {
		cv, ok := found[p.cName]
		if !ok {
			t.Errorf("в bridge.h нет %s (Go %s = %#x)", p.cName, p.goName, p.goValue)
			continue
		}
		if cv != p.goValue {
			t.Errorf("%s: C=%#x, Go %s=%#x — РАСХОЖДЕНИЕ", p.cName, cv, p.goName, p.goValue)
		}
		if prev, dup := seen[p.goValue]; dup {
			t.Errorf("бит %#x занят дважды: %s и %s", p.goValue, prev, p.goName)
		}
		seen[p.goValue] = p.goName
	}
	if t.Failed() {
		t.Logf("найдено в bridge.h: %s", fmt.Sprint(found))
	}
}
