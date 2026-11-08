package ui

// Бэклог #7: resolveSecretServicePassword — пароль из SecretService по метке
// asbru:<uuid> при флаге 'use secret service'. Инжектируемый secretServiceLookup.
// Ключ — UUID (уникален), не title/name (ревью MAJOR-2: коллизия смешала бы пароли).

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/secretservice"
)

const testUUID = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

func TestSecretServiceLabel(t *testing.T) {
	// ключ — только UUID (не title/name).
	if got := secretServiceLabel(testUUID); got != "asbru:"+testUUID {
		t.Fatalf("label = %q", got)
	}
}

// patchSecretLookup — временная замена secretServiceLookup.
func patchSecretLookup(f func(string) (string, error)) func() {
	old := secretServiceLookup
	secretServiceLookup = f
	return func() { secretServiceLookup = old }
}

// patchSecretLookupMany — временная замена secretServiceLookupMany (MINOR-11).
func patchSecretLookupMany(f func([]string) (map[string]string, error)) func() {
	old := secretServiceLookupMany
	secretServiceLookupMany = f
	return func() { secretServiceLookupMany = old }
}

func TestResolveKPXByUUIDOn(t *testing.T) {
	var gotUUID string
	old := secretServiceGetByUUID
	secretServiceGetByUUID = func(u string) (secretservice.Entry, string, error) {
		gotUUID = u
		return secretservice.Entry{UUID: u, Title: "fb"}, "kpx-pw", nil
	}
	defer func() { secretServiceGetByUUID = old }()

	env := &config.Environment{Name: "web", Pass: "saved", KPXUUID: testUUID}
	got, resolved := resolveKPXByUUID(env)
	if gotUUID != testUUID {
		t.Fatalf("GetByUUID uuid = %q, want %q", gotUUID, testUUID)
	}
	if !resolved || got.Pass != "kpx-pw" {
		t.Fatalf("resolved=%v pass=%q, want true/kpx-pw", resolved, got.Pass)
	}
	if env.Pass != "saved" {
		t.Fatalf("исходный env.Pass изменился: %q", env.Pass)
	}
}

func TestResolveKPXByUUIDEmptyLink(t *testing.T) {
	// нет KPXUUID → не обращаемся к сервису, пароль как есть.
	called := false
	old := secretServiceGetByUUID
	secretServiceGetByUUID = func(u string) (secretservice.Entry, string, error) {
		called = true
		return secretservice.Entry{}, "", nil
	}
	defer func() { secretServiceGetByUUID = old }()

	env := &config.Environment{Name: "web", Pass: "saved"}
	got, resolved := resolveKPXByUUID(env)
	if called {
		t.Fatal("пустой KPXUUID — GetByUUID не должен вызываться")
	}
	if resolved || got.Pass != "saved" {
		t.Fatalf("resolved=%v pass=%q, want false/saved", resolved, got.Pass)
	}
}

func TestResolveKPXByUUIDNotFound(t *testing.T) {
	// не найдено → сохранённый пароль (fallback).
	old := secretServiceGetByUUID
	secretServiceGetByUUID = func(u string) (secretservice.Entry, string, error) {
		return secretservice.Entry{}, "", secretservice.ErrNotFound
	}
	defer func() { secretServiceGetByUUID = old }()

	env := &config.Environment{Name: "web", Pass: "saved", KPXUUID: testUUID}
	got, resolved := resolveKPXByUUID(env)
	if resolved || got.Pass != "saved" {
		t.Fatalf("resolved=%v pass=%q, want false/saved (not found)", resolved, got.Pass)
	}
}

func TestResolveKPXByUUIDEmptyPass(t *testing.T) {
	old := secretServiceGetByUUID
	secretServiceGetByUUID = func(u string) (secretservice.Entry, string, error) {
		return secretservice.Entry{UUID: u}, "", nil
	}
	defer func() { secretServiceGetByUUID = old }()

	env := &config.Environment{Name: "web", Pass: "saved", KPXUUID: testUUID}
	got, resolved := resolveKPXByUUID(env)
	if resolved || got.Pass != "saved" {
		t.Fatalf("resolved=%v pass=%q, want false/saved (empty)", resolved, got.Pass)
	}
}

// бэклог #7-x: resolveSecretServiceCreds — пароль + пароль-фраза + прокси/jump.
func TestResolveSecretServiceCreds(t *testing.T) {
	labels := map[string]string{
		"asbru:" + testUUID:                 "ss-pw",
		"asbru:" + testUUID + ":passphrase": "ss-pp",
		"asbru:" + testUUID + ":proxy":      "ss-px",
		"asbru:" + testUUID + ":jump":       "ss-jp",
	}
	restore := patchSecretLookupMany(func(ls []string) (map[string]string, error) {
		out := make(map[string]string, len(ls))
		for _, l := range ls {
			if v, ok := labels[l]; ok {
				out[l] = v
			}
		}
		return out, nil
	})
	defer restore()

	env := &config.Environment{Name: "web1", Pass: "saved", Passphrase: "saved-pp",
		ProxyPass: "saved-px", JumpPass: "saved-jp", UseSecretService: 1}
	got, resolved := resolveSecretServiceCreds(env, testUUID)
	if !resolved {
		t.Fatal("resolved = true (все метки есть)")
	}
	if got.Pass != "ss-pw" || got.Passphrase != "ss-pp" || got.ProxyPass != "ss-px" || got.JumpPass != "ss-jp" {
		t.Fatalf("creds по SS = %q/%q/%q/%q",
			got.Pass, got.Passphrase, got.ProxyPass, got.JumpPass)
	}
	if env.Pass != "saved" {
		t.Fatal("исходный env изменён")
	}
}

// частично: есть только пароль; пароль-фраза/прокси/jump отсутствуют → сохранённые.
func TestResolveSecretServiceCredsPartial(t *testing.T) {
	restore := patchSecretLookupMany(func(ls []string) (map[string]string, error) {
		out := make(map[string]string, len(ls))
		for _, l := range ls {
			if l == "asbru:"+testUUID {
				out[l] = "ss-pw"
			}
		}
		return out, nil
	})
	defer restore()

	env := &config.Environment{Name: "web1", Pass: "saved", Passphrase: "keep-pp",
		ProxyPass: "keep-px", JumpPass: "keep-jp", UseSecretService: 1}
	got, resolved := resolveSecretServiceCreds(env, testUUID)
	if !resolved {
		t.Fatal("resolved = true (пароль подставлен)")
	}
	if got.Pass != "ss-pw" || got.Passphrase != "keep-pp" ||
		got.ProxyPass != "keep-px" || got.JumpPass != "keep-jp" {
		t.Fatalf("частично: %q/%q/%q/%q",
			got.Pass, got.Passphrase, got.ProxyPass, got.JumpPass)
	}
}

// выключен use secret service → всё как было, resolved=false.
func TestResolveSecretServiceCredsOff(t *testing.T) {
	restore := patchSecretLookup(func(label string) (string, error) { return "x", nil })
	defer restore()
	env := &config.Environment{Name: "web1", Pass: "saved", UseSecretService: 0}
	got, resolved := resolveSecretServiceCreds(env, testUUID)
	if resolved || got.Pass != "saved" {
		t.Fatalf("off: resolved=%v pass=%q", resolved, got.Pass)
	}
}

// MINOR-11: разрешение 4 меток (пароль/пароль-фраза/прокси/jump) должно открывать
// ОДИН клиент (1 подключение + 1 DH-рукопожатие), а не 4 изолированных open().
func TestResolveSecretServiceCredsOpensOneClient(t *testing.T) {
	openCount := 0
	oldOpen := secretServiceOpen
	secretServiceOpen = func() *secretservice.Client {
		openCount++
		return nil // реальный клиент тут не нужен — считаем вызовы open
	}
	defer func() { secretServiceOpen = oldOpen }()

	env := &config.Environment{Name: "web1", Pass: "saved", UseSecretService: 1}
	_, _ = resolveSecretServiceCreds(env, testUUID)
	if openCount != 1 {
		t.Fatalf("secretServiceOpen вызван %d раз, want 1 (один клиент на разрешение)", openCount)
	}
}

// item 7-x: GUI-пароль в SecretService — resolve/Store (зашифрованный блоб).
func TestResolveGUIPasswordFromSS(t *testing.T) {
	restore := patchSecretLookup(func(label string) (string, error) {
		if label != guiPassLabel {
			t.Fatalf("метка = %q, want %q", label, guiPassLabel)
		}
		return "enc-blob", nil
	})
	defer restore()
	d := &config.Defaults{UseGUIPassword: 1}
	if !resolveGUIPasswordFromSS(d) {
		t.Fatal("resolved = true")
	}
	if d.GUIPassword != "enc-blob" {
		t.Fatalf("GUIPassword = %q, want enc-blob", d.GUIPassword)
	}
}

func TestResolveGUIPasswordOff(t *testing.T) {
	restore := patchSecretLookup(func(label string) (string, error) { return "x", nil })
	defer restore()
	d := &config.Defaults{UseGUIPassword: 0}
	if resolveGUIPasswordFromSS(d) {
		t.Fatal("use gui password off → resolve=false")
	}
	if d.GUIPassword != "" {
		t.Fatal("GUIPassword не должен измениться")
	}
}

func TestStoreGUIPasswordEmptyNoop(t *testing.T) {
	// пустое значение — no-op, не затираем существующий секрет.
	if err := StoreGUIPasswordToSS(&config.Defaults{}); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if err := DeleteGUIPasswordFromSS(); err != nil {
		t.Fatalf("delete: %v", err)
	}
}
