package session

// M31: WoL (Wake-on-LAN) — magic packet: 6×0xFF + MAC×16 (102 байта), UDP :9.

import "testing"

func TestMagicPacket(t *testing.T) {
	pkt, err := magicPacket("00:11:22:33:44:55")
	if err != nil {
		t.Fatal(err)
	}
	if len(pkt) != 102 {
		t.Fatalf("len=%d want 102", len(pkt))
	}
	for i := 0; i < 6; i++ {
		if pkt[i] != 0xFF {
			t.Fatalf("префикс %d != 0xFF", i)
		}
	}
	want := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	for r := 0; r < 16; r++ {
		off := 6 + r*6
		for j := 0; j < 6; j++ {
			if pkt[off+j] != want[j] {
				t.Fatalf("повтор %d MAC неверен: %x", r, pkt[off:off+6])
			}
		}
	}
	if _, err := magicPacket("not-a-mac"); err == nil {
		t.Fatal("ошибки нет для невалидного MAC")
	}
	// M31-ревью #6: EUI-64 (8 байт) отклоняется, а не даёт неверный пакет.
	if _, err := magicPacket("00:11:22:33:44:55:66:77"); err == nil {
		t.Fatal("EUI-64 MAC принят")
	}
}

func TestWolAddr(t *testing.T) {
	if a := wolAddr(""); a != "255.255.255.255:9" {
		t.Fatalf("пустой IP: %q", a)
	}
	if a := wolAddr("192.0.2.1"); a != "192.0.2.1:9" {
		t.Fatalf("с IP: %q", a)
	}
}
