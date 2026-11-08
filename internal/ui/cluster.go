package ui

import (
	"sort"
	"sync"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// M20: кластеры и PCC (Power Cluster Controller) — ядро без Qt.
// Легаси: environments{uuid}{cluster} = [имена кластеров] (PACUtils.pm:2393);
// кластер у терминала задаётся при запуске (PACMain.pm:2692-2707,
// «Execute in New Cluster...»); PCC шлёт ввод во все терминалы кластера.

// clusterIDs — uuid соединений (не групп) кластера по имени; детерминированный
// порядок (легаси-порядок — произвольный, берём сортировку).
// ЗАДЕЛ: сейчас используется тестами; PCC-список строится по живым
// терминалам (легаси, PACMain.pm:2722-2728 RE-RUNNING), состав из конфига —
// для будущего диалога управления кластерами (PACCluster.pm).
func clusterIDs(cfg *config.Config, name string) []string {
	var out []string
	for uuid, e := range cfg.Environments {
		if e.IsGroup != 1 && clusterHas(e.Cluster, name) {
			out = append(out, uuid)
		}
	}
	sort.Strings(out)
	return out
}

// firstCluster — имя кластера терминала (первое в списке; легаси
// _CLUSTER у терминала — одно имя, PACMain.pm:2708).
func firstCluster(c config.ClusterNames) string {
	if len(c) > 0 {
		return c[0]
	}
	return ""
}

func clusterHas(c config.ClusterNames, name string) bool {
	for _, n := range c {
		if n == name {
			return true
		}
	}
	return false
}

// fanIn — неблокирующий разбор сообщения по каналам терминалов: полный
// буфер дропается, остальные получают (PCC не вешается на троттлинг).
func fanIn(chs []chan<- []byte, b []byte) (dropped int) {
	for _, ch := range chs {
		select {
		case ch <- b:
		default:
			dropped++
		}
	}
	return dropped
}

// clusterRegistry — живые терминалы по кластерам (лайфтайм запущенного
// терминала = сессии; registr-доступ из Qt-нити и трея — под мьютексом).
type clusterRegistry struct {
	mu        sync.Mutex
	byCluster map[string][]chan<- []byte
}

func (r *clusterRegistry) add(cluster string, in chan<- []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byCluster[cluster] = append(r.byCluster[cluster], in)
}

func (r *clusterRegistry) remove(cluster string, in chan<- []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	chans := r.byCluster[cluster]
	for i, ch := range chans {
		if ch == in {
			r.byCluster[cluster] = append(chans[:i], chans[i+1:]...)
			break
		}
	}
	if len(r.byCluster[cluster]) == 0 {
		delete(r.byCluster, cluster)
	}
}

// clusters — отсортированные имена кластеров живых терминалов.
func (r *clusterRegistry) clusters() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.byCluster))
	for name := range r.byCluster {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// count — сколько живых терминалов в кластере (UX PCC-окна).
func (r *clusterRegistry) count(cluster string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byCluster[cluster])
}

// send — разбор ввода в терминалы кластера (ядро PCC-окна).
func (r *clusterRegistry) send(cluster string, b []byte) (dropped int) {
	r.mu.Lock()
	chans := append([]chan<- []byte(nil), r.byCluster[cluster]...)
	r.mu.Unlock()
	return fanIn(chans, b)
}
