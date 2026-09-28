package strmscrape

import (
	"context"
	"sort"
	"strings"

	"litepan/internal/mediaorganize/javrules"
	"litepan/internal/settings"
)

// 番号墙上「整档隐藏的目录」的读写。
//
// 名单本身是**全局一份**（设置项 KeyStrmJavWallHiddenDirs），因为「未匹配」那类兜底
// 桶在每个任务里都叫同一个名字；勾选界面则在**页头那个「全部目录」按钮**里按当前任务
// 列磁盘上真实存在的一级目录（带部数），设置页里退化成按分类规则的目标目录列名单。

// JavWallHiddenDirOption 是勾选界面的一行。
type JavWallHiddenDirOption struct {
	Name string `json:"name"`
	// Count 是该目录下的卡片数。隐藏档也照常给数字 —— 用户得知道藏掉了多少。
	Count  int  `json:"count"`
	Hidden bool `json:"hidden"`
}

// JavWallHiddenDirList 是勾选界面的完整数据。
type JavWallHiddenDirList struct {
	// Dirs 是候选清单（磁盘上的一级目录 ∪ 规则的目标目录 ∪ 已存名单）。
	Dirs []JavWallHiddenDirOption `json:"dirs"`
	// Hidden 是当前生效的名单（勾上的那批）。
	Hidden []string `json:"hidden"`
	// FallbackName 是「用户还没勾过」时默认隐藏的那一个名字
	// （分类规则里那条兜底规则的目标目录，默认「未匹配」）。
	FallbackName string `json:"fallback_name"`
}

// ListJavWallHiddenDirs 列候选。
//
// taskID > 0 时按那个任务的实际输出目录列（带部数）—— 墙页头那个按钮走这条；
// taskID <= 0 时按分类规则的目标目录列（没有磁盘可扫）—— STRM 设置页那一行走这条。
func (s *Service) ListJavWallHiddenDirs(ctx context.Context, taskID int64) (JavWallHiddenDirList, error) {
	out := JavWallHiddenDirList{
		Hidden:       javWallHiddenDirs(s.settings),
		FallbackName: javWallDefaultHiddenName(s.settings),
	}
	if out.Hidden == nil {
		// 给前端一个稳定的空数组，别让它拿到 null 再到处判空。
		out.Hidden = []string{}
	}

	type candidate struct {
		count int
	}
	seen := map[string]candidate{}
	add := func(name string, count int) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		cur, ok := seen[name]
		if !ok || count > cur.count {
			seen[name] = candidate{count: count}
		}
	}

	if taskID > 0 {
		snap, err := s.javWallRows(ctx, taskID, false)
		if err != nil {
			return JavWallHiddenDirList{}, err
		}
		for _, cat := range snap.cats {
			// 空名是「根目录下散落的 .strm」那一桶，不是一个真实目录，不参与隐藏。
			if cat.Name == "" {
				continue
			}
			add(cat.Name, cat.Count)
		}
	} else {
		// 没有任务可扫时，退到分类规则的目标目录 —— 那批名字就是媒体库的一级目录。
		if s.settings != nil {
			for _, rule := range javrules.Parse(s.settings.String(settings.KeyMOJavRules)).ClassifyRules {
				add(rule.TargetName, 0)
			}
		}
	}

	// 兜底目录名与已存名单里的名字**一律列出来**，即使磁盘上还没有 / 已经没有了：
	// 前者要能「预先设好」，后者要能「取消勾选」（否则改名之后就永远藏着了）。
	if out.FallbackName != "" {
		add(out.FallbackName, 0)
	}
	for _, name := range out.Hidden {
		add(name, 0)
	}

	hiddenSet := make(map[string]struct{}, len(out.Hidden))
	for _, name := range out.Hidden {
		hiddenSet[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	// 兜底目录排最前（用户最可能要找的就是它），其余按部数多→少、再按名字。
	sort.Slice(names, func(i, j int) bool {
		fi, fj := names[i] == out.FallbackName, names[j] == out.FallbackName
		if fi != fj {
			return fi
		}
		if seen[names[i]].count != seen[names[j]].count {
			return seen[names[i]].count > seen[names[j]].count
		}
		return names[i] < names[j]
	})
	out.Dirs = make([]JavWallHiddenDirOption, 0, len(names))
	for _, name := range names {
		_, hidden := hiddenSet[name]
		out.Dirs = append(out.Dirs, JavWallHiddenDirOption{
			Name:   name,
			Count:  seen[name].count,
			Hidden: hidden,
		})
	}
	return out, nil
}

// UpdateJavWallHiddenDirs 保存名单（勾上 = 隐藏），并让所有番号墙的快照作废 ——
// 名单是全局的，改完每一面墙都要立刻跟上（只等 TTL 的话最长一分钟，用户会以为没保存上）。
func (s *Service) UpdateJavWallHiddenDirs(ctx context.Context, taskID int64, dirs []string) (JavWallHiddenDirList, error) {
	if s.settings == nil {
		return JavWallHiddenDirList{}, errJavWallSettingsUnavailable
	}
	// **空列表也要存**（`[]` ≠ 空串）：用户把兜底目录也取消勾选时，那份「一个都不藏」
	// 必须钉住，否则读侧会回落默认、把「未匹配」又藏回去。
	value := settings.EncodeJavWallHiddenDirs(dirs)
	if err := s.settings.Update(ctx, map[string]string{
		settings.KeyStrmJavWallHiddenDirs: value,
	}); err != nil {
		return JavWallHiddenDirList{}, err
	}
	s.InvalidateJavWallAll()
	return s.ListJavWallHiddenDirs(ctx, taskID)
}

// javWallDefaultHiddenName 是「用户还没勾过」时默认隐藏的那个名字：
// 分类规则里那条兜底规则（恒为最后一条）的目标目录。
func javWallDefaultHiddenName(svc *settings.Service) string {
	if svc == nil {
		return javrules.FallbackTargetName()
	}
	rules := javrules.Parse(svc.String(settings.KeyMOJavRules))
	if len(rules.ClassifyRules) > 0 {
		if name := strings.TrimSpace(rules.ClassifyRules[len(rules.ClassifyRules)-1].TargetName); name != "" {
			return name
		}
	}
	return javrules.FallbackTargetName()
}
