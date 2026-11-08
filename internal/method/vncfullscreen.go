package method

import (
	"strconv"
	"strings"
)

// ParseVNCFullscreen — нужно ли встроенному VNC-клиенту «во весь экран».
//
// Встроенный клиент (M33, libvncclient) не запускает внешнюю программу, поэтому
// легаси-токены строки options сами по себе ничего не делают. Читаем РОВНО те
// формы, которые писали легаси-методы (новых токенов не вводим):
//
//   - «-fullscreen»            — vncviewer / RealVNC
//     (PACMethod_vncviewer.pm:140, PACMethod_realvnc.pm:163);
//   - «-FullScreen=N» (N > 0)  — TigerVNC (PACMethod_tigervnc.pm:143/161;
//     по умолчанию клиент пишет «-FullScreen=0» — это выключено).
//
// Сравнение — с ТОЧНЫМ легаси-написанием: «-fullscreen» (нижний регистр,
// vncviewer/RealVNC) и «-FullScreen=N» (TigerVNC). Регистр у этих токенов
// исторически значим, а новых форм не изобретаем: «-FULLSCREEN» или
// «-fullscreen=1» легаси-клиенты не понимали, значит и здесь это не флаг.
// Пустая/чужая строка (в т.ч. xfreerdp-«/fullscreen») → false.
func ParseVNCFullscreen(options string) bool {
	const fullscreenAssign = "-FullScreen="
	for _, tok := range SplitArgs(options) {
		if tok == "-fullscreen" {
			return true
		}
		if strings.HasPrefix(tok, fullscreenAssign) {
			if n, err := strconv.Atoi(tok[len(fullscreenAssign):]); err == nil && n > 0 {
				return true
			}
		}
	}
	return false
}
