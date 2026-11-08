package method

import "sort"

// M37 (п.4): единый дескриптор диалекта — «метод → клиент подключения →
// поддерживаемые опции → разделы редактора». Раньше это знание было размазано:
// «встроенный ли клиент» решал ui-свитч по именам, «показывать ли раздел VNC» —
// безусловная строка формы, галки — отдельный реестр. Теперь источник один,
// а редактор и запуск читают его.

// ClientKind — чем подключается метод.
type ClientKind int

const (
	// ClientExternal — внешний процесс в PTY (ssh, telnet, sftp, mosh…).
	ClientExternal ClientKind = iota
	// ClientEmbedded — встроенный в вкладку клиент (libfreerdp3 / libvncclient).
	ClientEmbedded
)

// String — для логов/тестов.
func (c ClientKind) String() string {
	if c == ClientEmbedded {
		return "embedded"
	}
	return "external"
}

// DialectSpec — дескриптор диалекта для редактора и запуска.
type DialectSpec struct {
	Dialect   Dialect
	Client    ClientKind
	AuthTypes []AuthKind
	Flags     []OptionToggle // галки-опции (токен в строке «Опции»)
	Values    []OptionValue  // опции-значения (комбо)
	// VNCSecurity — раздел «Безопасность VNC» (VeNCrypt): только VNC.
	VNCSecurity bool
	// Commandable — терминально-командные поля (prepend/postpend/send string,
	// «Проброс»): только командным методам, не RDP/VNC.
	Commandable bool
}

// SpecFor — дескриптор диалекта. Источники — реестры (OptionsFor/
// OptionValuesFor/FeaturesFor), поэтому расхождение невозможно (тест).
func SpecFor(d Dialect) DialectSpec {
	return DialectSpec{
		Dialect:     d,
		Client:      clientKindOf(d),
		AuthTypes:   FeaturesFor(d).AuthTypes,
		Flags:       OptionsFor(d),
		Values:      OptionValuesFor(d),
		VNCSecurity: d == VNC,
		Commandable: CommandCapable(d),
	}
}

// clientKindOf — встроенный клиент у RDP/VNC (M33: рисуем в вкладке,
// а не запускаем внешнее окно) и у PVE (консоль гостя — VNC-прокси REST API,
// виджет рисуется внутри вкладки).
func clientKindOf(d Dialect) ClientKind {
	switch d {
	case RDesktop, XFreeRDP, VNC, PVE:
		return ClientEmbedded
	}
	return ClientExternal
}

// AllDialects — все диалекты реестра в детерминированном порядке (итерация по
// карте даёт произвольный порядок — тесты и UI должны быть стабильны).
func AllDialects() []Dialect {
	out := make([]Dialect, 0, len(displayNames))
	for d := range displayNames {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
