<script setup lang="ts">
/**
 * 转存 tab：手工粘贴链接，存进所选网盘。
 *
 * 一行一个链接，可以是**混合类型** —— 磁力 / ed2k / http(s) / ftp / thunder 走离线下载
 * 通道，115 分享链走分享转存。两类分别提交，结果合并成一张「已提交 / 失败」清单。
 *
 * 为什么不在前端逐条拦截「这个网盘不支持」：判据在服务端（save-capabilities），
 * 前端只负责把不支持的挑出来并说明原因，真正的拒绝由两个后端接口给。
 */
import { computed, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import { searchTransferApi } from "@/api/searchTransfer";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import type { Account } from "@/api/types";
import type { SaveCapabilities } from "@/types/search-transfer";

const props = defineProps<{
  accounts: Account[];
  accountsLoading: boolean;
}>();

const accountId = ref<number | null>(null);
const targetParentId = ref("");
const targetDisplayPath = ref("");
const pickerOpen = ref(false);
const rawText = ref("");
const submitting = ref(false);
const capabilities = ref<SaveCapabilities | null>(null);

/**
 * 分享链种类 → 中文名。顺序即 chip 显示顺序。
 *
 * ⚠️ 与后端 internal/searchtransfer/service.go 的 shareMatrix **同源**，加一种分享链
 * 要一起改。这里只负责显示名，可用性一律由后端的能力表给（不在前端判）。
 */
const SHARE_LABELS: Record<string, string> = {
  "115": "115 分享",
  "123": "123 分享",
  quark: "夸克分享",
  guangya: "光鸭分享",
  baidu: "百度分享",
  "189": "天翼分享",
  "139": "移动分享",
};

/** 后端给的分享能力表，按 SHARE_LABELS 的顺序摊平成 chip 列表。 */
const shareChips = computed(() => {
  const shares = capabilities.value?.shares;
  if (!shares) return [];
  return Object.keys(SHARE_LABELS)
    .filter((kind) => kind in shares)
    .map((kind) => ({ kind, label: SHARE_LABELS[kind], cap: shares[kind] }));
});

const accountOptions = computed(() =>
  props.accounts.map((a) => ({ value: a.id, label: a.name })),
);

const targetLabel = computed(() =>
  targetDisplayPath.value ? targetDisplayPath.value : "尚未选择目录",
);

// 账号换了就重新拉能力表 —— 决定哪些链接能提交、哪些要拦下来。
watch(accountId, async (id) => {
  capabilities.value = null;
  if (!id) return;
  try {
    capabilities.value = await searchTransferApi.saveCapabilities(id);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "读取网盘保存能力失败"));
  }
});

const lines = computed(() =>
  rawText.value
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean),
);

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

interface SubmitOutcome {
  url: string;
  ok: boolean;
  message: string;
  /** 实际走的通道：builtin 时补一句「在内机下载」的说明。 */
  provider?: string;
}

const outcomes = ref<SubmitOutcome[]>([]);

async function submit() {
  if (!accountId.value) {
    toast.warning("请先选择要保存到的网盘");
    return;
  }
  if (!targetParentId.value) {
    toast.warning("请先选择保存目录");
    return;
  }
  if (!lines.value.length) {
    toast.warning("请粘贴至少一个链接");
    return;
  }
  submitting.value = true;
  outcomes.value = [];
  try {
    // 类型判定与提交**都在服务端**做（save-links 接口）—— 前端那份 classifyLink
    // 只用来在提交前把明显不支持的挑出来提示，真正的分派与拒绝理由由后端给。
    // 这样以后加一种资源类型时，只需要改服务端。
    const res = await searchTransferApi.saveLinks({
      account_id: accountId.value,
      items: lines.value.map((url) => ({ url })),
      target_parent_id: targetParentId.value,
      target_display_path: targetDisplayPath.value,
      source: "manual",
    });
    outcomes.value = (res.items ?? []).map((item) => ({
      url: item.url,
      ok: item.ok,
      // 走内置下载器时把这件事说清楚：那条路是**本机在下载**，
      // 网盘上要等它下完才看得见 —— 用户以为提交完网盘就有了。
      message: item.ok && item.provider === "builtin"
        ? `${item.message}（在内置下载器中，下完自动上传）`
        : item.message,
    }));
    if (res.failed === 0 && res.accepted > 0) toast.success(`全部提交成功（${res.accepted} 条）`);
    else if (res.accepted > 0) toast.warning(`提交完成：成功 ${res.accepted} 条，失败 ${res.failed} 条`);
    else toast.error("全部提交失败，见下方清单");
  } catch (error) {
    // 整批失败（账号没选、服务未就绪、网络错误…）：逐条标出来，别只弹一个 toast ——
    // 用户在这一页等的是「每条到底去哪了」。
    const msg = getApiErrorMessage(error, "提交失败");
    outcomes.value = lines.value.map((url) => ({ url, ok: false, message: msg }));
    toast.error(msg);
  } finally {
    submitting.value = false;
  }
}

const okCount = computed(() => outcomes.value.filter((o) => o.ok).length);
</script>

<template>
  <div class="st-transfer">
    <section class="st-card">
      <div class="st-card__head">
        <h3>保存到</h3>
        <p class="st-hint">
          选好网盘与目录后，粘贴的链接会按类型自动分流：磁力 / ed2k / 直链走离线下载，
          115、123、夸克、光鸭、百度、天翼分享链走转存。
          <span class="st-steps__note">
            没有原生离线下载能力的网盘（夸克、天翼、移动等），磁力 / 直链会走**内置下载器**
            （本机下载完自动上传），网盘上要等它下完才看得见。
          </span>
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

      <label class="st-field">
        <span class="st-field__label">链接（一行一个，可混合类型）</span>
        <textarea
          v-model="rawText"
          class="st-textarea"
          rows="6"
          placeholder="magnet:?xt=urn:btih:…&#10;ed2k://|file|…&#10;https://115.com/s/xxxx?password=abcd&#10;https://123865.com/s/xxxx?pwd=abcd&#10;https://pan.quark.cn/s/xxxx&#10;https://pan.baidu.com/s/1xxxx?pwd=abcd&#10;https://cloud.189.cn/t/xxxx&#10;https://example.com/movie.mkv"
        ></textarea>
      </label>

      <div v-if="capabilities" class="st-caps">
        <span class="st-caps__title">该账号当前支持：</span>
        <span
          v-for="(cap, scheme) in capabilities.schemes"
          :key="scheme"
          class="st-chip"
          :class="{ 'st-chip--off': !cap.ok }"
          :title="cap.ok ? `走${cap.provider === 'builtin' ? '内置下载器' : '网盘离线下载'}` : cap.reason"
        >
          {{ scheme }}
        </span>
        <span
          v-for="chip in shareChips"
          :key="chip.kind"
          class="st-chip"
          :class="{ 'st-chip--off': !chip.cap.ok }"
          :title="chip.cap.ok ? '走分享转存' : chip.cap.reason"
        >
          {{ chip.label }}
        </span>
      </div>

      <div class="st-actions">
        <AppButton type="button" variant="primary" :disabled="submitting" @click="submit">
          {{ submitting ? "提交中…" : `提交 ${lines.length} 条` }}
        </AppButton>
        <AppButton type="button" variant="ghost" :disabled="submitting" @click="rawText = ''">
          清空
        </AppButton>
      </div>
    </section>

    <section v-if="outcomes.length" class="st-card">
      <div class="st-card__head">
        <h3>提交结果</h3>
        <p class="st-hint">成功 {{ okCount }} 条，失败 {{ outcomes.length - okCount }} 条</p>
      </div>
      <ul class="st-outcomes">
        <li v-for="(o, i) in outcomes" :key="i" :class="o.ok ? 'is-ok' : 'is-bad'">
          <span class="st-outcomes__mark">{{ o.ok ? "✓" : "✕" }}</span>
          <div class="st-outcomes__body">
            <div class="st-outcomes__url" :title="o.url">{{ o.url }}</div>
            <div class="st-outcomes__msg">{{ o.message }}</div>
          </div>
        </li>
      </ul>
    </section>

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
