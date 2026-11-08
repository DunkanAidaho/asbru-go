// Пакет method: режимы подключений (диалекты), реестр display-имён из конфига,
// декларативные схемы полей и конвертация опционов между диалектами.
package method

import (
	"strings"
)

// Dialect — идентификатор метода подключения.
type Dialect string

const (
	SSH             Dialect = "SSH"
	SFTP            Dialect = "SFTP"
	Telnet          Dialect = "Telnet"
	FTP             Dialect = "FTP"
	WebDAV          Dialect = "WebDAV"
	MOSH            Dialect = "MOSH"
	SerialCu        Dialect = "Serial (cu)"
	SerialRemoteTTY Dialect = "Serial (remote-tty)"
	IBM3270         Dialect = "IBM 3270/5250"
	RDesktop        Dialect = "RDP (rdesktop)"
	XFreeRDP        Dialect = "RDP (xfreerdp)"
	VNC             Dialect = "VNC"
	PVE             Dialect = "Proxmox VE (PVE)"
	Generic         Dialect = "Generic Command"
	LocalShell      Dialect = "Local Shell"
)

var displayNames = map[Dialect]string{
	SSH: "SSH", SFTP: "SFTP", Telnet: "Telnet", FTP: "FTP", WebDAV: "WebDAV",
	MOSH: "MOSH", SerialCu: "Serial (cu)", SerialRemoteTTY: "Serial (remote-tty)",
	IBM3270: "IBM 3270/5250", RDesktop: "RDP (rdesktop)", XFreeRDP: "RDP (xfreerdp)",
	VNC: "VNC", PVE: "Proxmox VE (PVE)", Generic: "Generic Command", LocalShell: "Local Shell",
}

var lookup = func() map[string]Dialect {
	m := make(map[string]Dialect, len(displayNames))
	for d, name := range displayNames {
		m[name] = d
	}
	return m
}()

// FromDisplay — display-имя (значение environments{uuid}.method) → диалект.
func FromDisplay(name string) (Dialect, bool) {
	d, ok := lookup[name]
	return d, ok
}

// DisplayName — обратное отображение диалекта.
func DisplayName(d Dialect) string { return displayNames[d] }

// VNCBackend — подвид VNC-клиента (детектируется по выводу `vncviewer --help`).
type VNCBackend string

const (
	VNCGeneric  VNCBackend = "generic"
	VNCTigerVNC VNCBackend = "tigervnc"
	VNCRealVNC  VNCBackend = "realvnc"
)

// DetectVNC повторяет легаси-детект: вывод help ищется по подстрокам
// "RealVNC" / "TigerVNC" (порядок, как в asbru_conn).
func DetectVNC(help string) VNCBackend {
	if strings.Contains(help, "RealVNC") {
		return VNCRealVNC
	}
	if strings.Contains(help, "TigerVNC") {
		return VNCTigerVNC
	}
	return VNCGeneric
}

// SplitArgs разбирает опционную строку как шелл-командную строку:
// кавычки снимаются и группуют пробелы внутри значений; обратный слэш
// экранирует следующий символ (внутри кавычек — `\"` и `\\`).
func SplitArgs(s string) []string {
	var out []string
	var buf strings.Builder
	var quote byte
	started := false // явный ли аргумент: `""` — пустой, но существующий
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			started = true
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\'):
				i++
				buf.WriteByte(s[i])
			default:
				buf.WriteByte(c)
			}
		case c == '\\' && i+1 < len(s) && (s[i+1] == ' ' || s[i+1] == '"' || s[i+1] == '\'' || s[i+1] == '\\'):
			i++
			buf.WriteByte(s[i])
			started = true
		case c == '"' || c == '\'':
			quote = c
			started = true
		case c == ' ' || c == '\t':
			flushArg(&out, &buf, &started)
		default:
			buf.WriteByte(c)
			started = true
		}
	}
	flushArg(&out, &buf, &started)
	return out
}

func flushArg(out *[]string, buf *strings.Builder, started *bool) {
	if !*started {
		return
	}
	*out = append(*out, buf.String()) // пустой квотированный аргумент тоже важен
	buf.Reset()
	*started = false
}
