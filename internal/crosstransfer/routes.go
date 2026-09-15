package crosstransfer

import (
	"sort"

	"litepan/internal/driver"
)

type PanMeta struct {
	Driver           string   `json:"driver"`
	Name             string   `json:"name"`
	Logo             string   `json:"logo"`
	Color            string   `json:"color"`
	ConflictPolicies []string `json:"conflict_policies"`
}

type Route struct {
	ID            string  `json:"id"`
	From          PanMeta `json:"from"`
	To            PanMeta `json:"to"`
	Method        string  `json:"method"`
	MethodLabel   string  `json:"method_label"`
	Bidirectional bool    `json:"bidirectional"`
}

func BuildRoutes() []Route {
	infos := driver.List()
	names := make([]string, 0, len(infos))
	byName := make(map[string]driver.DriverInfo, len(infos))
	for _, info := range infos {
		names = append(names, info.Name)
		byName[info.Name] = info
	}
	sort.Strings(names)

	feasible := map[[2]string][]string{}
	for _, a := range names {
		for _, b := range names {
			methods := feasibleMethods(byName[a], byName[b])
			if len(methods) > 0 {
				feasible[[2]string{a, b}] = methods
			}
		}
	}

	var routes []Route
	seen := map[[2]string]struct{}{}
	for _, a := range names {
		for _, b := range names {
			key := [2]string{a, b}
			if _, ok := seen[key]; ok {
				continue
			}
			methods, ok := feasible[key]
			if !ok {
				continue
			}
			bidirectional := len(feasible[[2]string{b, a}]) > 0
			methodID := methods[0]
			method := Methods[methodID]
			routes = append(routes, Route{
				ID:            a + "__" + b,
				From:          panMeta(byName[a]),
				To:            panMeta(byName[b]),
				Method:        methodID,
				MethodLabel:   method.Label,
				Bidirectional: bidirectional,
			})
			seen[key] = struct{}{}
			if bidirectional {
				seen[[2]string{b, a}] = struct{}{}
			}
		}
	}
	return routes
}

func feasibleMethods(src, dst driver.DriverInfo) []string {
	provides := toSet(src.ProvideHashes)
	accepts := toSet(dst.RapidUpload)
	var out []string
	for id := range Methods {
		if provides[id] && accepts[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func panMeta(info driver.DriverInfo) PanMeta {
	name := info.DisplayName
	if name == "" {
		name = info.Name
	}
	policies := info.UploadConflictPolicies
	if len(policies) == 0 {
		policies = []string{"skip", "rename", "overwrite"}
	} else if !containsPolicy(policies, "skip") {
		policies = append([]string{"skip"}, policies...)
	}
	return PanMeta{
		Driver:           info.Name,
		Name:             name,
		Logo:             info.CardLogo,
		Color:            info.CardColor,
		ConflictPolicies: policies,
	}
}

func containsPolicy(items []string, policy string) bool {
	for _, item := range items {
		if item == policy {
			return true
		}
	}
	return false
}

func toSet(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}
