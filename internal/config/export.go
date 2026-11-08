package config

// M22: импорт/экспорт (легаси PACConfig.pm _exporter/_importer).
// Формат — тот же asbru.yml; маркер __PAC__EXPORTED__FULL__ отличает
// полный бэкап (замещение) от частичного (merge).

// WithExportMarker помечает конфиг как полный экспорт (перед Save).
func WithExportMarker(cfg *Config) *Config {
	cfg.ExportedFull = 1
	return cfg
}

// MergeConfig — импорт частичного экспорта: новые uuid добавляются,
// существующие НЕ перетираются (легаси: uuid — ключ, замены нет
// при merge-импорте); дети с отсутствующим родителем пересаживаются
// в корень; глобальные переменные — только новые ключи. Возвращает
// количество добавленных соединений.
//
// ВАЖНО (ревью M22 P1): иерархия дерева в порте строится ТОЛЬКО по
// children{}-картам групп — прокидываем их вторым проходом (порядок
// map-итерации на результат не влияет).
func MergeConfig(dst, src *Config) int {
	// нулевой dst (&Config{}) — легальный вход; без инициализации запись ниже
	// паниковала (assignment to entry in nil map).
	if dst.Environments == nil {
		dst.Environments = map[string]Environment{}
	}
	added := 0
	parents := map[string]string{} // uuid → итоговый родитель
	for uuid, e := range src.Environments {
		if _, exists := dst.Environments[uuid]; exists {
			continue
		}
		if _, ok := src.Environments[e.Parent]; !ok || e.Parent == "" {
			e.Parent = RootUUID // сирота — в корень
		}
		parents[uuid] = e.Parent
		dst.Environments[uuid] = e
		added++
	}
	for uuid, p := range parents {
		g, ok := dst.Environments[p]
		if !ok {
			g = dst.Environments[RootUUID] // родителя нет? → корень
			p = RootUUID
		}
		if g.Children == nil {
			g.Children = map[string]FlexInt{}
		}
		if g.Children[uuid] != 1 {
			g.Children[uuid] = 1
		}
		dst.Environments[p] = g
	}
	if dst.Defaults.GlobalVariables == nil && len(src.Defaults.GlobalVariables) > 0 {
		dst.Defaults.GlobalVariables = map[string]VarEntry{}
	}
	for k, v := range src.Defaults.GlobalVariables {
		if _, exists := dst.Defaults.GlobalVariables[k]; !exists {
			dst.Defaults.GlobalVariables[k] = v
		}
	}
	return added
}

// M36 tree_menu: экспорт ВЫБРАННЫХ узлов — выделенная подмножество окружений
// в отдельный Config, который можно повторно импортировать через MergeConfig.
// ВАЖНО: дети с выбранным родителем сохраняют иерархию; выбранный узел,
// чей родитель НЕ выбран, пересаживается в корень (родитель вне экспорта).

// SubsetConfig — собирает подмножество Environments по sel (uuid), включая
// их потомков (рекурсивно по children{}), в отдельный Config. Возвращает
// новый Config и количество включённых окружений. Иерархия дерева (children{}
// групп) переносится: если предок выбран, дети остаются под ним; иначе —
// сирота в корень (как MergeConfig).
func SubsetConfig(src *Config, sel []string) (*Config, int) {
	// M36-ревью MINOR: Defaults копируются ГЛУБОКО (map-поля иначе алиасят
	// src → правка части экспорта портила бы живой конфиг).
	dst := &Config{Defaults: cloneDefaults(src.Defaults), Environments: map[string]Environment{}}

	include := map[string]bool{} // uuid → включён
	var walk func(string)
	walk = func(u string) {
		if include[u] {
			return
		}
		// M36-ревью MINOR: неизвестный uuid в sel — фантом не создаём
		// (иначе в dst улетала пустая Environment).
		if _, ok := src.Environments[u]; !ok {
			return
		}
		include[u] = true
		if e := src.Environments[u]; e.IsGroup == 1 {
			for c := range e.Children {
				walk(c)
			}
		}
	}
	for _, u := range sel {
		walk(u)
	}

	// переносим выбранные окружения; родители вне экспорта → корень.
	// M36-ревью MINOR: глубокое копирование collection-полей, иначе
	// dst.Environments[u] разделяет Variables/Macros/Expect/Raw/... с src.
	for u := range include {
		e := cloneEnvironment(src.Environments[u])
		if _, ok := include[e.Parent]; !ok {
			e.Parent = RootUUID
		}
		dst.Environments[u] = e
	}
	// восстанавливаем children{} только для включённых групп (второй проход:
	// Map-итерация на результат не влияет).
	for u := range include {
		e := dst.Environments[u]
		if e.IsGroup != 1 {
			continue
		}
		kids := map[string]FlexInt{}
		for c := range src.Environments[u].Children {
			if include[c] {
				kids[c] = 1
			}
		}
		e.Children = kids
		dst.Environments[u] = e
	}
	return dst, len(include)
}

// cloneDefaults — глубокая копия Defaults (map-поля не алиасят источник).
func cloneDefaults(d Defaults) Defaults {
	c := d // struct-копия скаляров
	if d.GlobalVariables != nil {
		c.GlobalVariables = map[string]VarEntry{}
		for k, v := range d.GlobalVariables {
			c.GlobalVariables[k] = v
		}
	}
	if d.KeyBindings != nil {
		c.KeyBindings = map[string]map[string][]string{}
		for k, v := range d.KeyBindings {
			inner := map[string][]string{}
			for k2, v2 := range v {
				inner[k2] = append([]string(nil), v2...)
			}
			c.KeyBindings[k] = inner
		}
	}
	if d.Raw != nil {
		c.Raw = cloneAny(d.Raw)
	}
	return c
}

// cloneEnvironment — глубокая копия collection-полей Environment (скаляры —
// по значению; срезы/карты — заново, чтобы не алиасить источник).
func cloneEnvironment(e Environment) Environment {
	c := e
	if len(e.Variables) > 0 {
		c.Variables = cloneSlice(e.Variables)
	}
	if len(e.Expect) > 0 {
		c.Expect = cloneSlice(e.Expect)
	}
	if len(e.LocalBefore) > 0 {
		c.LocalBefore = cloneSlice(e.LocalBefore)
	}
	if len(e.LocalConnected) > 0 {
		c.LocalConnected = cloneSlice(e.LocalConnected)
	}
	if len(e.LocalAfter) > 0 {
		c.LocalAfter = cloneSlice(e.LocalAfter)
	}
	if len(e.Macros) > 0 {
		c.Macros = cloneSlice(e.Macros)
	}
	if len(e.Screenshots) > 0 {
		c.Screenshots = append([]any(nil), e.Screenshots...)
	}
	if e.Children != nil {
		c.Children = map[string]FlexInt{}
		for k, v := range e.Children {
			c.Children[k] = v
		}
	}
	if e.Cluster != nil {
		c.Cluster = append([]string(nil), e.Cluster...)
	}
	if e.Raw != nil {
		c.Raw = cloneAny(e.Raw)
	}
	if e.TerminalOptions != nil {
		c.TerminalOptions = make(map[string]any, len(e.TerminalOptions))
		for k, v := range e.TerminalOptions {
			c.TerminalOptions[k] = v
		}
	}
	return c
}
