// Package pve — тонкий REST-клиент Proxmox VE API v2 (/api2/json).
// Референс: https://pve.proxmox.com/wiki/Proxmox_VE_API.
// Аутентификация: POST /access/ticket → PVEAuthCookie (cookie) +
// CSRFPreventionToken (заголовок мутаций); 401 на запросе после логина →
// один тихий релогин и один повтор запроса. Пароль держится в памяти клиента
// только ради этого релогина и никогда не попадает в логи.
package pve

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Error — ошибка PVE (HTTP-код + текст {errors}).
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("PVE %d: %s", e.Status, e.Message)
}

// Client — соединение с одним PVE (нода/балансир). НЕ потокобезопасен
// (используется из одной горутины-владельца; см. виджет).
type Client struct {
	base   string
	http   *http.Client
	user   string // учётные данные запомнены ради единственного тихого релогина
	pass   string // при 401; никуда не логируются
	ticket string
	csrf   string
}

func NewClient(base string, tlsSkip bool, timeout time.Duration) *Client {
	return &Client{
		base: strings.TrimSuffix(base, "/"),
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: tlsSkip}, //nolint:gosec // self-signed PVE — флаг соединения
			},
		},
	}
}

// Login — POST /access/ticket: обменивает логин/пароль на тикет
// (PVEAuthCookie) и CSRFPreventionToken.
func (c *Client) Login(user, pass string) error {
	return c.LoginCtx(context.Background(), user, pass)
}

// LoginCtx — логин с контекстом (релогин из живого запроса не висит после
// отмены операции — ревью 2, улучшение 3).
func (c *Client) LoginCtx(ctx context.Context, user, pass string) error {
	var out envelope
	if err := c.raw(ctx, http.MethodPost,
		"/api2/json/access/ticket",
		url.Values{"username": {user}, "password": {pass}}, &out, false); err != nil {
		return fmt.Errorf("логин %s: %w", user, err)
	}
	var tk struct {
		Ticket string `json:"ticket"`
		CSRF   string `json:"CSRFPreventionToken"`
	}
	if err := json.Unmarshal(out.Data, &tk); err != nil || tk.Ticket == "" {
		return fmt.Errorf("логин %s: тикет не получен: %w", user, err)
	}
	c.user, c.pass = user, pass
	c.ticket, c.csrf = tk.Ticket, tk.CSRF
	return nil
}

// Resource — одна запись GET /cluster/resources (нода, гость или стор).
type Resource struct {
	ID       string // "qemu/101", "node/pve1", "storage/pve1/local"
	Name     string // у нод поле name отсутствует — выводится из node
	Status   string
	NodeType string // node | qemu | lxc | storage
	Node     string
	Tags     string // "prod;web"
	VMID     int    // 0 у нод и сторов
}

// rawResource — как PVE отдаёт записи /cluster/resources: vmid бывает
// числом и строкой, name у нод отсутствует.
type rawResource struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Node   string  `json:"node"`
	Status string  `json:"status"`
	Tags   string  `json:"tags"`
	Name   *string `json:"name"`
	VMID   any     `json:"vmid"`
}

// Resources — GET /cluster/resources: состояние всего кластера одним вызовом.
func (c *Client) Resources(ctx context.Context) ([]Resource, error) {
	var raws []rawResource
	if err := c.doJSON(ctx, http.MethodGet, "/cluster/resources", nil, &raws); err != nil {
		return nil, fmt.Errorf("cluster/resources: %w", err)
	}
	rs := make([]Resource, 0, len(raws))
	for _, r := range raws {
		res := Resource{
			ID:       r.ID,
			NodeType: r.Type,
			Node:     r.Node,
			Status:   r.Status,
			Tags:     r.Tags,
		}
		switch v := r.VMID.(type) {
		case float64:
			res.VMID = int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				res.VMID = n
			}
		}
		if r.Name != nil {
			res.Name = *r.Name
		} else {
			res.Name = r.Node // у нод name нет — имя это имя ноды
		}
		rs = append(rs, res)
	}
	return rs, nil
}

// Guests — только гости (qemu + lxc).
func Guests(rs []Resource) []Resource { return filterResources(rs, "qemu", "lxc") }

// Nodes — только ноды.
func Nodes(rs []Resource) []Resource { return filterResources(rs, "node") }

// Storages — только хранилища.
func Storages(rs []Resource) []Resource { return filterResources(rs, "storage") }

// StoragesOn — хранилища конкретной ноды (диалог бэкапа предлагает только их).
func StoragesOn(rs []Resource, node string) []Resource {
	out := make([]Resource, 0, 4)
	for _, r := range Storages(rs) {
		if r.Node == node {
			out = append(out, r)
		}
	}
	return out
}

// filterResources — выборка ресурсов по типам с сохранением порядка.
func filterResources(rs []Resource, types ...string) []Resource {
	want := make(map[string]bool, len(types))
	for _, t := range types {
		want[t] = true
	}
	out := make([]Resource, 0, len(rs))
	for _, r := range rs {
		if want[r.NodeType] {
			out = append(out, r)
		}
	}
	return out
}

// Running — ресурс в состоянии "running" (для нод PVE отдаёт "online",
// которое трактуется как running — см. статус в тесте).
func (r Resource) Running() bool {
	return r.Status == "running" || r.Status == "online"
}

// envelope — общая обёртка PVE {"data": …} (текст ошибки при !=200 в неё НЕ
// попадает — читается из тела ответа в rawOnce).
type envelope struct {
	Data json.RawMessage `json:"data"`
}

// doJSON — запрос к /api2/json<path> с разворотом {"data": …} в out.
func (c *Client) doJSON(ctx context.Context, method, path string, form url.Values, out any) error {
	var env envelope
	if err := c.raw(ctx, method, "/api2/json"+path, form, &env, true); err != nil {
		return err
	}
	if len(env.Data) == 0 {
		return nil // пустой data — валидный ответ мутаций
	}
	return json.Unmarshal(env.Data, out)
}

// raw — один HTTP-ход; при 401 на авторизованном запросе (протухший тикет —
// тикеты PVE живут ограниченное время) делает единственный тихий релогин
// сохранёнными учётными данными и один повтор. Если релогин не удался —
// наружу уходит исходная ошибка (виджет покажет «сессия истекла»).
func (c *Client) raw(ctx context.Context, method, path string, form url.Values, out *envelope, authed bool) error {
	err := c.rawOnce(ctx, method, path, form, out, authed)
	if ae, ok := err.(*Error); ok && authed && ae.Status == http.StatusUnauthorized {
		if lerr := c.LoginCtx(ctx, c.user, c.pass); lerr == nil {
			return c.rawOnce(ctx, method, path, form, out, authed)
		} else {
			// Ревью 2 (улучшение 3): исходная 401 + причина провала релогина —
			// обе ошибки, иначе «unauthorized» без деталей (пароль сменили?).
			return errors.Join(err, fmt.Errorf("релогин: %w", lerr))
		}
	}
	return err
}

// Close — освободить idle-соединения транспорта. После Close клиент не
// использовать. Вызывается при закрытии вкладки (pveShutdown) и в конце
// короткоживущих клиентов (trackTask) — ревью 3 (Minor: transport-накопление).
func (c *Client) Close() {
	if c == nil || c.http == nil {
		return // фейки тестов отдают nil-клиент — гасить нечего
	}
	c.http.CloseIdleConnections()
}

// rawOnce — одиночный HTTP-запрос без релогина; форма data при ошибках
// не парсится — только {"errors": …}.
func (c *Client) rawOnce(ctx context.Context, method, path string, form url.Values, out *envelope, authed bool) error {
	var body io.Reader
	if form != nil && method != http.MethodGet {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if form != nil && method != http.MethodGet {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if authed && c.ticket != "" {
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: c.ticket})
		if method != http.MethodGet {
			req.Header.Set("CSRFPreventionToken", c.csrf)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24)) // 16 МиБ — ответы кластера много меньше
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized && authed {
		// сигнал наверх; релогин выполнит обёртка raw
		return &Error{Status: resp.StatusCode, Message: "unauthorized"}
	}
	if resp.StatusCode >= 300 {
		msg := http.StatusText(resp.StatusCode)
		var he struct {
			Errors any `json:"errors"`
		}
		if json.Unmarshal(data, &he) == nil && he.Errors != nil {
			msg = fmt.Sprintf("%v", he.Errors)
		}
		return &Error{Status: resp.StatusCode, Message: msg}
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
