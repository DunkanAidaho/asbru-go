package secretservice

// D-Bus-клиент freedesktop Secret Service (org.freedesktop.secrets),
// адаптированный из go-keychain (keybase/go.dbus) на godbus/dbus/v5.
// Крипто-сессия — dh-ietf1024-sha256-aes128-cbc-pkcs7 (session.go).
//
// Публичный API для Ásbrú: Lookup(label)/Store(label,pass)/Delete(label) в
// default-коллекции, инжектируемый sessionBus (тесты крутят фейковый dbus-daemon,
// как internal/tray).

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/godbus/dbus/v5"
)

// ErrNotFound — элемент с данной меткой/атрибутами не найден.
var ErrNotFound = errors.New("secretservice: item not found")

// Имена интерфейсов/путей Secret Service. ВАЖНО: полный интерфейс —
// org.freedesktop.Secret.* (camelCase: .Service/.Collection/.Item/.Session/.Prompt),
// а org.freedesktop.secrets (строчные) — только имя ШИНЫ (bus name). Раньше
// ServiceIface был строчным — реален бэкенд не отвечал («No such interface»),
// живо проверено на KeePassXC (интерфейс org.freedesktop.Secret.Collection).
const (
	ServiceIface   = "org.freedesktop.Secret"  // префикс интерфейсов
	BusName        = "org.freedesktop.secrets" // имя шины (строчные)
	ServicePath    = dbus.ObjectPath("/org/freedesktop/secrets")
	DefaultColPath = dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")
	NullPrompt     = dbus.ObjectPath("/")
)

// authMode — алгоритм крипто-сессии.
type authMode string

const (
	authPlain authMode = "plain"
	authDHAES authMode = "dh-ietf1024-sha256-aes128-cbc-pkcs7"
)

// sessionBus — подключение к сессионной шине (инжектируемо в тестах).
// PRODUCTION использует dbus.ConnectSessionBus — свежее ИЗОЛИРОВАННОЕ соединение
// с полным рукопожатием (Dial+Auth+Hello), готовое к D-Bus-вызовам. НЕ dbus.SessionBus
// (shared singleton, использование tray: закрытие разорвало бы трей — MAJOR-1) и
// НЕ SessionBusPrivate (без Hello — зависает на вызовах; живо проверено на KeePassXC).
// Изолированное соединение можно безопасно закрывать в New()-error-пути и Close().
var sessionBus = dbus.ConnectSessionBus

// promptMatch — match-правило для org.freedesktop.Secret.Prompt.Completed.
// conn.Signal(ch) лишь регистрирует ЛОКАЛЬНЫЙ канал: широковещательные сигналы
// шина доставляет только при наличии match-правила (AddMatchSignal). Без него
// promptAndWait (CreateItem/Delete) висел до 30-секундного таймаута.
var promptMatch = []dbus.MatchOption{
	dbus.WithMatchInterface(ServiceIface + ".Prompt"),
	dbus.WithMatchMember("Completed"),
}

// subscribePromptSignals — локальный канал + match-правило. Возвращает ошибку,
// если шина не приняла AddMatch (тогда локальная подписка снимается).
func subscribePromptSignals(conn *dbus.Conn, ch chan *dbus.Signal) error {
	conn.Signal(ch)
	if err := conn.AddMatchSignal(promptMatch...); err != nil {
		conn.RemoveSignal(ch)
		return err
	}
	return nil
}

// unsubscribePromptSignals — снять канал и match-правило (best-effort: при
// закрытом соединении RemoveMatch уже не имеет смысла).
func unsubscribePromptSignals(conn *dbus.Conn, ch chan *dbus.Signal) {
	conn.RemoveSignal(ch)
	_ = conn.RemoveMatchSignal(promptMatch...)
}

// Attributes — карта атрибутов элемента (поиск по exact-совпадению).
type Attributes map[string]string

// Secret — структура секрета Secret Service (Session/Parameters/Value/ContentType).
type Secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// Client — соединение с Secret Service + открытая крипто-сессия.
type Client struct {
	conn     *dbus.Conn
	signalCh chan *dbus.Signal
	session  *session
}

type session struct {
	path   dbus.ObjectPath
	aesKey []byte
	mode   authMode
}

// openTimeout — таймаут OpenSession (D-Bus-вызов может вешать; как go-keychain).
const openTimeout = 10 * time.Second

// openMode — алгоритм крипто-сессии по умолчанию (DHAES для продакшена;
// тесты могут переключить на "plain", чтобы избежать DH-инференса в фейке).
var openMode authMode = authDHAES

// New — подключение + OpenSession. Изолированный D-Bus-коннект (SessionBusPrivate),
// поэтому Client.Close() безопасен и не трогает общее соединение (tray).
func New() (*Client, error) {
	conn, err := sessionBus()
	if err != nil {
		return nil, err
	}
	ch := make(chan *dbus.Signal, 16)
	if err := subscribePromptSignals(conn, ch); err != nil {
		conn.Close()
		return nil, err
	}
	c := &Client{conn: conn, signalCh: ch}
	sess, err := c.openSession(openMode)
	if err != nil {
		conn.Close()
		return nil, err
	}
	c.session = sess
	return c, nil
}

// Close — завершить работу клиента. Стирает AES-ключ сессии (гигиена кредов) и
// закрывает ИЗОЛИРОВАННОЕ D-Bus-соединение (ConnectSessionBus — свежее, не shared
// с tray) + отменяет подписку на сигналы (иначе утечка канала на долгоживущей
// шине — ревью). CloseSession уже вызван внутри (идемпотентно, best-effort).
func (c *Client) Close() error {
	if c.conn != nil && c.signalCh != nil {
		unsubscribePromptSignals(c.conn, c.signalCh)
	}
	c.CloseSession()
	if c.session != nil {
		for i := range c.session.aesKey {
			c.session.aesKey[i] = 0
		}
		c.session.aesKey = nil
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) serviceObj() dbus.BusObject {
	// dest = имя ШИНЫ (BusName), а не интерфейс.
	return c.conn.Object(BusName, ServicePath)
}

func (c *Client) obj(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(BusName, path)
}

// openSession — OpenSession + произведение AES-ключа из DH-руки.
func (c *Client) openSession(mode authMode) (*session, error) {
	sess := &session{mode: mode}
	var input dbus.Variant
	var group *dhGroup
	var private *big.Int
	switch mode {
	case authPlain:
		input = dbus.MakeVariant("")
	case authDHAES:
		group = rfc2409SecondOakleyGroup()
		priv, pub, err := group.NewKeypair()
		if err != nil {
			return nil, err
		}
		private = priv
		input = dbus.MakeVariant(pub.Bytes())
	default:
		return nil, errors.New("secretservice: unknown auth mode")
	}

	type openResp struct {
		output dbus.Variant
		path   dbus.ObjectPath
	}
	respCh := make(chan openResp, 1)
	errCh := make(chan error, 1)
	go func() {
		var resp openResp
		err := c.serviceObj().
			Call(ServiceIface+".Service.OpenSession", 0, mode, input).
			Store(&resp.output, &resp.path)
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	var output dbus.Variant
	var path dbus.ObjectPath
	select {
	case resp := <-respCh:
		output, path = resp.output, resp.path
	case err := <-errCh:
		return nil, err
	case <-time.After(openTimeout):
		return nil, errors.New("secretservice: OpenSession timed out")
	}
	sess.path = path

	if mode == authDHAES {
		theirPubBytes, ok := output.Value().([]byte)
		if !ok {
			return nil, errors.New("secretservice: bad algorithm output")
		}
		theirPub := new(big.Int)
		theirPub.SetBytes(theirPubBytes)
		aesKey, err := group.dhKDF(theirPub, private)
		if err != nil {
			return nil, err
		}
		sess.aesKey = aesKey
	}
	return sess, nil
}

// CloseSession — закрыть крипто-сессию (best-effort).
func (c *Client) CloseSession() {
	if c.session != nil && c.conn != nil {
		_ = c.obj(c.session.path).Call(ServiceIface+".Session.Close", 0).Err
	}
}

// searchItems — SearchItems в коллекции по атрибутам → пути элементов.
func (c *Client) searchItems(col dbus.ObjectPath, attrs Attributes) ([]dbus.ObjectPath, error) {
	var items []dbus.ObjectPath
	if err := c.obj(col).Call(ServiceIface+".Collection.SearchItems", 0, map[string]string(attrs)).Store(&items); err != nil {
		return nil, err
	}
	return items, nil
}

// getSecret — GetSecret (расшифровка через сессию).
func (c *Client) getSecret(item dbus.ObjectPath) ([]byte, error) {
	var sec Secret
	if err := c.obj(item).Call(ServiceIface+".Item.GetSecret", 0, c.session.path).Store(&sec); err != nil {
		return nil, err
	}
	switch c.session.mode {
	case authPlain:
		return sec.Value, nil
	case authDHAES:
		return unauthenticatedAESCBCDecrypt(sec.Parameters, sec.Value, c.session.aesKey)
	}
	return nil, errors.New("secretservice: unknown mode")
}

// newSecret — упаковка секрета (шифрование при DHAES).
func (c *Client) newSecret(secretBytes []byte) (Secret, error) {
	switch c.session.mode {
	case authPlain:
		return Secret{Session: c.session.path, Parameters: nil, Value: secretBytes, ContentType: "application/octet-stream"}, nil
	case authDHAES:
		iv, ct, err := unauthenticatedAESCBCEncrypt(secretBytes, c.session.aesKey)
		if err != nil {
			return Secret{}, err
		}
		return Secret{Session: c.session.path, Parameters: iv, Value: ct, ContentType: "application/octet-stream"}, nil
	}
	return Secret{}, errors.New("secretservice: unknown mode")
}

// createItem — CreateItem в коллекции (replace если надо).
func (c *Client) createItem(col dbus.ObjectPath, props map[string]dbus.Variant, secret Secret, replace bool) (dbus.ObjectPath, error) {
	var item dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := c.obj(col).Call(ServiceIface+".Collection.CreateItem", 0, props, secret, replace).Store(&item, &prompt); err != nil {
		return "", err
	}
	if _, err := c.promptAndWait(prompt); err != nil {
		return "", err
	}
	return item, nil
}

// deleteItem — Delete элемента (best-effort).
func (c *Client) deleteItem(item dbus.ObjectPath) error {
	var prompt dbus.ObjectPath
	if err := c.obj(item).Call(ServiceIface+".Item.Delete", 0).Store(&prompt); err != nil {
		return err
	}
	_, err := c.promptAndWait(prompt)
	return err
}

// promptAndWait — дождаться Prompt.Completed (или NullPrompt).
func (c *Client) promptAndWait(prompt dbus.ObjectPath) (*dbus.Variant, error) {
	if prompt == NullPrompt {
		return nil, nil
	}
	if err := c.obj(prompt).Call(ServiceIface+".Prompt.Prompt", 0, "").Err; err != nil {
		return nil, err
	}
	for {
		select {
		case sig, ok := <-c.signalCh:
			if !ok {
				return nil, errors.New("secretservice: prompt channel closed")
			}
			if sig == nil || sig.Name != ServiceIface+".Prompt.Completed" {
				continue
			}
			var dismissed bool
			var paths dbus.Variant
			if err := dbus.Store(sig.Body, &dismissed, &paths); err != nil {
				return nil, err
			}
			if dismissed {
				return nil, errors.New("secretservice: prompt dismissed")
			}
			return &paths, nil
		case <-time.After(30 * time.Second):
			return nil, errors.New("secretservice: prompt timed out")
		}
	}
}

// computeAttr — атрибуты для label-поиска: application=asbru, name=label.
func computeAttr(label string) Attributes {
	return Attributes{"application": "asbru", "name": label}
}

// newProperties — свойства нового элемента (Label + Attributes).
func newProperties(label string, attrs Attributes) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		ServiceIface + ".Item.Label":      dbus.MakeVariant(label),
		ServiceIface + ".Item.Attributes": dbus.MakeVariant(map[string]string(attrs)),
	}
}

// Entry — метаданные KeePass-записи (из Secret Service элемента). Пароль НЕ
// включён (ток с метаданными; secret достаётся отдельно по Uuid).
type Entry struct {
	UUID     string
	Title    string
	Username string
	URL      string
}

// uniqueItem — единый guard уникальности результата SearchItems. Атрибуты
// (Uuid/label) считаются уникальными, но сеть этого не доказывает: при >1
// брать items[0] нельзя — вернули бы/удалили ЧУЖУЮ запись. what — имя атрибута
// для диагностики.
func uniqueItem(items []dbus.ObjectPath, what string) (dbus.ObjectPath, error) {
	switch len(items) {
	case 0:
		return "", ErrNotFound
	case 1:
		return items[0], nil
	default:
		return "", fmt.Errorf("secretservice: non-unique %s match", what)
	}
}

// GetByUUID — точечное извлечение записи KeePassXC по Uuid (атрибут "Uuid").
// KeePassXC хранит элементы, атрибутированные Uuid=...; SearchItems по этому
// атрибуту возвращает ТОЛЬКО нужную запись → GetSecret (расшифровка) → пароль.
// Вся база НЕ разворачивается (доступ по индексу/ссылке, как Remmina).
func (c *Client) GetByUUID(uuid string) (Entry, string, error) {
	items, err := c.searchItems(DefaultColPath, Attributes{"Uuid": uuid})
	if err != nil {
		return Entry{}, "", err
	}
	// A (ревью): Uuid уникален, но не доказуемо по сети. РЕЖЕМ на >1 — иначе
	// несфильтрованный SearchItems подсунул бы ЧУЖОЙ пароль (не least-privilege,
	// а wrong-secret). Только ровно один элемент.
	item, err := uniqueItem(items, "Uuid")
	if err != nil {
		return Entry{}, "", err
	}
	entry, err := c.entryMeta(item)
	if err != nil {
		return Entry{}, "", err
	}
	pw, err := c.getSecret(item)
	if err != nil {
		return Entry{}, "", err
	}
	return entry, string(pw), nil
}

// entryMeta — метаданные элемента (Title/UserName/URL + Uuid из Attributes).
func (c *Client) entryMeta(item dbus.ObjectPath) (Entry, error) {
	var attrs dbus.Variant
	if err := c.obj(item).Call("org.freedesktop.DBus.Properties.Get", 0, ServiceIface+".Item", "Attributes").Store(&attrs); err != nil {
		return Entry{}, err
	}
	// B (ревью): не молчать при не-a{ss} (иное бэкенд/протокол) — иначе пустые
	// Uuid/Title были бы молчаливой ошибкой (особенно в List, где отсутствие
	// Uuid фильтруется). Поверхностная ошибка — явная.
	a, ok := attrs.Value().(map[string]string)
	if !ok {
		return Entry{}, errors.New("secretservice: item Attributes не a{ss}")
	}
	return Entry{
		UUID:     a["Uuid"],
		Title:    a["Title"],
		Username: a["UserName"],
		URL:      a["URL"],
	}, nil
}

// List — метаданные всех записей в default-коллекции (для UI-выбора), БЕЗ паролей.
func (c *Client) List() ([]Entry, error) {
	items, err := c.listItems(DefaultColPath)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		e, err := c.entryMeta(it)
		if err != nil {
			continue // пропустить недоступный элемент
		}
		if e.UUID == "" {
			continue // без Uuid неинтересно для ссылки
		}
		out = append(out, e)
	}
	return out, nil
}

// listItems — Items-свойство коллекции (все пути элементов).
func (c *Client) listItems(col dbus.ObjectPath) ([]dbus.ObjectPath, error) {
	var iv dbus.Variant
	if err := c.obj(col).Call("org.freedesktop.DBus.Properties.Get", 0, ServiceIface+".Collection", "Items").Store(&iv); err != nil {
		return nil, err
	}
	paths, _ := iv.Value().([]dbus.ObjectPath)
	return paths, nil
}

// Lookup — пароль по label-метке. ErrNotFound, если нет/пусто. Дубликаты
// метки — ошибка неоднозначности (брать items[0] нельзя: чужой секрет).
func (c *Client) Lookup(label string) (string, error) {
	items, err := c.searchItems(DefaultColPath, computeAttr(label))
	if err != nil {
		return "", err
	}
	item, err := uniqueItem(items, "label")
	if err != nil {
		return "", err
	}
	pw, err := c.getSecret(item)
	if err != nil {
		return "", err
	}
	return string(pw), nil
}

// Store — сохранить пароль по label-метке (replace=true, идемпотентно).
func (c *Client) Store(label, password string) error {
	secret, err := c.newSecret([]byte(password))
	if err != nil {
		return err
	}
	_, err = c.createItem(DefaultColPath, newProperties(label, computeAttr(label)), secret, true)
	return err
}

// Delete — удалить элемент по label-метке. ErrNotFound, если нет. При
// дубликатах — ошибка неоднозначности (не удаляем чужую запись).
func (c *Client) Delete(label string) error {
	items, err := c.searchItems(DefaultColPath, computeAttr(label))
	if err != nil {
		return err
	}
	item, err := uniqueItem(items, "label")
	if err != nil {
		return err
	}
	return c.deleteItem(item)
}
