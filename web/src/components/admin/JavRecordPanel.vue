<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppPagination from "@/components/base/AppPagination.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  deleteJavRecord,
  fetchJavPushRecordDownloaders,
  fetchJavPushRecords,
  javImageURL,
  repushJavRecord,
  setJavRecordStatus,
} from "@/api/jav";
import { toast } from "@/composables/useToast";
import type { JavPushRecord } from "@/types/jav";

/**
 * 下载记录。照源码 records.html：
 *   筛选条（起止日期 / 关键字 / 资源类型 / 下载器 / 状态）+ 表格 + 重推 / 删除。
 *
 * 没有轮询：记录的状态由离线下载完成事件回写，只有推送真的完成时才会变。
 * 轮询一个大部分时间不变的列表是白费请求，用户点一下刷新就够了。
 */
const loading = ref(false);
const items = ref<JavPushRecord[]>([]);
const total = ref(0);
const downloaders = ref<string[]>([]);
const page = ref(1);
const PAGE_SIZE = 50;

/**
 * 默认**不限日期**（全部时间）。
 *
 * 原来默认是「今天」。那是个陷阱：记录页是日志，昨天推的、甚至几小时前推的
 * （只要跨了本地零点）都会凭空消失，用户只会得出「推了却没记录」——
 * 2026-09-22 就是这么报上来的。日期选择器就在旁边，想缩范围随手一点。
 */
const filter = reactive({
  from: "",
  to: "",
  keyword: "",
  rtype: "",
  downloader: "",
  status: "",
});

const statusOptions = [
  { value: "", label: "状态 全部" },
  { value: "pushed", label: "已推送" },
  { value: "pending", label: "待推送" },
  { value: "failed", label: "失败" },
];

const rtypeOptions = [
  { value: "", label: "资源类型 全部" },
  { value: "hd", label: "高清" },
  { value: "uncensored", label: "破解" },
];

async function load() {
  loading.value = true;
  try {
    const res = await fetchJavPushRecords({
      from: filter.from,
      to: filter.to,
      keyword: filter.keyword,
      status: filter.status,
      downloader: filter.downloader,
      page: page.value,
      limit: PAGE_SIZE,
    });
    items.value = res.items ?? [];
    total.value = res.total ?? 0;

    const dl = await fetchJavPushRecordDownloaders();
    downloaders.value = dl.items ?? [];
  } catch (err) {
    toast.error(getApiErrorMessage(err, "下载记录加载失败"));
    items.value = [];
    total.value = 0;
  } finally {
    loading.value = false;
  }
}

/** 资源类型是本地筛的：它由磁链名称现算，后端没有这一列，也就没法在 SQL 里筛。 */
const filtered = ref<JavPushRecord[]>([]);

function applyLocalFilter() {
  let list = items.value;
  if (filter.rtype === "hd") list = list.filter((r) => r.hd || r.uncensored);
  if (filter.rtype === "uncensored") list = list.filter((r) => r.uncensored);
  filtered.value = list;
}

function onFilter() {
  page.value = 1;
  void load().then(applyLocalFilter);
}

function resetFilter() {
  // 与上面那个默认值保持一致：重置回**不限日期**，不是回「今天」。
  filter.from = "";
  filter.to = "";
  filter.keyword = "";
  filter.rtype = "";
  filter.downloader = "";
  filter.status = "";
  onFilter();
}

function goPage(next: number) {
  page.value = next;
  void load().then(applyLocalFilter);
}

async function repush(rec: JavPushRecord) {
  try {
    const res = await repushJavRecord(rec.id);
    toast[res.ok ? "success" : "error"](res.message || "已重新提交");
    await load();
    applyLocalFilter();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "重推失败"));
  }
}

async function markPushed(rec: JavPushRecord) {
  try {
    await setJavRecordStatus(rec.id, "pushed");
    toast.success("已标记为已推送");
    await load();
    applyLocalFilter();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "操作失败"));
  }
}

async function remove(rec: JavPushRecord) {
  if (!window.confirm("确认删除这条记录？")) return;
  try {
    await deleteJavRecord(rec.id);
    await load();
    applyLocalFilter();
  } catch (err) {
    toast.error(getApiErrorMessage(err, "删除失败"));
  }
}

const totalPages = ref(1);

onMounted(async () => {
  await load();
  totalPages.value = Math.max(1, Math.ceil(total.value / PAGE_SIZE));
  applyLocalFilter();
});
</script>

<template>
  <div>
    <div class="jav-filters">
      <AppInput v-model="filter.from" type="date" style="width: 150px" />
      <span style="color: var(--text-muted)">—</span>
      <AppInput v-model="filter.to" type="date" style="width: 150px" />
      <AppInput v-model="filter.keyword" placeholder="番号 / 片名 / 磁链" style="width: 200px" @keyup.enter="onFilter" />
      <AppSelect v-model="filter.rtype" :options="rtypeOptions" style="width: 140px" />
      <AppSelect
        v-model="filter.downloader"
        :options="[{ value: '', label: '下载器 全部' }, ...downloaders.map((d) => ({ value: d, label: d }))]"
        style="width: 170px"
      />
      <AppSelect v-model="filter.status" :options="statusOptions" style="width: 130px" />
      <AppButton size="sm" @click="onFilter">筛选</AppButton>
      <AppButton size="sm" variant="ghost" @click="resetFilter">重置</AppButton>
    </div>

    <div v-if="loading" class="jav-empty">加载中…</div>
    <div v-else-if="filtered.length === 0" class="jav-empty">
      <div class="jav-empty__icon">📋</div>
      <div>这个时间范围内没有下载记录。</div>
    </div>

    <table v-else class="jav-record-table">
      <thead>
        <tr>
          <th style="width: 46px">状态</th>
          <th style="width: 74px">封面</th>
          <th>影片</th>
          <th style="width: 110px">资源类型</th>
          <th style="width: 130px">下载时间</th>
          <th style="width: 140px">下载器</th>
          <th style="width: 150px">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="rec in filtered" :key="rec.id">
          <td>
            <span v-if="rec.status === 'pushed'" class="jav-record-status jav-record-status--ok" title="已推送">✓</span>
            <span v-else-if="rec.status === 'failed'" class="jav-record-status jav-record-status--err" title="失败">✕</span>
            <span v-else class="jav-record-status jav-record-status--pend" title="待推送">⟳</span>
          </td>
          <td>
            <img v-if="rec.cover" :src="javImageURL(rec.cover)" class="jav-record-cover" loading="lazy" :alt="rec.code" />
            <span v-else style="color: var(--text-muted); font-size: 11.5px">无</span>
          </td>
          <td>
            <div v-if="rec.code" class="jav-card__num" style="display: inline-block">{{ rec.code }}</div>
            <!-- 来源角标挂这里，不挂「资源类型」那一列 —— 那列的颜色是有语义的
                 （红=破解 / 绿=高清），来源挤进去会被读成又一个画质结论。 -->
            <span
              v-if="rec.from_comment"
              class="jav-badge"
              style="margin-left: 5px"
              title="链接来自影片评论区的用户分享，不是 JAVBUS 的磁力"
            >
              评论区
            </span>
            <div style="font-size: 12px; margin-top: 3px; word-break: break-all">
              {{ rec.title || rec.name || rec.magnet.slice(0, 50) }}
            </div>
            <!-- 失败原因要显示出来，否则用户看到的只是「失败了」三个字。 -->
            <div v-if="rec.error" class="jav-candidate__reasons">{{ rec.error }}</div>
          </td>
          <td>
            <span v-if="rec.uncensored" class="jav-badge jav-badge--break">破解</span>
            <span v-if="rec.hd" class="jav-badge jav-badge--on" style="margin-left: 4px">高清</span>
            <span v-if="!rec.hd && !rec.uncensored" class="jav-badge">普通</span>
          </td>
          <td style="font-size: 11.5px; color: var(--text-muted)">
            {{ (rec.pushed_at || rec.created_at).slice(0, 16).replace("T", " ") }}
          </td>
          <td style="font-size: 11.5px">{{ rec.downloader || "—" }}</td>
          <td>
            <div style="display: flex; gap: 5px">
              <AppButton v-if="rec.status !== 'pushed'" size="sm" variant="ghost" @click="repush(rec)">重推</AppButton>
              <AppButton v-if="rec.status !== 'pushed'" size="sm" variant="ghost" @click="markPushed(rec)">标记</AppButton>
              <AppButton size="sm" variant="ghost" @click="remove(rec)">删除</AppButton>
            </div>
          </td>
        </tr>
      </tbody>
    </table>

    <div v-if="!loading && totalPages > 1" class="jav-pagination">
      <AppPagination :page="page" :total-pages="totalPages" @update:page="goPage" />
    </div>
  </div>
</template>

<style scoped>
.jav-record-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 12.5px;
}

.jav-record-table th {
  text-align: left;
  padding: 8px 10px;
  border-bottom: 1px solid var(--border);
  color: var(--text-muted);
  font-weight: 600;
  font-size: 11.5px;
}

.jav-record-table td {
  padding: 8px 10px;
  border-bottom: 1px solid var(--border-soft);
  vertical-align: middle;
}

.jav-record-status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  font-size: 12px;
  font-weight: 700;
}

.jav-record-status--ok {
  background: rgba(16, 185, 129, 0.14);
  color: var(--success);
}
.jav-record-status--err {
  background: rgba(239, 68, 68, 0.14);
  color: var(--danger);
}
.jav-record-status--pend {
  background: rgba(245, 158, 11, 0.14);
  color: var(--warning);
}

.jav-record-cover {
  width: 60px;
  aspect-ratio: 3 / 2;
  object-fit: cover;
  border-radius: 5px;
  display: block;
}
</style>
