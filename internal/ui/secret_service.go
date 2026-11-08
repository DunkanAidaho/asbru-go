package ui

// Бэклог #7 (аудит): пароль соединения берётся из freedesktop Secret Service
// (org.freedesktop.secrets), если у соединения установлен флаг 'use secret
// service'. Источник — единый для KWallet5/6, встроенного секрет-сервиса
// KeePassXC и gnome-keyring (отдельный KWallet-бэкенд НЕ делаем). Чтение при
// запуске; запись/удаление — явные действия в редакторе (secret_service_actions).

import (
	"errors"
	"log/slog"

	"github.com/DunkanAidaho/asbru-go/internal/config"
	"github.com/DunkanAidaho/asbru-go/internal/secretservice"
)

// errSecretServiceUnavailable — сервис недоступен (нет бэкенда/шинной сессии).
var errSecretServiceUnavailable = errors.New("secret service недоступен")

// secretServiceLabel — метка элемента в SecretService (стабильный ключ поиска).
// КЛЮЧ = UUID (уникален): title/name могут совпадать у разных соединений → общий
// label смешал бы пароли (ревью MAJOR-2). Клиент ищет по атрибуту name=label.
func secretServiceLabel(uuid string) string {
	return "asbru:" + uuid
}

// secretServiceOpen — инжектируемая точка подключения к SecretService (тесты
// подменяют); default = New. Возвращает клиент (может быть nil при недоступном
// сервисе).
var secretServiceOpen = func() *secretservice.Client {
	c, err := secretservice.New()
	if err != nil {
		return nil
	}
	return c
}

// secretServiceLookup — инжектируемая точка Lookup(метка) (тесты).
var secretServiceLookup = func(label string) (string, error) {
	c := secretServiceOpen()
	if c == nil {
		return "", errSecretServiceUnavailable
	}
	defer c.CloseSession()
	defer c.Close()
	return c.Lookup(label)
}

// secretServiceLookupMany — разрешение нескольких меток ОДНИМ клиентом (MINOR-11):
// 1 подключение + 1 DH-рукопожатие, а не N изолированных open() на каждую метку
// (раньше resolveSecretServiceCreds дёргал Lookup 4 раза → 4 коннекта). Возвращает
// найденные (label → пароль); отсутствующие/ошибочные метки просто не в карте.
var secretServiceLookupMany = func(labels []string) (map[string]string, error) {
	c := secretServiceOpen()
	if c == nil {
		return nil, errSecretServiceUnavailable
	}
	defer c.CloseSession()
	defer c.Close()
	out := make(map[string]string, len(labels))
	for _, l := range labels {
		if v, err := c.Lookup(l); err == nil && v != "" {
			out[l] = v
		}
	}
	return out, nil
}

// secretServiceStore — явное сохранение пароля соединения в SecretService
// (по метке). Инжектируемый Store (тесты). Действие из редактора соединения.
var secretServiceStore = func(label, password string) error {
	c := secretServiceOpen()
	if c == nil {
		return errSecretServiceUnavailable
	}
	defer c.CloseSession()
	defer c.Close()
	return c.Store(label, password)
}

// secretServiceDelete — явное удаление пароля из SecretService. Инжектируемый.
var secretServiceDelete = func(label string) error {
	c := secretServiceOpen()
	if c == nil {
		return errSecretServiceUnavailable
	}
	defer c.CloseSession()
	defer c.Close()
	return c.Delete(label)
}

// secretServiceGetByUUID — инжектируемая точка GetByUUID (точечное извлечение
// KeePass-записи по Uuid). Возвращает метаданные + пароль этой ОДНОЙ записи.
// Вся база паролей НЕ разворачивается (доступ по ссылке/Uuid, как Remmina).
var secretServiceGetByUUID = func(uuid string) (secretservice.Entry, string, error) {
	c := secretServiceOpen()
	if c == nil {
		return secretservice.Entry{}, "", errSecretServiceUnavailable
	}
	defer c.Close()
	return c.GetByUUID(uuid)
}

// secretServiceList — инжектируемая точка List() (метаданные без паролей) для
// UI-диалога выбора KeePass-записи (#4).
var secretServiceList = func() ([]secretservice.Entry, error) {
	c := secretServiceOpen()
	if c == nil {
		return nil, errSecretServiceUnavailable
	}
	defer c.CloseSession()
	defer c.Close()
	return c.List()
}

// secretServiceFieldLabel — метка секрета в SecretService: asbru:<uuid>[:field].
// Поле "" → базовый пароль соединения (как хранят Store/Delete).
func secretServiceFieldLabel(uuid, field string) string {
	if field == "" {
		return "asbru:" + uuid
	}
	return "asbru:" + uuid + ":" + field
}

// resolveSecretServiceCreds — use secret service=1: из SecretService по меткам
// asbru:<uuid>[:passphrase|:proxy|:jump] берутся пароль соединения, пароль-фраза
// SSH-ключа и пароли прокси/jump (бэклог #7-x). MINOR-11: все метки читаются
// ОДНИМ клиентом (один D-Bus-коннект + DH-рукопожатие), а не четырьмя.
// Отсутствующие/пустые метки → остаётся сохранённое значение. Возвращает копию
// env, если хоть что-то подставлено.
func resolveSecretServiceCreds(env *config.Environment, uuid string) (*config.Environment, bool) {
	if env == nil || env.UseSecretService != 1 {
		return env, false
	}
	e := *env
	labels := []string{
		secretServiceFieldLabel(uuid, ""),
		secretServiceFieldLabel(uuid, "passphrase"),
		secretServiceFieldLabel(uuid, "proxy"),
		secretServiceFieldLabel(uuid, "jump"),
	}
	vals, err := secretServiceLookupMany(labels)
	if err != nil {
		return env, false
	}
	resolved := false
	set := func(field string, dst *string) {
		if v, ok := vals[secretServiceFieldLabel(uuid, field)]; ok && v != "" {
			*dst = v
			resolved = true
		}
	}
	set("", &e.Pass) // базовый пароль
	set("passphrase", &e.Passphrase)
	set("proxy", &e.ProxyPass)
	set("jump", &e.JumpPass)
	if !resolved {
		return env, false
	}
	return &e, true
}

// resolveKPXByUUID — бэклог #7-продолжение: соединение хранит ссылку на KeePass-
// запись по Uuid; пароль берётся точечно из SecretService (KeePassXC возвращает
// ТОЛЬКО эту запись). При недоступном сервисе/не найдено — сохранённый пароль
// (fallback). Возвращает (env, resolved).
// Приоритет: Uuid-ссылка > SearchPassOnKPX title-regexp > сохранённый.
func resolveKPXByUUID(env *config.Environment) (*config.Environment, bool) {
	if env.KPXUUID == "" {
		return env, false
	}
	entry, pw, err := secretServiceGetByUUID(env.KPXUUID)
	if err != nil || pw == "" {
		slog.Warn("use KeePass Uuid: не удалось получить пароль — использую сохранённый",
			"name", env.TitleOrName(), "kpx-uuid", env.KPXUUID, "err", err)
		return env, false
	}
	e := *env
	e.Pass = pw
	_ = entry // метаданные — для UI/логов; пароль подставлен
	return &e, true
}

// StoreSecretServiceCreds — сохранить непустые секреты соединения (пароль + пароль-фраза
// + прокси/jump) в SecretService по меткам asbru:<uuid>[:field]. Инжектируемый Store.
func StoreSecretServiceCreds(env *config.Environment, uuid string) error {
	if env == nil {
		return nil
	}
	creds := []struct{ field, value string }{
		{"", env.Pass}, {"passphrase", env.Passphrase},
		{"proxy", env.ProxyPass}, {"jump", env.JumpPass},
	}
	for _, c := range creds {
		if c.value == "" {
			continue
		}
		if err := secretServiceStore(secretServiceFieldLabel(uuid, c.field), c.value); err != nil {
			return err
		}
	}
	return nil
}

// DeleteSecretServiceCreds — удалить секреты соединения из SecretService (все метки).
func DeleteSecretServiceCreds(env *config.Environment, uuid string) error {
	if env == nil {
		return nil
	}
	for _, field := range []string{"", "passphrase", "proxy", "jump"} {
		_ = secretServiceDelete(secretServiceFieldLabel(uuid, field))
	}
	return nil
}

// Глобальный GUI-пароль (cfg.Defaults.GUIPassword) через SecretService (item 7-x):
// хранится ЗАШИФРОВАННЫЙ блоб (как в конфиге — guiPassSet) под меткой asbru:gui.
const guiPassLabel = "asbru:gui"

// resolveGUIPasswordFromSS — если use gui password=1 и в SecretService есть
// зашифрованный GUI-пароль (asbru:gui) — подставить в defaults.GUIPassword
// (иначе остаётся сохранённое значение). Возвращает true, если подставлен.
// Вызывается до guiPassGate.
func resolveGUIPasswordFromSS(d *config.Defaults) bool {
	if d == nil || d.UseGUIPassword != 1 {
		return false
	}
	v, err := secretServiceLookup(guiPassLabel)
	if err != nil || v == "" {
		return false
	}
	d.GUIPassword = v
	return true
}

// StoreGUIPasswordToSS — сохранить зашифрованный GUI-пароль в SecretService.
// Пустое значение — no-op (не затираем существующий секрет пустым).
func StoreGUIPasswordToSS(d *config.Defaults) error {
	if d == nil || d.GUIPassword == "" {
		return nil
	}
	return secretServiceStore(guiPassLabel, d.GUIPassword)
}

// DeleteGUIPasswordFromSS — удалить GUI-пароль из SecretService. Идемпотентно:
// отсутствующая запись — не ошибка (best-effort cleanup).
func DeleteGUIPasswordFromSS() error {
	_ = secretServiceDelete(guiPassLabel)
	return nil
}
