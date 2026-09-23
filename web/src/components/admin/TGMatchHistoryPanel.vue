<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import SettingsSegment from "@/components/admin/SettingsSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  clearTGRecords,
  fetchTGChannels,
  fetchTGRecords,
  fetchTGSubscriptions,
  ignoreTGRecord,
  pushTGRecord,
} from "@/api/tgSubscribe";
import { confirm } from "@/composables/useConfirm";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import type { TGChannel, TGMatchRecord, TGRecordStatus, TGSubscription } from "@/types/tg-subscribe";

const props = withDefaults(
  defineProps<{
    /** 紧凑模式：详情弹窗里复用，只读最近若干条、隐藏筛选栏。 */
    compact?: boolean;
    /** 限定只看某条订阅的记录。 */
    subscriptionId?: number;
  }>(),
  { compact: false, subscriptionId: 0 },
);


const STATUS_OPTIONS: { value: string; label: string }[] = [
  { value: "", label: "全部" },
  { value: "pushed", label: "已推送" },
  { value: "upgraded", label: "洗版升级" },
  { value: "pending", label: "等待选优" },
  { value: "ambiguous", label: "待确认" },
  { value: "unmatched", label: "未匹配" },
  { value: "unsupported", label: "暂不支持投递" },
  { value: "filtered", label: "被过滤" },
  { value: "superseded", label: "已被取代" },
  { value: "duplicate", label: "已处理过" },
  { value: "failed", label: "失败" },
  { value: "unretryable", label: "失败（不重试）" },
  { value: "ignored", label: "已忽略" },
];

const loading = ref(false);
const records = ref<TGMatchRecord[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = 20;

const filterStatus = ref("");
const filterKeyword = ref("");
const channels = ref<TGChannel[]>([]);
const subscriptions = ref<TGSubscription[]>([]);

const detailOpen = ref(false);
const active = ref<TGMatchRecord | null>(null);
const manualSubId = ref<number>(0);
const pushing = ref(false);

const statusToneMap: Record<TGRecordStatus, "success" | "warning" | "brand" | "danger" | "muted"> = {
  pushed: "success",
  upgraded: "success",
  pending: "brand",
  filtered: "warning",
  ambiguous: "warning",
  unsupported: "warning",
  unmatched: "muted",
  duplicate: "muted",
  superseded: "muted",
  ignored: "muted",
  failed: "danger",
  // 比 failed 更重：failed 还有重试机会，unretryable 是判死的。
  unretryable: "danger",
};

/**
 * 未匹配的记录里 subscription_id 只是「得分最高的候选」，不是匹配结果
 * （后端对未匹配的记录也会写这个字段）。所以这类记录**不能**预选订阅 ——
 * 预选之后点「立即推送」就会把 A 片转存进 B 订阅的目录，而且成功了也看不出来。
 * 待确认记录相反：那个候选正是系统给出的猜测，预选是对的。
 */
const isUnmatched = computed(() => active.value?.status === "unmatched");

/**
 * 订阅下拉本身不能用的原因：类型根本没有投递器时，选了也没意义。
 *
 * ⚠️ 别把「未匹配但还没选订阅」并进来 —— 那正是需要用户去选的情况，
 * 下拉一禁用就再也选不了，按钮永远点不动。
 */
const selectBlockedReason = computed(() =>
  active.value?.status === "unsupported" ? active.value.reason : "",
);

/** 记录不能手动推送的原因：类型没有投递器、或未匹配却没指定订阅时，点了必然出错。 */
const pushBlockedReason = computed(() => {
  if (selectBlockedReason.value) return selectBlockedReason.value;
  if (isUnmatched.value && manualSubId.value <= 0) {
    return "这条记录没匹配上任何订阅，请先选择要推送到哪一条";
  }
  return "";
});

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)));

const subscriptionOptions = computed(() => [
  { value: 0, label: "不指定订阅" },
  ...subscriptions.value.map((s) => ({
    value: s.id,
    label: `${s.title}${s.year ? ` (${s.year})` : ""}`,
  })),
]);

async function load() {
  loading.value = true;
  try {
    const result = await fetchTGRecords({
      status: filterStatus.value,
      keyword: filterKeyword.value,
      subscription_id: props.subscriptionId || 0,
      limit: props.compact ? 10 : pageSize,
      offset: props.compact ? 0 : (page.value - 1) * pageSize,
    });
    records.value = result.items;
    total.value = result.total;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载匹配历史失败"));
  } finally {
    loading.value = false;
  }
}

async function loadRefs() {
  try {
    // 订阅列表两种模式都要（紧凑模式的手动推送也要能改指定订阅）；
    // 频道下拉只在完整模式用得上。
    const subs = await fetchTGSubscriptions();
    subscriptions.value = subs;
    if (!props.compact) {
      channels.value = await fetchTGChannels();
    }
  } catch {
    // 下拉数据加载失败不影响主列表，静默即可。
  }
}

function applyFilter() {
  page.value = 1;
  void load();
}

function goPage(delta: number) {
  const next = page.value + delta;
  if (next < 1 || next > totalPages.value) return;
  page.value = next;
  void load();
}

function openDetail(record: TGMatchRecord) {
  active.value = record;
  // 未匹配的记录不带出候选订阅，避免默认值把人引到一条不相干的订阅上。
  manualSubId.value = record.status === "unmatched" ? 0 : record.subscription_id || 0;
  detailOpen.value = true;
}

async function copyMagnet(record: TGMatchRecord) {
  const ok = await copyTextToClipboard(record.magnet);
  if (ok) toast.success("磁力链接已复制");
  else toast.error("复制失败，请手动选择");
}

/** 手动推送：待确认 / 未匹配 / 推送失败的兜底入口。 */
async function manualPush() {
  if (!active.value) return;
  pushing.value = true;
  try {
    await pushTGRecord(active.value.id, manualSubId.value);
    toast.success("已提交推送，稍后可在任务管理里看到离线任务");
    detailOpen.value = false;
    await load();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "推送失败"));
  } finally {
    pushing.value = false;
  }
}

async function ignore() {
  if (!active.value) return;
  try {
    await ignoreTGRecord(active.value.id);
    toast.success("已忽略");
    detailOpen.value = false;
    await load();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "操作失败"));
  }
}

async function clearAll() {
  try {
    await confirm({
      title: "清空匹配历史",
      message: "确认清空全部匹配历史？这不会影响订阅与已推送的任务。",
      confirmText: "清空",
      danger: true,
      icon: "trash",
    });
  } catch {
    return;
  }
  try {
    const { removed } = await clearTGRecords(new Date().toISOString());
    toast.success(`已清理 ${removed} 条记录`);
    page.value = 1;
    await load();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "清空失败"));
  }
}

/**
 * 「来源」列。
 *
 * 先按 id 查现成的频道（备注改过就显示最新的），查不到再退回落库时的快照 ——
 * 网盘搜索来的记录 channel_id 恒为 0，压根没有对应频道，它的来源（哪个搜索源
 * 给出的这条结果）就在 chat_title 里，显示成 `#0` 等于把唯一有用的信息丢了。
 */
function recordSource(record: TGMatchRecord) {
  const found = channels.value.find((c) => c.id === record.channel_id);
  if (found) return found.remark || found.title || found.chat_id;
  return record.chat_title || "—";
}

function episodeText(record: TGMatchRecord) {
  if (record.season < 0 && record.episode < 0) return "";
  const season = record.season >= 0 ? `S${String(record.season).padStart(2, "0")}` : "";
  const episode = record.episode >= 0 ? `E${String(record.episode).padStart(2, "0")}` : "";
  const end = record.episode_end >= 0 ? `–E${String(record.episode_end).padStart(2, "0")}` : "";
  return `${season}${episode}${end}`;
}

function qualityText(record: TGMatchRecord) {
  return [record.resolution, record.source_tag, record.video_codec].filter(Boolean).join(" ") || "—";
}

function sizeText(bytes: number) {
  if (!bytes) return "—";
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} GB`;
  return `${Math.round(bytes / (1 << 20))} MB`;
}

function timeText(raw?: string) {
  if (!raw) return "—";
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

watch(() => props.subscriptionId, applyFilter);
watch(filterStatus, applyFilter);

onMounted(async () => {
  await Promise.all([load(), loadRefs()]);
});

defineExpose({ load });
</script>

<template>
  <div class="tg-panel">
    <SettingsCard :title="compact ? '最近匹配记录' : '匹配历史'" accent="var(--brand)">
      <template #head-aside>
        <span v-if="compact">这个订阅最近的匹配情况，点任意一行看判定原因。</span>
        <span v-else>每条命中与未命中都记在这里，包括为什么没推。</span>
      </template>
      <template v-if="!compact" #head-actions>
        <AppButton
          type="button"
          variant="ghost"
          size="sm"
          :disabled="!records.length"
          @click="clearAll"
        >
          清空历史
        </AppButton>
      </template>

      <div v-if="!compact" class="tg-panel__toolbar" style="margin-bottom: 14px">
        <SettingsSegment
          v-model="filterStatus"
          label="按状态筛选"
          :options="STATUS_OPTIONS"
        />
        <div style="min-width: 220px">
          <AppInput
            v-model="filterKeyword"
            placeholder="搜索发布名 / 原因"
            @keyup.enter="applyFilter"
          />
        </div>
        <AppButton type="button" variant="secondary" size="sm" @click="applyFilter">筛选</AppButton>
      </div>

      <div v-if="records.length" class="admin-panel-table-wrap">
        <table class="admin-table tg-record-table">
          <thead>
            <tr>
              <th style="width: 150px">时间</th>
              <th style="width: 130px">频道</th>
              <th>发布名</th>
              <th style="width: 110px">类型</th>
              <th style="width: 130px">订阅</th>
              <th style="width: 110px">画质</th>
              <th style="width: 90px">状态</th>
              <!-- 原因：详情页那个「最近匹配记录」不显示 —— 那里列本来就挤，
                   而且多半只写一句「未匹配上任何订阅」，占一整列不划算。
                   要看原因点开这一行，弹窗里有完整的「判定原因」。 -->
              <th v-if="!compact" style="width: 90px">原因</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="record in records" :key="record.id" @click="openDetail(record)">
              <td>{{ timeText(record.created_at) }}</td>
              <td>
                <div class="tg-record__name tg-record__name--narrow" :title="recordSource(record)">
                  {{ recordSource(record) }}
                </div>
              </td>
              <td>
                <div class="tg-record__name" :title="record.raw_name">{{ record.raw_name || "—" }}</div>
                <div class="tg-record__hash">
                  <span v-if="episodeText(record)">{{ episodeText(record) }} · </span>
                  <span v-if="record.is_batch">整季包 · </span>{{ record.name_source || "—" }}
                </div>
              </td>
              <td>{{ record.kind_label || "—" }}</td>
              <td>
                <div
                  class="tg-record__name tg-record__name--narrow"
                  :title="record.subscription_title || ''"
                >
                  {{ record.subscription_title || "—" }}
                </div>
              </td>
              <td>{{ qualityText(record) }}</td>
              <td>
                <AdminStatusPill :tone="statusToneMap[record.status] ?? 'muted'">
                  {{ record.status_label }}
                </AdminStatusPill>
              </td>
              <td v-if="!compact">
                <div class="tg-record__name" :title="record.reason">
                  {{ record.reason || "—" }}
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <AdminEmptyState
        v-else-if="!loading"
        icon="🔍"
        title="还没有匹配记录"
        :description="compact ? '这个订阅还没匹配到任何资源。' : '确认 TG 订阅已启用、频道已添加，然后等频道有新消息。'"
      />

      <div v-if="!compact && total > pageSize" class="tg-wall__footer">
        <AppButton type="button" variant="secondary" size="sm" :disabled="page <= 1" @click="goPage(-1)">
          上一页
        </AppButton>
        <span>{{ page }} / {{ totalPages }}（共 {{ total }} 条）</span>
        <AppButton
          type="button"
          variant="secondary"
          size="sm"
          :disabled="page >= totalPages"
          @click="goPage(1)"
        >
          下一页
        </AppButton>
      </div>
    </SettingsCard>

    <AppModal :open="detailOpen" title="匹配记录详情" size="lg" @close="detailOpen = false">
      <div v-if="active" class="tg-form">
        <div class="tg-form__field">
          <label class="tg-form__label">发布名</label>
          <div class="tg-form__value">{{ active.raw_name || "—" }}</div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">状态</label>
          <div class="tg-form__value">
            <AdminStatusPill :tone="statusToneMap[active.status] ?? 'muted'">
              {{ active.status_label }}
            </AdminStatusPill>
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">判定原因</label>
          <div class="tg-form__value">{{ active.reason || "—" }}</div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">解析结果</label>
          <div class="tg-form__value">
            片名 {{ active.parsed_title || "—" }}
            <template v-if="active.parsed_year"> · 年份 {{ active.parsed_year }}</template>
            <template v-if="episodeText(active)"> · {{ episodeText(active) }}</template>
            · 画质 {{ qualityText(active) }}
            · 体积 {{ sizeText(active.size_bytes) }}
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">评分</label>
          <div class="tg-form__value">
            匹配 {{ active.match_score }} · 画质 {{ active.quality_score }}
            <template v-if="active.provider_kind"> · 通道 {{ active.provider_kind }}</template>
            <template v-if="active.retry_count"> · 已重试 {{ active.retry_count }} 次</template>
          </div>
        </div>
        <div v-if="active.offline_task_id" class="tg-form__field">
          <label class="tg-form__label">离线任务</label>
          <div class="tg-form__value tg-record__hash">{{ active.offline_task_id }}</div>
        </div>
        <div class="tg-form__field tg-form__field--stack">
          <!-- 类型可能是 ed2k / 115 分享 / 直链，不能硬编码「磁力链接」。 -->
          <label class="tg-form__label">{{ active.kind_label || "资源链接" }}</label>
          <div class="tg-form__value">
            <code class="tg-magnet">{{ active.magnet || "—" }}</code>
            <div style="margin-top: 8px">
              <AppButton type="button" variant="secondary" size="sm" @click="copyMagnet(active)">
                复制链接
              </AppButton>
            </div>
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">推送到</label>
          <div class="tg-form__value">
            <AppSelect v-model="manualSubId" :options="subscriptionOptions" :disabled="!!selectBlockedReason" />
            <p v-if="selectBlockedReason" class="tg-form__hint">{{ selectBlockedReason }}</p>
            <p v-else-if="isUnmatched" class="tg-form__hint">
              这条记录没匹配上任何订阅，系统给不出候选 —— 请指定要推送到哪一条。
            </p>
            <p v-else class="tg-form__hint">
              这里预选的是系统判定的订阅（待确认的记录给的是它的猜测），确认无误直接推送即可。
            </p>
          </div>
        </div>
      </div>

      <template #footer>
        <AppButton type="button" variant="ghost" :disabled="pushing" @click="ignore">忽略</AppButton>
        <AppButton type="button" variant="secondary" @click="detailOpen = false">关闭</AppButton>
        <!-- 暂不支持投递的记录点了必然失败，直接禁用并说明原因。 -->
        <AppButton
          type="button"
          variant="primary"
          :disabled="pushing || !!pushBlockedReason"
          :title="pushBlockedReason"
          @click="manualPush"
        >
          {{ pushing ? "提交中…" : "立即推送" }}
        </AppButton>
      </template>
    </AppModal>
  </div>
</template>
