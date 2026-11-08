package ui

import "testing"

// бэклог «иконки по типу»: имя тема-иконки для метода подключения.
func TestIconNameForMethod(t *testing.T) {
	cases := map[string]string{
		"SSH":               "utilities-terminal",
		"MOSH":              "utilities-terminal",
		"SFTP":              "folder-remote",
		"FTP":               "folder-remote",
		"WebDAV":            "folder-remote",
		"RDP (rdesktop)":    "preferences-desktop-remote",
		"RDP (xfreerdp)":    "preferences-desktop-remote",
		"VNC":               "preferences-desktop-remote",
		"Telnet":            "network-server",
		"Serial (cu)":       "network-server",
		"cu":                "network-server",
		"IBM 3270/5250":     "utilities-terminal",
		"Generic Command":   "utilities-terminal",
		"Local Shell":       "utilities-terminal",
		"неизвестный-метод": "utilities-terminal",
	}
	for method, want := range cases {
		if got := iconNameForMethod(method); got != want {
			t.Errorf("iconNameForMethod(%q) = %q, хотим %q", method, got, want)
		}
	}
}
