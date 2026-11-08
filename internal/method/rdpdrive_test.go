package method

// M39: перенаправление диска встроенным RDP-клиентом. Источник — существующее
// поле «options» соединения в обоих диалектах: xfreerdp `/drive:share,path`
// (канон, который пишет редактор Ásbrú) и rdesktop `-r disk:share=path`
// (наследие Perl-конфигов). Разбор обязан повторять семантику самого FreeRDP:
// значение режется по запятым, берутся поля [0]=share и [1]=path; `\` и `/`
// в имени шэра заменяются на `__` (freerdp_sanitize_drive_name).

import (
	"reflect"
	"testing"
)

func TestParseRdpDrives(t *testing.T) {
	tests := []struct {
		name string
		opts string
		want []RdpDrive
	}{
		{"пусто", "", nil},
		{"только другие опции", "-k en-us +clipboard /size:1280x1024", nil},
		{"xfreerdp один", "/drive:share,/home/u", []RdpDrive{{Share: "share", Path: "/home/u"}}},
		{
			"xfreerdp несколько",
			"/drive:docs,/home/u/docs /drive:media,\"/mnt/my media\"",
			[]RdpDrive{{Share: "docs", Path: "/home/u/docs"}, {Share: "media", Path: "/mnt/my media"}},
		},
		{"rdesktop отдельным значением", "-r disk:share=/home/u", []RdpDrive{{Share: "share", Path: "/home/u"}}},
		{"rdesktop слитно", "-rdisk:share=/home/u", []RdpDrive{{Share: "share", Path: "/home/u"}}},
		{
			"rdesktop несколько",
			"-r disk:a=/x -r disk:b=/y",
			[]RdpDrive{{Share: "a", Path: "/x"}, {Share: "b", Path: "/y"}},
		},
		{"rdesktop значение в кавычках", `-r disk:share="/home/share"`, []RdpDrive{{Share: "share", Path: "/home/share"}}},
		{"rdesktop путь с пробелами", `-r disk:share="/home/my docs"`, []RdpDrive{{Share: "share", Path: "/home/my docs"}}},
		// FreeRDP: CommandLineParseCommaSeparatedValuesEx → params[1]=share,
		// params[2]=path; третий и далее поля игнорируются.
		{"лишние запятые как в FreeRDP", "/drive:s,/a,b", []RdpDrive{{Share: "s", Path: "/a"}}},
		// Имя устройства RDPDR не должно содержать слэшей: FreeRDP заменяет их на __.
		{"санитайз share", `/drive:a\b/c,/tmp`, []RdpDrive{{Share: "a__b__c", Path: "/tmp"}}},
		// Неполные/битые токены не превращаются в диск (иначе сервер увидит
		// шэр с пустым путём или пустым именем).
		{"без запятой", "/drive:share", nil},
		{"пустой share", "/drive:,/tmp", nil},
		{"пустой path", "/drive:s,", nil},
		{"rdesktop без =", "-r disk:share", nil},
		{"rdesktop пустой path", "-r disk:share=", nil},
		{"rdesktop пустой share", "-r disk:=/tmp", nil},
		// Путь НЕ тримится: пробелы внутри и по краям — часть имени.
		{"пробелы в пути сохраняются", `/drive:s,"/tmp/ x"`, []RdpDrive{{Share: "s", Path: "/tmp/ x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRdpDrives(tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseRdpDrives(%q) = %+v, want %+v", tt.opts, got, tt.want)
			}
		})
	}
}

// /drive: — известный ключ опций: не должен попадать в unknown (UI-предупреждение
// «ключ не поддерживается»), при этом каждый диск извлекается отдельным парсером.
func TestRdpDriveOptionIsKnown(t *testing.T) {
	_, unknown := MapRdpOptions("/drive:share,/home/u /bpp:16")
	if len(unknown) != 0 {
		t.Fatalf("unknown=%v, /drive: должен быть известен", unknown)
	}
}
