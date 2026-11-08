package ui

// Чистая логика будущего PVE-виджета (без Qt): модель строк дерева,
// правила контекстного меню, счётчик подтверждений, кэш IP.
// Источник данных — pve.Resource (GET /cluster/resources, Tasks 2–5);
// виджет (pvewidget.go) только рисует эти строки и зовёт эти правила.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/DunkanAidaho/asbru-go/internal/i18n"
	"github.com/DunkanAidaho/asbru-go/internal/pve"
)

// PveRow — одна строка дерева PVE. Kind: "node" | "qemu" | "lxc" |
// "storage" | "storagehdr" (заголовок секции «Сторажи»). Key — стабильный
// («node:pve1», «qemu:101», «storage:pve1:local», «storagehdr»), по нему
// виджет восстанавливает раскрытость/выбор между обновлениями; ParentKey
// пуст у корней (ноды и заголовок сторов). Статус — сырая строка PVE
// (running/stopped/online/available), здесь НЕ переводится. IPs заполняет
// виджет из кэша (PveMergeIPs), модель всегда отдаёт пусто.
type PveRow struct {
	Key       string
	ParentKey string
	Kind      string
	VMID      int
	Name      string
	Status    string
	Tags      string
	IPs       string
}

// storagehdrKey — Key строки-заголовка секции «Сторажи».
const storagehdrKey = "storagehdr"

// PveTreeRows — плоские строки дерева в порядке отрисовки: секция «Ноды»
// (ноды по имени, внутри каждой — её гости по VMID↑), затем секция
// «Сторажи» (заголовок + сторы по ноде, потом по имени). Секция «Сторажи»
// рисуется только когда сторы есть. Чистая функция: вход не меняется.
func PveTreeRows(rs []pve.Resource) []PveRow {
	nodes := pve.Nodes(rs)
	slices.SortFunc(nodes, func(a, b pve.Resource) int {
		return strings.Compare(a.Node, b.Node)
	})

	// гости группируются по ноде и сортируются по (VMID, имя)
	guests := pve.Guests(rs)
	slices.SortFunc(guests, func(a, b pve.Resource) int {
		if c := strings.Compare(a.Node, b.Node); c != 0 {
			return c
		}
		if a.VMID != b.VMID {
			return a.VMID - b.VMID
		}
		return strings.Compare(a.Name, b.Name)
	})

	stors := pve.Storages(rs)
	slices.SortFunc(stors, func(a, b pve.Resource) int {
		if c := strings.Compare(a.Node, b.Node); c != 0 {
			return c
		}
		return strings.Compare(pveStorageName(a), pveStorageName(b))
	})

	rows := make([]PveRow, 0, len(nodes)+len(guests)+len(stors)+1)
	for _, n := range nodes {
		rows = append(rows, PveRow{
			Key:    "node:" + n.Node,
			Kind:   "node",
			Name:   n.Name,
			Status: n.Status,
			Tags:   n.Tags,
		})
		for _, g := range guests {
			if g.Node != n.Node {
				continue
			}
			rows = append(rows, PveRow{
				// Ревью 3 (Minor): vmid может совпасть на разных нодах
				// (импорт конфигов) — ключ включает ноду.
				Key:       fmt.Sprintf("%s:%s:%d", g.NodeType, g.Node, g.VMID),
				ParentKey: "node:" + n.Node,
				Kind:      g.NodeType,
				VMID:      g.VMID,
				Name:      g.Name,
				Status:    g.Status,
				Tags:      g.Tags,
			})
		}
	}
	if len(stors) > 0 {
		rows = append(rows, PveRow{
			Key:  storagehdrKey,
			Kind: "storagehdr",
			Name: i18n.Tr("Tree", "Сторажи"),
		})
		for _, s := range stors {
			rows = append(rows, PveRow{
				Key:       pveStorageKey(s),
				ParentKey: storagehdrKey,
				Kind:      "storage",
				Name:      pveStorageName(s),
				Status:    s.Status,
				Tags:      s.Tags,
			})
		}
	}
	return rows
}

// pveStorageName — имя стора: последний сегмент ID («storage/pve1/local» →
// «local»; у сторов Resource.Name — fallback на имя ноды, для показа негоден).
// Неразбираемый ID (не бывает у живого PVE) → Name как есть.
func pveStorageName(r pve.Resource) string {
	if rest, ok := strings.CutPrefix(r.ID, "storage/"); ok {
		if _, stor, found := strings.Cut(rest, "/"); found && stor != "" {
			return stor
		}
	}
	return r.Name
}

// pveStorageKey — стабильный ключ строки стора («storage:pve1:local»).
func pveStorageKey(r pve.Resource) string {
	return "storage:" + r.Node + ":" + pveStorageName(r)
}

// Действия контекстного меню (PveMenuAllowed) и подтверждений
// (PveConfirmCount). Гостевые — console/start/stop/reboot/shutdown/
// snapshots/config (как в спеке виджета); снапшотные — snapCreate/
// snapRollback/snapDelete (вызываются из диалога «Снапшоты…»).

// PveMenuAllowed — разрешено ли действие на строке: «Консоль» — работающий
// qemu (lxc-консоль через vncproxy PVE не даёт); start — остановленный гость;
// stop/reboot/shutdown — работающий гость; снапшоты и конфиг — гостю любого
// статуса (PVE умеет и на остановленном). Неизвестное действие — запрещено.
func PveMenuAllowed(row PveRow, action string) bool {
	guest := row.Kind == "qemu" || row.Kind == "lxc"
	switch action {
	case "console":
		// M45-остаток: LXC-консоль через termproxy тоже VNC — доступна так же.
		return guest && row.Status == "running"
	case "start":
		return guest && row.Status == "stopped"
	case "stop", "reboot", "shutdown":
		return guest && row.Status == "running"
	case "snapshots", "config":
		return guest
	case "backup":
		// vzdump работает и на остановленном госте.
		return guest
	}
	return false
}

// PveConfirmCount — сколько подтверждений требовать: 1 — обычное
// (start/reboot/создание снапшота), 2 — опасное (stop/shutdown/откат/
// удаление снапшота — второй текст «Действие необратимо…»), 0 — действие
// без подтверждения или неизвестное.
func PveConfirmCount(action string) int {
	switch action {
	case "start", "reboot", "snapCreate", "backup":
		return 1
	case "stop", "shutdown", "snapRollback", "snapDelete":
		return 2
	}
	return 0
}

// PveMergeIPs — занесение выдачи GuestAgentIPs в кэш IP (map vmid → адреса):
// набор ЗАМЕЩАЕТСЯ (адрес, сменившийся по DHCP, не должен висеть рядом со
// старым — ревью 2); пустой список (гостевой агент не стоит — частый случай)
// кэш НЕ трогает. Карта меняется на месте (владелец — виджет).
func PveMergeIPs(existing map[int][]string, vmid int, ips []string) {
	if existing == nil || len(ips) == 0 {
		return
	}
	// Ревью 2 (Minor): адрес, СМЕНИВШИЙСЯ по DHCP, раньше оставался в кэше
	// навсегда — агент возвращает актуальную картину целиком, замещаем набор.
	existing[vmid] = ips
}

// PveIPsText — адреса кэша для колонки «IP» (join "; ").
func PveIPsText(ips []string) string {
	return strings.Join(ips, "; ")
}
