import { computed, ref } from "vue";
import type { MediaOrganizePlan, MediaOrganizePlanAction } from "@/api/mediaOrganize";

export interface PlanActionMeta {
  isDir: boolean;
  typeLabel: string;
  dirLabel?: string;
  title?: string;
  se?: string;
  mode?: string;
  conf: number;
  confLow: boolean;
}

export interface PlanGroupRow {
  type: "action" | "range";
  action?: MediaOrganizePlanAction;
  range?: {
    key: string;
    season: number;
    startEpisode: number | null;
    endEpisode: number | null;
    consecutive: boolean;
    count: number;
    expanded: boolean;
    samplePattern: { oldPattern: string; newPattern: string };
  };
}

export interface PlanGroup {
  key: string;
  tmdbId: string;
  tmdbUrl: string;
  aiAssisted: boolean;
  classificationLabel: string;
  classificationDegraded: boolean;
  dirAction: MediaOrganizePlanAction | null;
  virtualDir: boolean;
  hasDirInfo: boolean;
  title: string;
  titleOld: string;
  titleNew: string;
  actions: MediaOrganizePlanAction[];
  actionCount: number;
  expanded: boolean;
  rows: PlanGroupRow[];
}

export interface PlanNeedsMatch {
  group_uid: string;
  media_kind?: string;
  dir_id?: string;
  dir_name?: string;
  title?: string;
  year?: number;
  reason?: string;
  count?: number;
  candidates?: Array<{ title: string; year: string }>;
}

const SKIP_PREVIEW_LIMIT = 20;

function episodeNumberFromName(name: string | undefined) {
  if (!name) return null;
  const m = name.match(/S(\d{1,3})E(\d{1,4})/i);
  if (!m) return null;
  return { season: parseInt(m[1], 10), episode: parseInt(m[2], 10) };
}

function isNumericEpisodeSource(name: string) {
  const stem = name.replace(/\.[^.]+$/, "").trim();
  return /^\d{1,4}(?:\s*(?:4k|2160p|1080p|720p))?$/i.test(stem);
}

function formatCollapsedOldPattern(oldP: string, items: Array<{ _ep: number | null }>, seasonKey: number) {
  if (oldP.startsWith("NUM{ee}")) {
    const ext = oldP.slice("NUM{ee}".length) || ".mp4";
    const eps = items.map((a) => a._ep).filter((v): v is number => v != null).sort((a, b) => a - b);
    if (!eps.length) return `*${ext}`;
    const pad = (n: number) => String(n).padStart(2, "0");
    if (eps.length === 1) return `${pad(eps[0])}${ext}`;
    const consecutive = eps.every((ep, i) => i === 0 || ep === eps[i - 1] + 1);
    if (consecutive) return `${pad(eps[0])}–${pad(eps[eps.length - 1])}${ext}`;
    return `${pad(eps[0])}…${pad(eps[eps.length - 1])}${ext}`;
  }
  return oldP.replace("S{ss}E{ee}", `S${String(seasonKey).padStart(2, "0")}E**`);
}

function formatCollapsedNewPattern(newP: string, seasonKey: number) {
  return newP.replace("S{ss}E{ee}", `S${String(seasonKey).padStart(2, "0")}E**`);
}

function patternOf(name: string, season: number | null, episode: number | null) {
  if (!name) return "";
  if (season != null && episode != null) {
    const tag = `S${String(season).padStart(2, "0")}E${String(episode).padStart(2, "0")}`;
    if (name.includes(tag)) return name.split(tag).join("S{ss}E{ee}");
    if (isNumericEpisodeSource(name)) {
      const ext = name.match(/(\.[^.]+)$/)?.[1] || "";
      return `NUM{ee}${ext}`;
    }
  }
  return name;
}

export function planActionMeta(action: MediaOrganizePlanAction | undefined): PlanActionMeta {
  const md = (action?.metadata ?? {}) as Record<string, unknown>;
  const conf = Math.round((action?.confidence ?? 0) * 100);
  const confLow = action?.confidence != null && conf < 80;
  const kindLabel = String(md.kind_label ?? "");
  // 番号方案的目录动作：目录改名 / 建作品目录 / 分类移动，都按「目录」渲染成组头
  if (kindLabel === "season_dir_rename" || kindLabel === "dir_rename" || kindLabel === "jav_dir_rename") {
    return {
      isDir: true,
      typeLabel: "目录",
      dirLabel:
        kindLabel === "season_dir_rename"
          ? "季目录标准化"
          : kindLabel === "jav_dir_rename"
            ? "目录名跟随番号"
            : "目录改名",
      conf,
      confLow,
    };
  }
  const isTv = md.media_kind === "tv";
  // 番号方案不走 TMDB，media_kind 恒为空；不特判的话每条都会被标成「电影」
  const isJav = String(md.jav_scheme ?? "") === "jav" || kindLabel.startsWith("jav_");
  let title = isTv ? "" : String(md.title ?? "").trim();
  if (!isTv && !title) title = String(action?.reason ?? "").split("|")[1]?.trim() || "";
  let se = "";
  if (isTv && md.season != null && md.episode != null) {
    se = `S${String(md.season).padStart(2, "0")}E${String(md.episode).padStart(2, "0")}`;
  }
  const isRename = md.mode ? md.mode === "rename" : action?.source_parent_id === action?.target_parent_id;
  return {
    isDir: false,
    typeLabel: isTv ? "剧集" : isJav ? "番号" : "电影",
    title,
    se,
    mode: isRename ? "原地重命名" : "移动并重命名",
    conf,
    confLow,
  };
}

export function useOrganizePlanPreview() {
  const relocates = ref<MediaOrganizePlanAction[]>([]);
  // 待删除的小文件（番号方案的「清理小文件」）。单独拎出来是因为它**不可逆** ——
  // 用户必须在执行前明确看到会删掉哪些文件。
  const deletes = ref<MediaOrganizePlanAction[]>([]);
  const skipped = ref<Array<Record<string, unknown>>>([]);
  const needsMatch = ref<PlanNeedsMatch[]>([]);
  const tmdbStatus = ref("");
  const scheme = ref("");
  const activeTab = ref<"plan" | "delete" | "skip" | "match">("plan");
  const groupExpanded = ref<Record<string, boolean>>({});
  const rangeExpanded = ref<Record<string, boolean>>({});
  const skipExpandedReasons = ref<Record<string, boolean>>({});
  const skipShowAll = ref<Record<string, boolean>>({});

  function loadPlan(plan: MediaOrganizePlan | null) {
    const actions = plan?.actions ?? [];
    relocates.value = actions.filter((a) => a.kind === "relocate");
    deletes.value = actions.filter((a) => a.kind === "delete_file");
    skipped.value = plan?.skipped ?? [];
    const rawNeeds = plan?.diagnostics?.needs_match;
    needsMatch.value = Array.isArray(rawNeeds)
      ? (rawNeeds as PlanNeedsMatch[]).filter((n) => n && n.group_uid)
      : [];
    tmdbStatus.value = String(plan?.diagnostics?.tmdb_status ?? "");
    // 方案取自计划本身；老计划没有这个字段，当作 TMDB 处理（保持原有行为）
    scheme.value = String(plan?.scheme ?? plan?.diagnostics?.scheme ?? "") || "tmdb";
    groupExpanded.value = {};
    rangeExpanded.value = {};
    skipExpandedReasons.value = {};
    skipShowAll.value = {};
    if (relocates.value.length) activeTab.value = "plan";
    else if (deletes.value.length) activeTab.value = "delete";
    else if (skipped.value.length) activeTab.value = "skip";
    else activeTab.value = "plan";
  }

  const isJav = computed(() => scheme.value === "jav");

  // 番号计划零 tmdb_id，不做门控的话「未匹配 TMDB」会对整份计划满屏误报
  const noTmdbCount = computed(() =>
    isJav.value ? 0 : relocates.value.filter((a) => !(a.metadata && a.metadata.tmdb_id)).length,
  );

  /** 待删除文件释放的总字节数。 */
  const freedBytes = computed(() =>
    deletes.value.reduce((sum, a) => {
      const size = Number((a.metadata as Record<string, unknown> | undefined)?.size ?? 0);
      return sum + (Number.isFinite(size) ? size : 0);
    }, 0),
  );

  const skipGroups = computed(() => {
    const map = new Map<string, Array<Record<string, unknown>>>();
    for (const item of skipped.value) {
      const reason = String(item.reason ?? "其它");
      if (!map.has(reason)) map.set(reason, []);
      map.get(reason)!.push(item);
    }
    return Array.from(map.entries()).map(([reason, items]) => ({ reason, items }));
  });

  const groups = computed<PlanGroup[]>(() => {
    const map = new Map<
      string,
      { key: string; tmdbId: string; dirAction: MediaOrganizePlanAction | null; actions: MediaOrganizePlanAction[] }
    >();
    for (const action of relocates.value) {
      const md = action.metadata ?? {};
      const tmdbId = String(md.tmdb_id ?? "");
      let key: string;
      if (tmdbId) key = `tmdb:${tmdbId}`;
      else if (md.group_uid) key = `g:${String(md.group_uid)}`;
      else {
        const fallback = String(action.reason ?? "").split("|")[1]?.trim() || action.target_name || "";
        key = `title:${fallback}`;
      }
      if (!map.has(key)) map.set(key, { key, tmdbId, dirAction: null, actions: [] });
      const bucket = map.get(key)!;
      if (String(md.kind_label ?? "") === "dir_rename") bucket.dirAction = action;
      else bucket.actions.push(action);
    }

    const out: PlanGroup[] = [];
    for (const g of map.values()) {
      let title = "";
      let titleOld = "";
      let titleNew = "";
      let virtualDir = false;
      if (g.dirAction) {
        titleOld = g.dirAction.source_name ?? "";
        titleNew = g.dirAction.target_name ?? "";
        title = titleNew;
      } else {
        const sample =
          g.actions.find((a) => {
            const k = String(a.metadata?.kind_label ?? "");
            return k !== "season_dir_rename" && k !== "dir_rename";
          }) ?? g.actions[0];
        const md = (sample?.metadata ?? {}) as Record<string, unknown>;
        if (md.group_old_dir_name && md.group_new_dir_name) {
          titleOld = String(md.group_old_dir_name);
          titleNew = String(md.group_new_dir_name);
          title = titleNew;
          virtualDir = true;
        } else if (sample) {
          title = String(sample.reason ?? "").split("|")[1]?.trim() || sample.target_name || "";
        } else {
          title = `tmdb-${g.tmdbId}`;
        }
      }

      const seasonBuckets = new Map<number, Array<MediaOrganizePlanAction & { _ep: number | null }>>();
      for (const action of g.actions) {
        const ep = episodeNumberFromName(action.target_name);
        const seasonKey = ep ? ep.season : 0;
        if (!seasonBuckets.has(seasonKey)) seasonBuckets.set(seasonKey, []);
        seasonBuckets.get(seasonKey)!.push({ ...action, _ep: ep ? ep.episode : null });
      }

      const rows: PlanGroupRow[] = [];
      for (const [seasonKey, list] of seasonBuckets.entries()) {
        list.sort((a, b) => (a._ep || 0) - (b._ep || 0));
        if (seasonKey === 0) {
          for (const a of list) rows.push({ type: "action", action: a });
          continue;
        }
        const subBuckets = new Map<string, { oldP: string; newP: string; items: typeof list }>();
        for (const a of list) {
          const oldP = patternOf(a.source_name ?? "", seasonKey, a._ep);
          const newP = patternOf(a.target_name ?? "", seasonKey, a._ep);
          const subKey = `${oldP}\u0001${newP}`;
          if (!subBuckets.has(subKey)) subBuckets.set(subKey, { oldP, newP, items: [] });
          subBuckets.get(subKey)!.items.push(a);
        }
        let groupIndex = 0;
        for (const { oldP, newP, items } of subBuckets.values()) {
          if (items.length < 3) {
            for (const a of items) rows.push({ type: "action", action: a });
            continue;
          }
          groupIndex += 1;
          const rangeKey = `${g.key}::S${seasonKey}::p${groupIndex}`;
          const eps = items.map((a) => a._ep).filter((v): v is number => v != null);
          const minEp = eps.length ? Math.min(...eps) : null;
          const maxEp = eps.length ? Math.max(...eps) : null;
          const consecutive = items.every((a, i) => i === 0 || a._ep === items[i - 1]._ep! + 1);
          const expanded = rangeExpanded.value[rangeKey] === true;
          rows.push({
            type: "range",
            range: {
              key: rangeKey,
              season: seasonKey,
              startEpisode: minEp,
              endEpisode: maxEp,
              consecutive,
              count: items.length,
              expanded,
              samplePattern: {
                oldPattern: formatCollapsedOldPattern(oldP, items, seasonKey),
                newPattern: formatCollapsedNewPattern(newP, seasonKey),
              },
            },
          });
          if (expanded) {
            for (const a of items) rows.push({ type: "action", action: a });
          }
        }
      }

      out.push({
        key: g.key,
        tmdbId: g.tmdbId,
        tmdbUrl: g.tmdbId
          ? `https://www.themoviedb.org/${
              [g.dirAction, ...g.actions].some((action) => action?.metadata?.media_kind === "tv")
                ? "tv"
                : "movie"
            }/${encodeURIComponent(g.tmdbId)}`
          : "",
        aiAssisted: [g.dirAction, ...g.actions].some(
          (action) => action?.metadata?.recognition_source === "ai",
        ),
        classificationLabel: (() => {
          const action = [g.dirAction, ...g.actions].find(
            (item) => item?.metadata?.classification_applied === true,
          );
          if (!action) return "";
          if (action.metadata?.classification_matched !== true) return "目标根";
          return String(
            action.metadata?.classification_relative_path ?? action.metadata?.classification_category ?? "",
          );
        })(),
        classificationDegraded: [g.dirAction, ...g.actions].some(
          (action) =>
            action?.metadata?.classification_applied === true &&
            action?.metadata?.classification_matched !== true,
        ),
        dirAction: g.dirAction,
        virtualDir,
        hasDirInfo: Boolean(g.dirAction || (titleOld && titleNew)),
        title,
        titleOld,
        titleNew,
        actions: g.actions,
        actionCount: g.actions.length + (g.dirAction ? 1 : 0),
        expanded: groupExpanded.value[g.key] === true,
        rows,
      });
    }
    return out;
  });

  function toggleGroup(key: string) {
    groupExpanded.value = { ...groupExpanded.value, [key]: !groupExpanded.value[key] };
  }

  function toggleRange(rangeKey: string) {
    rangeExpanded.value = { ...rangeExpanded.value, [rangeKey]: !rangeExpanded.value[rangeKey] };
  }

  function toggleSkipReason(reason: string) {
    skipExpandedReasons.value = { ...skipExpandedReasons.value, [reason]: !skipExpandedReasons.value[reason] };
  }

  function showAllSkip(reason: string) {
    skipShowAll.value = { ...skipShowAll.value, [reason]: true };
  }

  return {
    relocates,
    deletes,
    skipped,
    needsMatch,
    tmdbStatus,
    scheme,
    isJav,
    freedBytes,
    activeTab,
    noTmdbCount,
    skipGroups,
    groups,
    skipExpandedReasons,
    skipShowAll,
    skipPreviewLimit: SKIP_PREVIEW_LIMIT,
    loadPlan,
    toggleGroup,
    toggleRange,
    toggleSkipReason,
    showAllSkip,
    planActionMeta,
  };
}
