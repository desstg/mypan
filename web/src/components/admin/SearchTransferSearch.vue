<script setup lang="ts">
/**
 * 搜索 tab：复刻自建盘搜（pansou-web）的搜索页，换成 MyPan 品牌。
 *
 * 与盘搜原站的三处有意差异：
 *  1. 顶部多一条「保存到」—— 选好网盘与目录后，结果行上出现「转存」按钮；
 *  2. 结果按网盘类型分 tab，但**能转存的类型才有按钮**，其余置灰并说明原因
 *     （夸克/百度/阿里等当前版本没有投递器，与 CAPABILITIES.md 的结论一致）；
 *  3. 不做盘搜原站那套「链接健康检测 / 导出 / 账号中心」—— 那些是它自己的插件生态。
 *
 * 站点探活放在最前面：没装内置盘搜、用户也没填地址时，这一页要明确告诉他
 * 「搜索功能需要先装盘搜」，而不是给一个空列表让人以为是「没资源」。
 */
import { computed, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import { searchTransferApi } from "@/api/searchTransfer";
import { getApiErrorMessage } from "@/api/client";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import type { Account } from "@/api/types";
import type {
  SaveCapabilities,
  SearchHit,
  SearchResult,
  SearchSite,
} from "@/types/search-transfer";

const props = defineProps<{
  accounts: Account[];
  accountsLoading: boolean;
}>();

/** 转存成功后通知外壳 —— 转存记录那一侧据此在下次打开时重拉。 */
const emit = defineEmits<{ saved: [] }>();

/** 网盘类型的中文名。与盘搜原站 utils/diskTypes.ts 一致，另补上 LitePan 认得的类型。 */
const DISK_TYPE_NAMES: Record<string, string> = {
  baidu: "百度",
  aliyun: "阿里",
  "115": "115",
  "123": "123",
  quark: "夸克",
  xunlei: "迅雷",
  guangya: "光鸭",
  mobile: "移动",
  tianyi: "天翼",
  uc: "UC",
  pikpak: "PikPak",
  ed2k: "电驴",
  magnet: "磁力",
  other: "其他",
};

function diskTypeName(type: string) {
  return DISK_TYPE_NAMES[type] || type;
}

// ——————————————————— 站点探活 ———————————————————

const site = ref<SearchSite | null>(null);
const siteLoading = ref(false);

async function probeSite() {
  siteLoading.value = true;
  try {
    site.value = await searchTransferApi.site();
  } catch (error) {
    site.value = {
      base_url: "",
      configured: false,
      ready: false,
      reason: getApiErrorMessage(error, "无法连接搜索服务"),
    };
  } finally {
    siteLoading.value = false;
  }
}

onMounted(probeSite);

// ——————————————————— 搜索 ———————————————————

const keyword = ref("");
const includeWords = ref("");
const excludeWords = ref("");
const showAdvanced = ref(false);
const searching = ref(false);
const result = ref<SearchResult | null>(null);
const activeType = ref("");

async function doSearch() {
  const kw = keyword.value.trim();
  if (!kw) return;
  if (site.value && !site.value.ready) {
    toast.warning(
      site.value.base_url
        ? "盘搜连不上，请检查「搜索服务地址」填得对不对"
        : "还没有可用的盘搜：请启用内置盘搜，或到 TG 订阅设置里填自己的盘搜地址",
    );
    return;
  }
  searching.value = true;
  try {
    // 搜索范围跟着「保存到」的网盘走：选了 123 就只要 123 的结果 + 磁力。
    // 不限制的话用户搜出来的全是他存不进去的链接（夸克/百度/阿里…），
    // 一屏按钮全是灰的 —— 这是实测反馈的原话。
    const res = await searchTransferApi.search({
      keyword: kw,
      cloud_types: searchCloudTypes.value,
    });
    result.value = res;
    // 默认落在**磁力**那一档：visibleTypes 已经把它排在第一个，而且磁力是
    // 覆盖最好、哪个网盘都能存进去的一类。
    // 磁力一条都没有时（站点偶尔会这样）自然落到第二个，不会是空档。
    const types = visibleTypes.value;
    activeType.value = types.length ? types[0] : "";
  } catch (error) {
    result.value = null;
    toast.error(getApiErrorMessage(error, "搜索失败"));
  } finally {
    searching.value = false;
  }
}

/**
 * 搜索要哪些网盘类型。
 *
 * 规则：**所选账号的网盘类型 + 磁力**。
 *   - 磁力永远保留：任何网盘都能靠离线下载（或内置下载器）把它存进去，
 *     而且实测盘搜的磁力覆盖最好（自建实例上国产内容几乎全是磁力）。
 *   - 账号那一类跟在后面（115 / 123 / quark…），只有它才可能带分享链。
 *
 * 没选账号时不限制（后端给一整套宽类型）—— 用户还没决定存哪，先让他看全。
 */
const searchCloudTypes = computed<string[] | undefined>(() => {
  const accountType = capabilities.value?.pansou_cloud_type;
  if (!accountType) return undefined;
  return ["magnet", accountType];
});

const visibleTypes = computed(() => {
  const merged = result.value?.merged_by_type || {};
  const own = capabilities.value?.pansou_cloud_type || "";
  // 磁力排最前（覆盖最好、且哪个网盘都能靠离线下载存进去），
  // 其次是所选账号那一类（唯一可能带分享链的），其余按条数。
  const rank = (t: string) => (t === "magnet" ? 0 : t === own ? 1 : 2);
  return Object.keys(merged)
    .filter((t) => (merged[t] || []).length > 0)
    .sort((a, b) => rank(a) - rank(b) || (merged[b] || []).length - (merged[a] || []).length);
});

const currentHits = computed<SearchHit[]>(() => {
  const merged = result.value?.merged_by_type || {};
  const raw = merged[activeType.value] || [];
  const inc = includeWords.value.split(/[,\s]+/).filter(Boolean).map((w) => w.toLowerCase());
  const exc = excludeWords.value.split(/[,\s]+/).filter(Boolean).map((w) => w.toLowerCase());
  return raw.filter((hit) => {
    const note = (hit.note || "").toLowerCase();
    if (inc.length && !inc.some((w) => note.includes(w))) return false;
    if (exc.length && exc.some((w) => note.includes(w))) return false;
    return true;
  });
});

// ——————————————————— 保存目标 ———————————————————

const accountId = ref<number | null>(null);
const targetParentId = ref("");
const targetDisplayPath = ref("");
const pickerOpen = ref(false);
const capabilities = ref<SaveCapabilities | null>(null);

const accountOptions = computed(() =>
  props.accounts.map((a) => ({ value: a.id, label: a.name })),
);

const targetLabel = computed(() => targetDisplayPath.value || "尚未选择目录");

const targetReady = computed(() => Boolean(accountId.value && targetParentId.value));

watch(accountId, async (id) => {
  capabilities.value = null;
  targetParentId.value = "";
  targetDisplayPath.value = "";
  if (!id) return;
  try {
    capabilities.value = await searchTransferApi.saveCapabilities(id);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "读取网盘保存能力失败"));
  }
  // 换账号 = 换搜索范围。已经搜过的话就按新范围重搜一次 ——
  // 不重搜的话用户会看到「改成 123 了，结果还是 115 和磁力」，以为没生效。
  if (result.value && keyword.value.trim()) void doSearch();
});

function openPicker() {
  if (!accountId.value) {
    toast.warning("请先选择要保存到的网盘");
    return;
  }
  pickerOpen.value = true;
}

function applyTarget(payload: { parentId: string; path: string }) {
  pickerOpen.value = false;
  targetParentId.value = payload.parentId;
  targetDisplayPath.value = payload.path;
}

// ——————————————————— 逐条转存 ———————————————————

/** 分享域名白名单 —— 与后端 shareHosts115 / shareHosts123 / shareHostsQuark 同源，改一处要核对另一处。 */
const SHARE_115_HOSTS = ["115.com", "115cdn.com", "anxia.com"];
const SHARE_123_HOSTS = [
  "123pan.com",
  "123pan.cn",
  "123684.com",
  "123685.com",
  "123865.com",
  "123912.com",
  "123592.com",
];
const SHARE_QUARK_HOSTS = ["quark.cn"];
const SHARE_GUANGYA_HOSTS = ["guangyapan.com"];

/** 分享页的路径形状：`/s/<2-64 位码>` —— 光看域名不够（`115.com/download/x` 不是分享）。 */
const SHARE_PATH_RE = /^\/s\/[A-Za-z0-9_-]{2,64}\/?$/;
/** 光鸭的分享码更长（`<数字id>_<token>`，实测 33 位），单独一条更宽的规则。 */
const SHARE_GUANGYA_PATH_RE = /^\/s\/[A-Za-z0-9_-]{2,128}\/?$/;

function shareHost(raw: string, allowed: string[], pathRe = SHARE_PATH_RE): boolean {
  try {
    const u = new URL(raw);
    const h = u.hostname.toLowerCase();
    if (!allowed.some((a) => h === a || h.endsWith("." + a))) return false;
    return pathRe.test(u.pathname);
  } catch {
    return false;
  }
}

type LinkKind = "offline" | "share115" | "share123" | "shareQuark" | "shareGuangya" | "unsupported";

function classify(raw: string): { kind: LinkKind; scheme: string } {
  const lower = raw.toLowerCase();
  if (lower.startsWith("magnet:?")) return { kind: "offline", scheme: "magnet" };
  if (lower.startsWith("ed2k://")) return { kind: "offline", scheme: "ed2k" };
  if (lower.startsWith("thunder://")) return { kind: "offline", scheme: "thunder" };
  if (lower.startsWith("ftp://")) return { kind: "offline", scheme: "ftp" };
  if (lower.startsWith("http://") || lower.startsWith("https://")) {
    if (shareHost(raw, SHARE_115_HOSTS)) return { kind: "share115", scheme: "share_115" };
    if (shareHost(raw, SHARE_123_HOSTS)) return { kind: "share123", scheme: "share_123" };
    if (shareHost(raw, SHARE_QUARK_HOSTS)) return { kind: "shareQuark", scheme: "share_quark" };
    if (shareHost(raw, SHARE_GUANGYA_HOSTS, SHARE_GUANGYA_PATH_RE)) return { kind: "shareGuangya", scheme: "share_guangya" };
    return { kind: "offline", scheme: "http" };
  }
  return { kind: "unsupported", scheme: "" };
}

/** 一条结果能不能转存；不能的话给出置灰理由。 */
function verdict(hit: SearchHit): { ok: boolean; reason: string; kind: LinkKind } {
  const { kind, scheme } = classify(hit.url);
  if (kind === "unsupported") {
    return { ok: false, reason: "认不出链接类型，可复制链接手动处理", kind };
  }
  if (kind === "share115" || kind === "share123" || kind === "shareQuark" || kind === "shareGuangya") {
    const caps = capabilities.value;
    const cap =
      kind === "share123"
        ? caps?.share_123
        : kind === "shareQuark"
          ? caps?.share_quark
          : kind === "shareGuangya"
            ? caps?.share_guangya
            : caps?.share_115;
    if (cap && !cap.ok) {
      return { ok: false, reason: cap.reason || "该账号当前不能转存这种分享", kind };
    }
    return { ok: true, reason: "", kind };
  }
  const cap = capabilities.value?.schemes?.[scheme];
  if (cap && !cap.ok) return { ok: false, reason: cap.reason || "该网盘不支持这种链接", kind };
  return { ok: true, reason: "", kind };
}

/** 结果行的按钮文案/禁用原因 —— 未选目标时统一提示先选。 */
function actionState(hit: SearchHit) {
  if (!targetReady.value) {
    return { disabled: true, reason: accountId.value ? "请先选择保存目录" : "请先选择要保存到的网盘" };
  }
  const v = verdict(hit);
  return { disabled: !v.ok, reason: v.reason };
}

const savingKey = ref("");

async function saveHit(hit: SearchHit) {
  if (!targetReady.value || !accountId.value) return;
  savingKey.value = hit.url;
  try {
    // 与「转存」tab 共用同一个批量接口：类型分派与**转存记录留档**都在服务端做，
    // 这里只是把一次提交包装成单条。搜索页也走它，记录里的 source 才会是 search。
    const res = await searchTransferApi.saveLinks({
      account_id: accountId.value,
      items: [{ url: hit.url, password: hit.password, share_name: hit.note }],
      target_parent_id: targetParentId.value,
      target_display_path: targetDisplayPath.value,
      source: "search",
    });
    const item = res.items?.[0];
    if (item?.ok) {
      // 走内置下载器时补一句：那条路是**本机在下载**，网盘上要等它下完才看得见。
      const note = item.provider === "builtin" ? "（在内置下载器中，下完自动上传）" : "";
      toast.success((item.message || "已提交") + note);
      // 记录写完了，通知外壳的「转存记录」按钮下次打开时刷新。
      emit("saved");
    } else {
      toast.error(item?.message || "转存失败");
    }
  } catch (error) {
    toast.error(getApiErrorMessage(error, "转存失败"));
  } finally {
    savingKey.value = "";
  }
}

async function copyLink(hit: SearchHit) {
  const text = hit.password ? `${hit.url} 提取码: ${hit.password}` : hit.url;
  if (await copyTextToClipboard(text)) toast.success("已复制");
  else toast.error("复制失败");
}

function formatTime(raw?: string) {
  if (!raw) return "";
  const d = new Date(raw);
  if (Number.isNaN(d.getTime())) return raw;
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}
</script>

<template>
  <div class="st-search">
    <!-- 站点不可用：这是**要展示的状态**，不是报错弹窗。
         两种情况分开写：根本没配地址（要告诉用户去哪装/去哪填），
         配了但连不上（要告诉用户去查地址/网络）。 -->
    <section v-if="site && !site.ready" class="st-card st-card--warn">
      <div class="st-card__head">
        <h3>{{ site.base_url ? "盘搜连不上" : "还没有可用的盘搜" }}</h3>
        <p v-if="site.base_url" class="st-hint">
          地址 <code>{{ site.base_url }}</code> 连不上（{{ site.reason || "未知原因" }}）。
          下面两条总有一个适合你。
        </p>
        <p v-else class="st-hint">
          搜索功能依赖一个**盘搜服务**，MyPan 自己不含搜索能力。装上下面任意一个就能用。
        </p>
      </div>
      <ol class="st-steps">
        <li>
          <b>用内置盘搜。</b>把 <code>docker-compose.yml</code> 里
          <code>pansou</code> 那段和 <code>litepan</code> 的
          <code>LITEPAN_PANSOU_BASE_URL</code> 一起打开（原文件里已经写好），
          <code>docker compose up -d</code> 即可 —— 地址会自动指向它，不用手填。
        </li>
        <li>
          <b>或者：填自己的盘搜地址。</b>到「影视订阅 → 推送设置 → 网盘搜索 → 搜索服务地址」
          填上（比如 <code>http://192.168.31.4:805</code>），只填到端口、不带
          <code>/api/search</code>。<b>填了就用你的，两处都生效。</b>
          <span class="st-steps__note">
            注意：如果 pansou 跑在别的主机上，地址要写那台机器的**局域网 IP**，
            不能写 <code>127.0.0.1</code>。
          </span>
        </li>
      </ol>
      <div class="st-actions">
        <AppButton type="button" variant="secondary" :disabled="siteLoading" @click="probeSite">
          {{ siteLoading ? "检测中…" : "重新检测" }}
        </AppButton>
      </div>
    </section>

    <template v-else>
      <section class="st-card">
        <div class="st-searchbar">
          <AppInput
            v-model="keyword"
            placeholder="搜索资源、电影、剧集…"
            @keydown.enter="doSearch"
          />
          <button
            type="button"
            class="st-adv-toggle"
            :class="{ 'is-on': showAdvanced, 'is-active': includeWords.trim() || excludeWords.trim() }"
            title="高级筛选"
            @click="showAdvanced = !showAdvanced"
          >
            筛选
          </button>
          <AppButton type="button" variant="primary" :disabled="searching" @click="doSearch">
            {{ searching ? "搜索中…" : "搜索" }}
          </AppButton>
        </div>

        <div v-if="showAdvanced" class="st-advanced">
          <label class="st-field">
            <span class="st-field__label">包含关键词（空格分隔，满足其一）</span>
            <AppInput v-model="includeWords" placeholder="4k hdr 高码" />
          </label>
          <label class="st-field">
            <span class="st-field__label">排除关键词（空格分隔，命中即排除）</span>
            <AppInput v-model="excludeWords" placeholder="预告 花絮 枪版" />
          </label>
        </div>

        <p v-if="site" class="st-src">
          搜索源：<code>{{ site.base_url }}</code>
          <span v-if="!site.configured" class="st-badge">内置</span>
        </p>
      </section>

      <section class="st-card">
        <div class="st-card__head">
          <h3>保存到</h3>
          <p class="st-hint">
            选好后，每条结果的「转存」按钮才可用。
            <template v-if="capabilities?.pansou_cloud_type">
              搜索范围会跟着收窄到「<b>{{ diskTypeName(capabilities.pansou_cloud_type) }}</b> +
              磁力」。
            </template>
          </p>
        </div>
        <div class="st-target">
          <label class="st-field">
            <span class="st-field__label">网盘账号</span>
            <select v-model.number="accountId" class="st-select" :disabled="accountsLoading">
              <option :value="null" disabled>请选择</option>
              <option v-for="opt in accountOptions" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </option>
            </select>
          </label>
          <label class="st-field st-field--grow">
            <span class="st-field__label">保存目录</span>
            <button type="button" class="st-path" :disabled="!accountId" @click="openPicker">
              {{ targetLabel }}
            </button>
          </label>
        </div>
      </section>

      <section v-if="result" class="st-card">
        <div class="st-card__head">
          <h3>
            「{{ result.keyword }}」的结果
            <span class="st-count">{{ result.total }} 条</span>
          </h3>
        </div>

        <div v-if="!visibleTypes.length" class="st-empty">未找到相关资源，换个关键词试试。</div>

        <template v-else>
          <div class="st-tabs">
            <button
              v-for="type in visibleTypes"
              :key="type"
              type="button"
              class="st-tab"
              :class="{ 'is-on': activeType === type }"
              @click="activeType = type"
            >
              {{ diskTypeName(type) }} ({{ (result.merged_by_type[type] || []).length }})
            </button>
          </div>

          <div v-if="!currentHits.length" class="st-empty">当前筛选条件下没有结果。</div>

          <ul v-else class="st-results">
            <li v-for="(hit, i) in currentHits" :key="i" class="st-result">
              <div class="st-result__main">
                <div class="st-result__title" :title="hit.note">{{ hit.note }}</div>
                <div class="st-result__meta">
                  <span v-if="hit.source" class="st-result__source">{{ hit.source }}</span>
                  <span v-if="hit.source && hit.datetime">·</span>
                  <span v-if="hit.datetime">{{ formatTime(hit.datetime) }}</span>
                </div>
                <div class="st-result__url" :title="hit.url">{{ hit.url }}</div>
              </div>
              <div class="st-result__actions">
                <button
                  v-if="hit.password"
                  type="button"
                  class="st-mini"
                  @click="copyLink(hit)"
                >
                  提取码 {{ hit.password }}
                </button>
                <button v-else type="button" class="st-mini" @click="copyLink(hit)">复制</button>
                <AppButton
                  type="button"
                  variant="primary"
                  size="sm"
                  :disabled="actionState(hit).disabled || savingKey === hit.url"
                  :title="actionState(hit).disabled ? actionState(hit).reason : ''"
                  @click="saveHit(hit)"
                >
                  {{ savingKey === hit.url ? "…" : "转存" }}
                </AppButton>
              </div>
            </li>
          </ul>
        </template>
      </section>

      <section v-else-if="searching" class="st-card">
        <div class="st-empty">正在搜索…</div>
      </section>
    </template>

    <FolderPickerModal
      :open="pickerOpen"
      title="选择保存目录"
      confirm-text="选择当前目录"
      :selectable-account="false"
      :account-id="accountId"
      :accounts="props.accounts"
      @close="pickerOpen = false"
      @resolve="applyTarget"
    />
  </div>
</template>
