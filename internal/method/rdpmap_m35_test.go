package method

import "testing"

// M35: rdesktop длинный флаг "-grab-keyboard" НЕ должен усекаться до "-g" и
// мапиться в size. Он известен (rdpSettingMap "-grab-keyboard" → grab-keyboard)
// и является БУЛЕВЫМ флагом (значение "1", не "rab-keyboard").
func TestGrabKeyboardNotMappedToSize(t *testing.T) {
	settings, unknown := MapRdpOptions("-grab-keyboard")
	if _, ok := settings["size"]; ok {
		t.Errorf("-grab-keyboard ошибочно мапится в size: %#v", settings)
	}
	if settings["grab-keyboard"] != "1" {
		t.Errorf("-grab-keyboard должен дать grab-keyboard=1, а не %q: %#v", settings["grab-keyboard"], settings)
	}
	if len(unknown) != 0 {
		t.Errorf("-grab-keyboard в unknown: %v", unknown)
	}
	// короткий эквивалент -K → тоже grab-keyboard=1 (консистентность).
	if s, _ := MapRdpOptions("-K"); s["grab-keyboard"] != "1" {
		t.Errorf("-K должен дать grab-keyboard=1: %#v", s)
	}
}

// M35: короткие rdesktop-флаги и xfreerdp "/key:value" продолжают работать.
func TestRdpShortAndXFreeRDPStillWork(t *testing.T) {
	if s, _ := MapRdpOptions("-f"); s["fullscreen"] == "" {
		t.Errorf("-f: %#v", s)
	}
	if s, _ := MapRdpOptions("-g 1920x1080"); s["size"] == "" {
		t.Errorf("-g 1920x1080: %#v", s)
	}
	if s, _ := MapRdpOptions("/size:1280x720"); s["size"] != "1280x720" {
		t.Errorf("/size:1280x720: %#v", s)
	}
}
