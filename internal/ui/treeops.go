package ui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

// newUUID — 128-битный случайный UUID (секция environments{key}).
func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("manual-%d", len(b))
	}
	return hex.EncodeToString(b[:8]) + "-" + hex.EncodeToString(b[8:10]) + "-" + hex.EncodeToString(b[10:12]) +
		"-" + hex.EncodeToString(b[12:14]) + "-" + hex.EncodeToString(b[14:])
}

// ensureRoot — при пустом конфиге синтезируется корень-группа (ревью F5).
func ensureRoot(cfg *config.Config) {
	if _, ok := cfg.Environments[config.RootUUID]; !ok {
		cfg.Environments[config.RootUUID] = config.Environment{
			Name:    "root",
			IsGroup: 1,
		}
	}
}

// addEnv — добавление записи: привязка к parent через children{} и Parent;
// битый parent (или "") → корень.
func addEnv(cfg *config.Config, parent string, env *config.Environment) (string, error) {
	ensureRoot(cfg)
	if _, ok := cfg.Environments[parent]; !ok {
		parent = config.RootUUID
	}
	env.Parent = parent
	uuid := newUUID()
	cfg.Environments[uuid] = *env

	p := cfg.Environments[parent]
	if p.Children == nil {
		p.Children = map[string]config.FlexInt{}
	}
	p.Children[uuid] = 1
	cfg.Environments[parent] = p
	return uuid, nil
}

// removeEnv — удаление записи вместе с поддеревом (легаси _delNodes,
// PACMain.pm:4005) и снятие ссылок из children{} всех родителей.
// Циклические children{} не бросают рекурсию в стек (visived-set, ревью R2).
func removeEnv(cfg *config.Config, uuid string) error {
	return removeEnvVisited(cfg, uuid, map[string]bool{})
}

func removeEnvVisited(cfg *config.Config, uuid string, seen map[string]bool) error {
	if uuid == config.RootUUID {
		return fmt.Errorf("корневой узел удалять нельзя")
	}
	if seen[uuid] {
		return nil
	}
	seen[uuid] = true
	if _, ok := cfg.Environments[uuid]; !ok {
		return nil
	}
	// M36-ревью MAJOR: не удаляем ни один узел поддерева, если В ПОДДЕРЕВЕ есть
	// защищённый — иначе незащищённые соседи удалялись бы, а родитель оставался
	// (частичное удаление = потеря данных). Легаси PACMain.pm:1502 _hasProtectedChildren.
	if subtreeHasProtected(cfg, uuid, map[string]bool{}) {
		return fmt.Errorf("в поддереве есть защищённые узлы: удаление запрещено")
	}
	for child := range cfg.Environments[uuid].Children {
		if err := removeEnvVisited(cfg, child, seen); err != nil {
			return err
		}
	}
	delete(cfg.Environments, uuid)
	for _, p := range cfg.Environments {
		delete(p.Children, uuid) // map-ссылка мутирует живой объект
	}
	return nil
}

// subtreeHasProtected — есть ли в поддереве (включая сам узел) защищённый.
func subtreeHasProtected(cfg *config.Config, uuid string, seen map[string]bool) bool {
	if seen[uuid] {
		return false
	}
	seen[uuid] = true
	e, ok := cfg.Environments[uuid]
	if !ok {
		return false
	}
	if e.Protected == 1 {
		return true
	}
	for c := range e.Children {
		if subtreeHasProtected(cfg, c, seen) {
			return true
		}
	}
	return false
}

// clipboard — буфер copy/cut узлов дерева (P0-5 аудита).
var clipboard struct {
	env      *config.Environment
	srcUUID  string // источник (guard «в себя») — H1-ревью
	cut      bool
	cutUUID  string
	children map[string]config.FlexInt // снапшот детей (L2-ревью)
}

// deepCopyEnv — копия Environment без общих карт/слайсов. copyEnv раньше делал
// `cp := env`, поэтому TerminalOptions/Raw/Variables/… копии и оригинала были
// ОДНИМИ объектами: правка личных настроек терминала у клона/вставки мутировала
// исходное соединение. Дети копируются отдельно (newUUID при вставке).
func deepCopyEnv(env config.Environment) config.Environment {
	cp := env
	cp.Children = cloneFlexMap(env.Children)
	cp.TerminalOptions = deepCopyMap(env.TerminalOptions)
	cp.Raw = deepCopyMap(env.Raw)
	cp.Variables = cloneMapSlice(env.Variables)
	cp.Screenshots = deepCopySlice(env.Screenshots)
	cp.LocalBefore = cloneMapSlice(env.LocalBefore)
	cp.Expect = cloneMapSlice(env.Expect)
	cp.LocalConnected = cloneMapSlice(env.LocalConnected)
	cp.Macros = cloneMapSlice(env.Macros)
	cp.LocalAfter = cloneMapSlice(env.LocalAfter)
	// ag-6nb: слайс из YAML часто имеет cap > len (append-рост), поэтому append
	// в копию писал в общий backing-массив оригинала и затирал соседний слот.
	cp.Cluster = append(config.ClusterNames(nil), env.Cluster...)
	return cp
}

// deepCopyMap — рекурсивная копия map[string]any (вложенные map/slice).
func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyAny(v)
	}
	return out
}

func deepCopyAny(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		return deepCopySlice(t)
	case []map[string]any:
		return cloneMapSlice(t)
	default:
		return v
	}
}

func deepCopySlice(in []any) []any {
	if in == nil {
		return nil
	}
	out := make([]any, len(in))
	for i := range in {
		out[i] = deepCopyAny(in[i])
	}
	return out
}

func cloneMapSlice(in []map[string]any) []map[string]any {
	if in == nil {
		return nil
	}
	out := make([]map[string]any, len(in))
	for i, m := range in {
		out[i] = deepCopyMap(m)
	}
	return out
}

func cloneFlexMap(in map[string]config.FlexInt) map[string]config.FlexInt {
	if in == nil {
		return nil
	}
	out := make(map[string]config.FlexInt, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// copyEnv — копия узла в буфер (вместе с поддеревом; дети генерируются
// при вставке, чтобы не делить UUID).
func copyEnv(cfg *config.Config, uuid string) error {
	env, ok := cfg.Environments[uuid]
	if !ok {
		return fmt.Errorf("узел не найден: %s", uuid)
	}
	cp := deepCopyEnv(env)
	cp.Parent = "" // позиция определяется при вставке
	cp.Children = nil
	clipboard.env = &cp
	clipboard.srcUUID = uuid
	if len(env.Children) > 0 { // снапшот: карта оригинала не живая ссылка (L2)
		clipboard.children = map[string]config.FlexInt{}
		for k, v := range env.Children {
			clipboard.children[k] = v
		}
	} else {
		clipboard.children = nil
	}
	clipboard.cut = false
	return nil
}

// cutEnv — то же, но с пометкой вырезки: исходный узел удаляется после вставки.
func cutEnv(cfg *config.Config, uuid string) error {
	if err := copyEnv(cfg, uuid); err != nil {
		return err
	}
	clipboard.cut = true
	clipboard.cutUUID = uuid
	return nil
}

// pasteEnv — вставка буфера под parent (новые UUID всего поддерева).
func pasteEnv(cfg *config.Config, parent string) (string, error) {
	if clipboard.env == nil {
		return "", fmt.Errorf("буфер пуст")
	}
	if _, ok := cfg.Environments[parent]; !ok {
		parent = config.RootUUID
	}
	// H1-ревью: вставка в самого себя или потомка — бесконечная рекурсия
	// pasteSubtree (мутирование итерируемой карты children) → stack overflow.
	if err := rejectSelfDescendant(cfg, clipboard.srcUUID, parent); err != nil {
		return "", err
	}
	uuid := newUUID()
	cp := deepCopyEnv(*clipboard.env)
	cp.Parent = parent
	cp.Children = nil
	cfg.Environments[uuid] = cp
	addChild(cfg, parent, uuid)
	if cp.IsGroup == 1 && len(clipboard.children) > 0 {
		pasteSubtree(cfg, uuid, clipboard.children)
	}
	if clipboard.cut && clipboard.cutUUID != uuid {
		if err := removeEnv(cfg, clipboard.cutUUID); err != nil {
			return uuid, err
		}
		clipboard.env = nil // вырезка одноразовая
	}
	return uuid, nil
}

// rejectSelfDescendant — parent в поддереве источника? Обход по children{}
// (H2-ревью: Parent в старых/ручных конфигах может отсутствовать — куча
// там рекурсия-пинг-понг не сходилась к stack overflow; с Parent-цепочкой —
// тот же результат, но дети задаются children{}).
func rejectSelfDescendant(cfg *config.Config, srcUUID, parent string) error {
	if srcUUID == "" {
		return nil
	}
	if parent == srcUUID {
		return fmt.Errorf("нельзя вставить узел в себя")
	}
	stack := []string{srcUUID}
	seen := map[string]bool{srcUUID: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for child := range cfg.Environments[cur].Children {
			if child == parent {
				return fmt.Errorf("нельзя вставить узел в его же потомка")
			}
			if !seen[child] {
				seen[child] = true
				stack = append(stack, child)
			}
		}
	}
	return nil
}

// pasteSubtree — рекурсивная перегенерация поддерева: снапшот ключей +
// ОБЩИЙ visited через всю рекурсию (H2-ревью: Parent-less конфиг обходил
// guard; кросс-уровневый пинг-понг копий иначе не сходился к фейталу).
func pasteSubtree(cfg *config.Config, parentUUID string, children map[string]config.FlexInt) {
	pasteSubtreeVisited(cfg, parentUUID, children, map[string]bool{})
}

func pasteSubtreeVisited(cfg *config.Config, parentUUID string, children map[string]config.FlexInt, visited map[string]bool) {
	keys := make([]string, 0, len(children))
	for k := range children {
		keys = append(keys, k)
	}
	for _, src := range keys {
		if visited[src] {
			continue
		}
		visited[src] = true
		srcEnv, ok := cfg.Environments[src]
		if !ok {
			continue
		}
		nu := newUUID()
		ce := deepCopyEnv(srcEnv)
		ce.Parent = parentUUID
		ce.Children = nil
		cfg.Environments[nu] = ce
		addChild(cfg, parentUUID, nu)
		if srcEnv.IsGroup == 1 && len(srcEnv.Children) > 0 {
			// снапшот уровня: srcEnv.Children — ЖИВАЯ карта (в неё могла
			// попасть свежая копия при paste-под-потомка; L2-ревью)
			sub := map[string]config.FlexInt{}
			for k, v := range srcEnv.Children {
				sub[k] = v
			}
			pasteSubtreeVisited(cfg, nu, sub, visited)
		}
	}
}

// cloneEnv — копия под тем же родителем (deep), без следа в буфере.
func cloneEnv(cfg *config.Config, uuid string) (string, error) {
	env, ok := cfg.Environments[uuid]
	if !ok {
		return "", fmt.Errorf("узел не найден: %s", uuid)
	}
	if uuid == config.RootUUID {
		return "", fmt.Errorf("корневой узел клонировать нельзя")
	}
	// M36 Protect: нельзя клонировать ПОД защищённым родителем (легаси 2954).
	if cfg.Environments[env.Parent].Protected == 1 {
		return "", fmt.Errorf("родитель защищён: клонирование под ним запрещено")
	}
	// узел мелькает в буфере — подмена временно, потом чистится
	if err := copyEnv(cfg, uuid); err != nil {
		return "", err
	}
	nu, err := pasteEnv(cfg, env.Parent)
	clipboard.env = nil // clone: буфер не наследуется дальше (H1)
	clipboard.srcUUID = ""
	clipboard.cut = false
	clipboard.cutUUID = ""
	clipboard.children = nil
	return nu, err
}

// addChild — ссылка children{} родителя на дочерний uuid.
func addChild(cfg *config.Config, parent, uuid string) {
	if _, ok := cfg.Environments[parent]; !ok {
		parent = config.RootUUID
	}
	p := cfg.Environments[parent]
	if p.Children == nil {
		p.Children = map[string]config.FlexInt{}
	}
	p.Children[uuid] = 1
	cfg.Environments[parent] = p
}

// reconcileOrder — переписывание Parent/children{} по порядку из UI
// (M15: после внутреннего DnD канонический конфиг синхронизируется с
// расположением элементов). order — post-order (дети раньше родителей);
// каждая строка несёт свой список children, порядок прохода не важен.
func reconcileOrder(cfg *config.Config, order []reconcileNode) {
	known := map[string]bool{config.RootUUID: true}
	for u := range cfg.Environments {
		known[u] = true
	}
	// сначала сброс всей иерархии
	for u := range cfg.Environments {
		e := cfg.Environments[u]
		e.Parent = ""
		e.Children = nil
		cfg.Environments[u] = e
	}
	for _, n := range order {
		parent := n.UUID
		if !known[parent] {
			continue
		}
		for _, child := range n.Children {
			if !known[child] || child == config.RootUUID || child == parent {
				continue
			}
			ce := cfg.Environments[child]
			ce.Parent = parent
			cfg.Environments[child] = ce
			addChild(cfg, parent, child)
		}
	}
	// defense (M15-ревью H1): узел, оставшийся без родителя (верхний
	// уровень не попал в root-строку или неизвестная строка) — к корню,
	// а не в сироты (порча уходила на диск при отсутствии root-эмиссии)
	for u := range cfg.Environments {
		e := cfg.Environments[u]
		if u != config.RootUUID && e.Parent == "" {
			e.Parent = config.RootUUID
			cfg.Environments[u] = e
			addChild(cfg, config.RootUUID, u)
		}
	}
}

// reconcileNode — строка порядка.
type reconcileNode struct {
	UUID     string
	Children []string
}
