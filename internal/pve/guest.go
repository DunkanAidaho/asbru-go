// Действия над гостями и задачи (UPID) — /nodes/{node}/{guestType}/{vmid}/…,
// /nodes/{node}/tasks/{upid}/status, guest-agent
// (/nodes/{node}/qemu/{vmid}/agent/…).
package pve

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// GuestAction — POST /nodes/{node}/{guestType}/{vmid}/status/{action}
// (action: start|stop|reboot|shutdown) → UPID запущенной задачи.
func (c *Client) GuestAction(ctx context.Context, node, guestType string, vmid int, action string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/status/%s",
		url.PathEscape(node), url.PathEscape(guestType), vmid, url.PathEscape(action))
	var upid string
	if err := c.doJSON(ctx, http.MethodPost, path, url.Values{}, &upid); err != nil {
		return "", fmt.Errorf("%s %d на %s: %s: %w", guestType, vmid, node, action, err)
	}
	return upid, nil
}

// taskNode — нода из UPID («UPID:<node>:<pid>:…»). /cluster/tasks — только
// list-эндпоинт, статус конкретной задачи живёт на ноде, поэтому без ноды
// запрос не построить; некорректный UPID — ошибка без сетевого вызова.
func taskNode(upid string) (string, error) {
	parts := strings.SplitN(upid, ":", 3)
	if len(parts) < 3 || parts[0] != "UPID" || parts[1] == "" {
		return "", fmt.Errorf("некорректный UPID: %q", upid)
	}
	return parts[1], nil
}

// TaskStatus — GET /nodes/{node}/tasks/{upid}/status: выполняется ли задача
// и чем закончилась (exitstatus == "OK" при успехе). Нода берётся из UPID;
// сам upid URL-экранируется — в нём двоеточия и "@".
func (c *Client) TaskStatus(ctx context.Context, upid string) (running bool, exit string, err error) {
	node, err := taskNode(upid)
	if err != nil {
		return false, "", err
	}
	var st struct {
		Status     string `json:"status"`
		ExitStatus string `json:"exitstatus"`
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &st); err != nil {
		return false, "", fmt.Errorf("статус задачи: %w", err)
	}
	return st.Status == "running", st.ExitStatus, nil
}

// WaitTask — поллинг TaskStatus каждые every до завершения задачи;
// exitstatus != "OK" → ошибка с текстом PVE. Ограничение по времени —
// только контекстом (таймаутов внутри нет).
func (c *Client) WaitTask(ctx context.Context, upid string, every time.Duration) error {
	if every <= 0 {
		every = time.Second
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		running, exit, err := c.TaskStatus(ctx, upid)
		if err != nil {
			return err
		}
		if !running {
			if exit == "" {
				// Ревью 3: data:null (задача завершена без exitstatus) —
				// иначе в сообщении хвост-пустота «задача …: ».
				return fmt.Errorf("задача %s завершилась без статуса", upid)
			}
			if exit != "OK" {
				return fmt.Errorf("задача %s: %s", upid, exit)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// GuestConfig — GET /nodes/{node}/{guestType}/{vmid}/config: сырой конфиг
// гостя (ключи PVE: hostname, memory, net0, …) без разбора в типы.
func (c *Client) GuestConfig(ctx context.Context, node, guestType string, vmid int) (map[string]any, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/config",
		url.PathEscape(node), url.PathEscape(guestType), vmid)
	var cfg map[string]any
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &cfg); err != nil {
		return nil, fmt.Errorf("конфиг %s %d на %s: %w", guestType, vmid, node, err)
	}
	return cfg, nil
}

// Snapshot — запись GET /nodes/{node}/{guestType}/{vmid}/snapshot.
type Snapshot struct {
	Name        string
	Description string
	Time        int64 // unix-время создания (snaptime PVE)
}

// Snapshots — список снапшотов гостя; служебная запись "current"
// («вы здесь») не возвращается.
func (c *Client) Snapshots(ctx context.Context, node, guestType string, vmid int) ([]Snapshot, error) {
	var raws []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SnapTime    int64  `json:"snaptime"`
	}
	path := fmt.Sprintf("/nodes/%s/%s/%d/snapshot",
		url.PathEscape(node), url.PathEscape(guestType), vmid)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &raws); err != nil {
		return nil, fmt.Errorf("снапшоты %s %d на %s: %w", guestType, vmid, node, err)
	}
	out := make([]Snapshot, 0, len(raws))
	for _, r := range raws {
		if r.Name == "current" {
			continue
		}
		out = append(out, Snapshot{Name: r.Name, Description: r.Description, Time: r.SnapTime})
	}
	return out, nil
}

// mutateUPID — мутация (POST/DELETE), в data которой PVE кладёт UPID
// запущенной фоновой задачи.
func (c *Client) mutateUPID(ctx context.Context, method, path string, form url.Values, what string) (string, error) {
	var upid string
	if err := c.doJSON(ctx, method, path, form, &upid); err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	return upid, nil
}

// SnapshotCreate — POST …/snapshot {snapname, description} → UPID.
func (c *Client) SnapshotCreate(ctx context.Context, node, guestType string, vmid int, name, descr string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/snapshot",
		url.PathEscape(node), url.PathEscape(guestType), vmid)
	return c.mutateUPID(ctx, http.MethodPost, path,
		url.Values{"snapname": {name}, "description": {descr}},
		fmt.Sprintf("снапшот %s %s %d на %s", name, guestType, vmid, node))
}

// SnapshotRollback — POST …/snapshot/{name}/rollback → UPID
// (откат — фоновая задача).
func (c *Client) SnapshotRollback(ctx context.Context, node, guestType string, vmid int, name string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/snapshot/%s/rollback",
		url.PathEscape(node), url.PathEscape(guestType), vmid, url.PathEscape(name))
	return c.mutateUPID(ctx, http.MethodPost, path, url.Values{},
		fmt.Sprintf("откат на %s %s %d на %s", name, guestType, vmid, node))
}

// SnapshotDelete — DELETE …/snapshot/{name} → UPID.
func (c *Client) SnapshotDelete(ctx context.Context, node, guestType string, vmid int, name string) (string, error) {
	path := fmt.Sprintf("/nodes/%s/%s/%d/snapshot/%s",
		url.PathEscape(node), url.PathEscape(guestType), vmid, url.PathEscape(name))
	return c.mutateUPID(ctx, http.MethodDelete, path, nil,
		fmt.Sprintf("удаление снапшота %s %s %d на %s", name, guestType, vmid, node))
}

// VNCProxy — параметры VNC-консоли гостя (ответ vncproxy: port, ticket, cert).
type VNCProxy struct {
	Port   int
	Ticket string // одноразовый PVEVNC-тикет для авторизации в VNC
}

// VNCProxy — POST /nodes/{node}/qemu/{vmid}/vncproxy {websocket: "0"}:
// намеренно классический VNC (не noVNC-вебсокет) — порт и тикет для консоли.
func (c *Client) VNCProxy(ctx context.Context, node string, vmid int) (VNCProxy, error) {
	var vp VNCProxy
	path := fmt.Sprintf("/nodes/%s/qemu/%d/vncproxy", url.PathEscape(node), vmid)
	if err := c.doJSON(ctx, http.MethodPost, path, url.Values{"websocket": {"0"}}, &vp); err != nil {
		return VNCProxy{}, fmt.Errorf("vncproxy %d на %s: %w", vmid, node, err)
	}
	return vp, nil
}

// GuestAgentIPs — POST /nodes/{node}/qemu/{vmid}/agent/network-get-interfaces:
// IPv4/IPv6 гостя из qemu-guest-agent, без loopback (127.0.0.0/8, ::1).
// Только «агента нет/не запущен» (PVE отвечает 500/501) → пустой список и nil —
// это не сбой; все прочие ошибки (нода недоступна, сессия истекла, …)
// идут наверх.
func (c *Client) GuestAgentIPs(ctx context.Context, node string, vmid int) ([]string, error) {
	var out struct {
		Result []struct {
			IPAddresses []struct {
				Address string `json:"ip-address"`
				Type    string `json:"ip-address-type"`
			} `json:"ip-addresses"`
		} `json:"result"`
	}
	path := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(node), vmid)
	if err := c.doJSON(ctx, http.MethodPost, path, url.Values{}, &out); err != nil {
		if ae, ok := err.(*Error); ok &&
			(ae.Status == http.StatusInternalServerError || ae.Status == http.StatusNotImplemented) {
			return nil, nil // гостевой агент не установлен/не включён — частый случай, НЕ сбой
		}
		return nil, err
	}
	ips := make([]string, 0)
	for _, ifc := range out.Result {
		for _, a := range ifc.IPAddresses {
			p := net.ParseIP(a.Address)
			if p == nil || p.IsLoopback() {
				continue // мусор/кривой адрес агента или loopback — в колонку не попадает
			}
			ips = append(ips, a.Address)
		}
	}
	return ips, nil
}

// TermProxy — консоль LXC (M45-остаток): POST /nodes/{node}/lxc/{vmid}/
// termproxy → {port, ticket}; PVE оборачивает терминал LXC в VNC-протокол,
// поэтому консоль открывается тем же vncview (ticket = VNC-пароль).
func (c *Client) TermProxy(ctx context.Context, node string, vmid int) (VNCProxy, error) {
	var out VNCProxy
	err := c.doJSON(ctx, http.MethodPost,
		"/nodes/"+url.PathEscape(node)+"/lxc/"+strconv.Itoa(vmid)+"/termproxy",
		url.Values{}, &out)
	return out, err
}

// Backup — vzdump (M45-остаток): POST /nodes/{node}/vzdump, режим
// «snapshot» (PVE-дефолт, живая ВМ без остановки), сжатие zstd → UPID.
func (c *Client) Backup(ctx context.Context, node string, vmid int, storage, mode string) (string, error) {
	form := url.Values{
		"vmid":     {strconv.Itoa(vmid)},
		"storage":  {storage},
		"mode":     {mode},
		"compress": {"zstd"},
	}
	var upid string
	err := c.doJSON(ctx, http.MethodPost,
		"/nodes/"+url.PathEscape(node)+"/vzdump", form, &upid)
	return upid, err
}
