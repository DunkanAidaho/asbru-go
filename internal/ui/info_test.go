package ui

// M31: панель Info — строки "метка: значение" о соединении (pure, тестируемо).

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func TestConnInfoLines(t *testing.T) {
	env := &config.Environment{
		Name: "web", Description: "prod web", IP: "10.0.0.1",
		Port: "22", User: "root", Method: "SSH", AuthType: "publickey",
	}
	lines := connInfoLines(env)
	if len(lines) == 0 {
		t.Fatal("нет строк")
	}
	want := map[string]string{
		"Имя": "web", "Описание": "prod web", "IP": "10.0.0.1",
		"Порт": "22", "Пользователь": "root", "Метод": "SSH",
		"Тип аутентификации": "publickey",
	}
	got := map[string]string{}
	for _, l := range lines {
		got[l.Label] = l.Value
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// пустые поля пропускаются
	if n := len(connInfoLines(&config.Environment{Name: "x"})); n != 1 {
		t.Fatalf("len=%d want 1 (только имя)", n)
	}
}
