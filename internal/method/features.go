package method

// M34: протокол-зависимые возможности редактора. Оригинал (латин. PACEdit.pm /
// PACUtils.pm): auth type — РОВНО 3 строки (userpass/publickey/manual), а
// «какими» они бывают — зависит от метода (только SSH/MOSH/SFTP допускают
// publickey; Generic/3270/cu — только manual). Модель здесь — «feature set» на
// диалект, чтобы редактор рисовал протокол-специфичные поля (issues 1–3 M34).

// AuthKind — значение 'auth type' (легаси PACEdit.pm:886/888/890).
type AuthKind string

const (
	AuthUserpass  AuthKind = "userpass"
	AuthPublickey AuthKind = "publickey"
	AuthManual    AuthKind = "manual"
)

// String — значение для конфига (легаси-строка).
func (a AuthKind) String() string { return string(a) }

// Features — протокол-зависимый набор полей редактора.
type Features struct {
	AuthTypes       []AuthKind // допустимые 'auth type' (в порядке комбо)
	HasPubKey       bool       // показывать Public key/Passphrase (только при AuthPublickey)
	HasAuthFallback bool       // 'auth fallback' (0/1) — SSH-подобные
	// Опции-галки и значения (для вкладки «Опции»), заполняются в OptionsFor.
}

// FeaturesFor — набор на диалект (по легаси PACUtils.pm set_sensitive).
func FeaturesFor(d Dialect) Features {
	switch d {
	case SSH, SFTP, MOSH:
		return Features{
			AuthTypes:       []AuthKind{AuthUserpass, AuthPublickey, AuthManual},
			HasPubKey:       true,
			HasAuthFallback: true,
		}
	case RDesktop, XFreeRDP, VNC, PVE, Telnet, FTP, WebDAV, SerialRemoteTTY:
		return Features{
			AuthTypes: []AuthKind{AuthUserpass, AuthManual},
		}
	case Generic, SerialCu, IBM3270, LocalShell:
		// auth-область вообще отключена → только manual (PACUtils.pm:628/737/1187).
		return Features{AuthTypes: []AuthKind{AuthManual}}
	default:
		return Features{}
	}
}

// ValidAuthType — допустим ли auth type для диалекта (редактор валидирует).
func ValidAuthType(d Dialect, a string) bool {
	k := AuthKind(a)
	for _, v := range FeaturesFor(d).AuthTypes {
		if v == k {
			return true
		}
	}
	return false
}

// CommandCapable — есть ли у метода терминально-командные опции (Команда
// перед/после, Строка отправки, sudo, autoreconnect/autossh, SOCKS, проброс
// портов). Чисто-графические методы (RDP/VNC) их НЕ используют — иначе вкладка
// «Опции» показывала SSH-галки на RDP-подключении (очный дебаг, issue 1).
// PVE — тоже не командный (fix-раунд 1): терминала нет, консоль гостя —
// VNC-прокси REST API (спека п.3 командных полей не предусматривает).
func CommandCapable(d Dialect) bool {
	switch d {
	case RDesktop, XFreeRDP, VNC, PVE:
		return false
	}
	return true
}
