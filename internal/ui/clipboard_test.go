package ui

import (
	"testing"

	"github.com/DunkanAidaho/asbru-go/internal/config"
)

func fixture(cfg *config.Config) {
	cfg.Environments["g1"] = config.Environment{Name: "Группа", IsGroup: 1, Parent: config.RootUUID,
		Children: map[string]config.FlexInt{"c1": 1}}
	cfg.Environments["c1"] = config.Environment{Name: "Сервер", Parent: "g1", Method: "SSH", IP: "10.0.0.9"}
	cfg.Environments[config.RootUUID] = config.Environment{Name: "root", IsGroup: 1,
		Children: map[string]config.FlexInt{"g1": 1}}
}

// M14: clone — глубокая копия поддерева, UUID не дублируются.
func TestCloneEnvDeep(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	fixture(cfg)
	nu, err := cloneEnv(cfg, "g1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environments[nu].Name != "Группа" {
		t.Fatalf("копия: %+v", cfg.Environments[nu])
	}
	count := 0
	for u, e := range cfg.Environments {
		if e.Name == "Сервер" {
			count++
			if u == "c1" && cfg.Environments[u].Parent == "g1" {
				continue
			}
		}
	}
	if count != 2 {
		t.Fatalf("дети не продублированы: %d", count)
	}
	if clipboard.env != nil {
		t.Fatal("буфер не очищен после clone")
	}
	if _, ok := cfg.Environments["c1"]; !ok {
		t.Fatal("оригинал потерян")
	}
}

// M14: cut→paste — узел перемещается (исходный удаляется), дети новые.
func TestCutPasteMoves(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	fixture(cfg)
	if err := cutEnv(cfg, "g1"); err != nil {
		t.Fatal(err)
	}
	nu, err := pasteEnv(cfg, config.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Environments["g1"]; ok {
		t.Fatal("исходник не удалён при cut")
	}
	ser, ok := cfg.Environments[nu].Children[findServer(cfg, nu)]
	if !ok {
		t.Fatal("у перемещённой копии нет сервера")
	}
	_ = ser
}

func findServer(cfg *config.Config, groupUUID string) string {
	for u, e := range cfg.Environments {
		if e.Parent == groupUUID && e.Name == "Сервер" {
			return u
		}
	}
	return ""
}

// M14: paste без буфера — ошибка, конфиг не тронут.
func TestPasteEmptyBuffer(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	clipboard.env = nil
	if _, err := pasteEnv(cfg, config.RootUUID); err == nil {
		t.Fatal("пустой буфер не отклонил вставку")
	}
}

// M14-ревью H1: paste в себя/потомка отклоняется (иначе stack overflow).
func TestPasteIntoOwnDescendantRejected(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	fixture(cfg)
	if err := copyEnv(cfg, "g1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pasteEnv(cfg, "c1"); err == nil {
		t.Fatal("paste в потомка источника не отклонён")
	}
	if _, err := pasteEnv(cfg, "g1"); err == nil {
		t.Fatal("paste в себя не отклонён")
	}
	if len(cfg.Environments) != 3 {
		t.Fatalf("конфиг изменён: %d записей", len(cfg.Environments))
	}
	if clipboard.env == nil {
		t.Fatal("буфер очищен раньше времени")
	}
}

// M14-ревью: одноразовая вырезка после вставки.
func TestCutPasteOneShot(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	fixture(cfg)
	if err := cutEnv(cfg, "c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pasteEnv(cfg, config.RootUUID); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.Environments["c1"]; ok {
		t.Fatal("cut-исходник остался")
	}
	if clipboard.env != nil {
		t.Fatal("буфер не очищен после одноразовой вырезки")
	}
}

// M14-ревью H2: Parent-less конфиг (children{} без Parent) — вставка
// не уходит в бесконечную рекурсию (общий visited через рекурсию).
func TestPasteIntoDescendantWithoutParent(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		config.RootUUID: {Name: "root", IsGroup: 1, Children: map[string]config.FlexInt{"g1": 1}},
		"g1":            {Name: "Г1", IsGroup: 1, Children: map[string]config.FlexInt{"s1": 1}},
		"s1":            {Name: "С1", IsGroup: 1, Children: map[string]config.FlexInt{"c1": 1}},
		"c1":            {Name: "Внук"},
	}}
	if err := copyEnv(cfg, "g1"); err != nil {
		t.Fatal(err)
	}
	// Parent у узлов пустой — guard по children{} всё равно отклоняет
	// (иначе кросс-уровневый пинг-понг копий — stack overflow)
	if _, err := pasteEnv(cfg, "s1"); err == nil {
		t.Fatal("вставка под потомка без Parent не отклонена")
	}
	if len(cfg.Environments) != 4 {
		t.Fatalf("конфиг изменён: %d записей", len(cfg.Environments))
	}
}

// Копия/вставка/клон не должны делить карты с оригиналом: правка личных
// настроек терминала (или Raw/таблиц) у копии мутировала исходное соединение.
func TestCopyPasteEnvDeepCopiesMaps(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		config.RootUUID: {Name: "root", IsGroup: 1},
		"c1": {
			Name: "Сервер", Method: "SSH", Parent: config.RootUUID,
			TerminalOptions: map[string]any{
				"terminal font": "Mono",
				"nested":        map[string]any{"k": "v"},
			},
			Raw:       map[string]any{"custom": map[string]any{"deep": 1}},
			Variables: []map[string]any{{"name": "v1"}},
			Macros:    []map[string]any{{"txt": "cmd"}},
			Cluster:   config.ClusterNames{"x", "y"},
		},
	}}
	clipboard.env = nil
	defer func() { clipboard.env = nil; clipboard.children = nil; clipboard.srcUUID = "" }()
	if err := copyEnv(cfg, "c1"); err != nil {
		t.Fatal(err)
	}
	// Мутируем буфер — оригинал не должен измениться.
	clipboard.env.TerminalOptions["terminal font"] = "Changed"
	clipboard.env.TerminalOptions["nested"].(map[string]any)["k"] = "changed"
	clipboard.env.Raw["custom"].(map[string]any)["deep"] = 2
	clipboard.env.Variables[0]["name"] = "changed"
	clipboard.env.Macros[0]["txt"] = "changed"
	clipboard.env.Cluster[0] = "changed"
	orig := cfg.Environments["c1"]
	if orig.TerminalOptions["terminal font"] != "Mono" ||
		orig.TerminalOptions["nested"].(map[string]any)["k"] != "v" ||
		orig.Raw["custom"].(map[string]any)["deep"] != 1 ||
		orig.Variables[0]["name"] != "v1" || orig.Macros[0]["txt"] != "cmd" ||
		orig.Cluster[0] != "x" {
		t.Fatalf("буфер делит карты с оригиналом: %+v", orig)
	}

	nu, err := pasteEnv(cfg, config.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	// Мутируем вставленный узел — ни буфер, ни оригинал не меняются.
	pasted := cfg.Environments[nu]
	pasted.TerminalOptions["terminal font"] = "Pasted"
	pasted.TerminalOptions["nested"].(map[string]any)["k"] = "pasted"
	pasted.Raw["custom"].(map[string]any)["deep"] = 3
	pasted.Variables[0]["name"] = "pasted"
	pasted.Cluster[0] = "pasted"
	cfg.Environments[nu] = pasted
	if cfg.Environments["c1"].TerminalOptions["terminal font"] != "Mono" ||
		cfg.Environments["c1"].TerminalOptions["nested"].(map[string]any)["k"] != "v" ||
		cfg.Environments["c1"].Raw["custom"].(map[string]any)["deep"] != 1 ||
		cfg.Environments["c1"].Variables[0]["name"] != "v1" ||
		cfg.Environments["c1"].Cluster[0] != "x" {
		t.Fatal("вставка делит карты с оригиналом")
	}
	if clipboard.env.TerminalOptions["terminal font"] != "Changed" {
		t.Fatal("вставка делит карты с буфером")
	}
}

// ag-6nb: Cluster тоже слайс — у списка из YAML бывает cap > len, и без
// собственного backing-массива append в копию/оригинал затирает друг друга.
func TestCopyPasteEnvDeepCopiesCluster(t *testing.T) {
	orig := make(config.ClusterNames, 3, 4) // как append из YAML: len 3, cap 4
	copy(orig, []string{"a", "b", "c"})
	cfg := &config.Config{Environments: map[string]config.Environment{
		config.RootUUID: {Name: "root", IsGroup: 1},
		"c1":            {Name: "Сервер", Method: "SSH", Parent: config.RootUUID, Cluster: orig},
	}}
	clipboard.env = nil
	defer func() { clipboard.env = nil; clipboard.children = nil; clipboard.srcUUID = "" }()
	if err := copyEnv(cfg, "c1"); err != nil {
		t.Fatal(err)
	}
	// append в буфер не должен попадать в backing-массив оригинала.
	clipboard.env.Cluster = append(clipboard.env.Cluster, "D")
	env := cfg.Environments["c1"]
	env.Cluster = append(env.Cluster, "E")
	cfg.Environments["c1"] = env
	if got := clipboard.env.Cluster; len(got) != 4 || got[3] != "D" {
		t.Fatalf("copy cluster list corrupted: %v (want [a b c D])", got)
	}

	nu, err := pasteEnv(cfg, config.RootUUID)
	if err != nil {
		t.Fatal(err)
	}
	pasted := cfg.Environments[nu]
	pasted.Cluster = append(pasted.Cluster, "F")
	cfg.Environments[nu] = pasted
	origEnv := cfg.Environments["c1"]
	origEnv.Cluster = append(origEnv.Cluster, "G")
	cfg.Environments["c1"] = origEnv
	if got := cfg.Environments[nu].Cluster; len(got) != 5 || got[3] != "D" || got[4] != "F" {
		t.Fatalf("paste cluster list corrupted: %v (want [a b c D F])", got)
	}
}

// cloneEnv идёт через copy/paste — карты клона тоже не должны быть общими.
func TestCloneEnvDeepCopiesMaps(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{
		config.RootUUID: {Name: "root", IsGroup: 1},
		"c1": {
			Name: "Сервер", Method: "SSH", Parent: config.RootUUID,
			TerminalOptions: map[string]any{"terminal font": "Mono"},
		},
	}}
	clipboard.env = nil
	defer func() { clipboard.env = nil; clipboard.children = nil; clipboard.srcUUID = "" }()
	nu, err := cloneEnv(cfg, "c1")
	if err != nil {
		t.Fatal(err)
	}
	clone := cfg.Environments[nu]
	clone.TerminalOptions["terminal font"] = "Changed"
	cfg.Environments[nu] = clone
	if cfg.Environments["c1"].TerminalOptions["terminal font"] != "Mono" {
		t.Fatal("клон делит terminal options с оригиналом")
	}
}

// M15: reconcileOrder — после DnD иерархия конфига переписывается по
// порядку UI (Parent/children чистые; отсутствующие в дереве узлы
// отцепляются; себе-в-потомки и корень игнорируются).
func TestReconcileOrder(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	fixture(cfg) // g1(Сервер) под root
	cfg.Environments["g2"] = config.Environment{Name: "Г2", IsGroup: 1, Parent: config.RootUUID}

	// новый порядок: Сервер → верхний уровень, Г2 → содержит г1
	// порядок, как его производит orderFromTree (post-order, root-строка)
	order := []reconcileNode{
		{UUID: config.RootUUID, Children: []string{"c1", "g2"}},
		{UUID: "c1"},
		{UUID: "g2", Children: []string{"g1"}},
		{UUID: "g1", Children: []string{"g1c2"}},
		{UUID: "g1c2"},
	}
	cfg.Environments["g1c2"] = config.Environment{Name: "Внук"}
	reconcileOrder(cfg, order)
	if cfg.Environments["c1"].Parent != config.RootUUID {
		t.Fatalf("c1: %q", cfg.Environments["c1"].Parent)
	}
	if cfg.Environments["g1"].Parent != "g2" {
		t.Fatalf("g1: %q", cfg.Environments["g1"].Parent)
	}
	if cfg.Environments["g2"].Children["g1"] != 1 {
		t.Fatalf("g2 children: %v", cfg.Environments["g2"].Children)
	}
	if _, ok := cfg.Environments["c1"].Children["g1c2"]; ok {
		t.Fatal("children у листа появились (порча)")
	}
	// исходный корень был переписан: его children теперь по порядку
	if cfg.Environments[config.RootUUID].Children["g1"] == 1 {
		t.Fatal("g1 осталась в корне")
	}
}

// M15-ревью H1: БЕЗ root-строки (кривой продюсер/игнорированный дроп)
// верхний уровень всё равно не становится сиротами — привязка к корню.
func TestReconcileOrphansToRoot(t *testing.T) {
	cfg := &config.Config{Environments: map[string]config.Environment{}}
	fixture(cfg)
	order := []reconcileNode{
		{UUID: "c1"},
		{UUID: "g1", Children: []string{"c1"}},
	}
	reconcileOrder(cfg, order)
	if cfg.Environments[config.RootUUID].Children["g1"] != 1 {
		t.Fatalf("g1 не на корне: %v", cfg.Environments[config.RootUUID].Children)
	}
	if cfg.Environments["c1"].Parent == "" {
		t.Fatalf("c1 сирота: %q", cfg.Environments["c1"].Parent)
	}
}
