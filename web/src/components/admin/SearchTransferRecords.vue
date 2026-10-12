<script setup lang="ts">
/**
 * 转存记录：搜索页与转存页提交过的每一次转存都留在这里。
 *
 * 形态照盘搜那套记录页（筛选 + 批量删除 + 列设置 + 分页），但样式与控件全部换成
 * 本项目自己的（AppSelect / AppButton / AdminStatusPill / admin-table）。
 *
 * 它是「搜索转存」页的**第三个 tab**（搜索 / 转存 / 转存记录），不是弹窗。
 * 面板由外壳 v-show 保活，所以只在首次挂载拉一次；切回这一档或有新提交时
 * 由外壳调 reload()。
 *
 * 两件事与「离线下载任务列表」刻意不同：
 *   - 这里记的是**提交那一刻的留档**（链接原文、提取码、目标目录），
 *     离线任务列表只有走离线下载的那一半；
 *   - 「处理中」的记录每次打开都会按离线任务的真实状态收敛一次（后端对账）。
 */
import { computed, onMounted, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppDropdown from "@/components/base/AppDropdown.vue";
import AppPagination from "@/components/base/AppPagination.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import { getApiErrorMessage } from "@/api/client";
import { searchTransferApi } from "@/api/searchTransfer";
import { confirm } from "@/composables/useConfirm";
import { copyTextToClipboard, toast } from "@/composables/useToast";
import type { TransferRecord } from "@/types/search-transfer";

// 这是一个**面板**，不是弹窗：它挂在「搜索转存」页的第三个 tab 上。
// 没有 open/close —— 切到这一档时外壳负责触发刷新（见 reload 暴露）。
const emit = defineEmits<{ changed: [] }>();


/** 类型筛选：值即后端 kind 列。 */
const KIND_OPTIONS = [
  { value: "", label: "全部类型" },
  { value: "share_115", label: "115分享" },
  { value: "share_123", label: "123分享" },
  { value: "share_quark", label: "夸克分享" },
  { value: "share_guangya", label: "光鸭分享" },
  { value: "magnet", label: "磁力" },
  { value: "ed2k", label: "ed2k" },
  { value: "http", label: "直链" },
  { value: "ftp", label: "ftp" },
  { value: "thunder", label: "迅雷" },
];

/**
 * 状态筛选。五个值与后端一致。
 *
 * 秒传那两个态现在没有产出方（还没有秒传通道），留着是为了让筛选器从一开始
 * 就是最终形态 —— 加通道时不用回头改这里。
 */
const STATUS_OPTIONS = [
  { value: "", label: "全部状态" },
  { value: "pending", label: "处理中" },
  { value: "success", label: "保存成功" },
  { value: "failed", label: "保存失败" },
  { value: "rapid_success", label: "秒传成功" },
  { value: "rapid_failed", label: "秒传失败" },
];

const KIND_LABELS: Record<string, string> = {
  share_115: "115",
  share_123: "123",
  share_quark: "夸克",
  share_guangya: "光鸭",
  magnet: "磁力",
  ed2k: "ed2k",
  http: "直链",
  https: "直链",
  ftp: "ftp",
  thunder: "迅雷",
};

const STATUS_META: Record<string, { label: string; tone: "success" | "warning" | "brand" | "danger" | "muted" }> = {
  pending: { label: "处理中", tone: "brand" },
  success: { label: "保存成功", tone: "success" },
  failed: { label: "保存失败", tone: "danger" },
  rapid_success: { label: "秒传成功", tone: "success" },
  rapid_failed: { label: "秒传失败", tone: "danger" },
};

/** 可选列。顺序即表头顺序，与截图一致。 */
const ALL_COLUMNS = [
  { key: "id", label: "id" },
  { key: "kind", label: "类型" },
  { key: "link", label: "链接" },
  { key: "password", label: "密码" },
  { key: "share_name", label: "分享名称" },
  { key: "file_id", label: "文件id" },
  { key: "file_name", label: "文件名" },
  { key: "status", label: "状态" },
  { key: "note", label: "备注" },
  { key: "created_at", label: "创建时间" },
  { key: "updated_at", label: "更新时间" },
] as const;

type ColumnKey = (typeof ALL_COLUMNS)[number]["key"];

const COLUMN_STORAGE_KEY = "litepan:search-transfer:record-columns";

/** 默认显示哪些列 —— 与截图上的默认勾选一致。 */
const DEFAULT_COLUMNS: ColumnKey[] = ["id", "kind", "link", "share_name", "file_name", "status", "updated_at"];

function loadColumns(): ColumnKey[] {
  try {
    const raw = localStorage.getItem(COLUMN_STORAGE_KEY);
    if (!raw) return [...DEFAULT_COLUMNS];
    const parsed = JSON.parse(raw) as string[];
    const valid = parsed.filter((k): k is ColumnKey => ALL_COLUMNS.some((c) => c.key === k));
    return valid.length ? valid : [...DEFAULT_COLUMNS];
  } catch {
    return [...DEFAULT_COLUMNS];
  }
}

const visibleColumns = ref<ColumnKey[]>(loadColumns());
const visibleSet = computed(() => new Set(visibleColumns.value));

function toggleColumn(key: ColumnKey) {
  const next = new Set(visibleColumns.value);
  if (next.has(key)) {
    // 至少留一列：全关掉之后表头空了，用户会以为页面坏了。
    if (next.size <= 1) return;
    next.delete(key);
  } else {
    next.add(key);
  }
  // 按 ALL_COLUMNS 的顺序存，保证表头顺序与勾选顺序无关。
  visibleColumns.value = ALL_COLUMNS.filter((c) => next.has(c.key)).map((c) => c.key);
  try {
    localStorage.setItem(COLUMN_STORAGE_KEY, JSON.stringify(visibleColumns.value));
  } catch {
    /* 隐私模式下写不了，忽略 */
  }
}

const loading = ref(false);
const records = ref<TransferRecord[]>([]);
const total = ref(0);
const page = ref(1);
const pageSize = ref(10);
const filterKind = ref("");
const filterStatus = ref("");
const selected = ref<Set<number>>(new Set());

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)));
const allSelected = computed(
  () => records.value.length > 0 && records.value.every((r) => selected.value.has(r.id)),
);

async function load() {
  loading.value = true;
  try {
    const res = await searchTransferApi.records({
      page: page.value,
      page_size: pageSize.value,
      kind: filterKind.value,
      status: filterStatus.value,
    });
    records.value = res.items ?? [];
    total.value = res.total ?? 0;
    // 换页/换筛选之后，上一页的勾选要清掉 —— 留着会让「删除」删到看不见的行。
    selected.value = new Set();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "读取转存记录失败"));
  } finally {
    loading.value = false;
  }
}

/** 查询：回到第一页再拉，否则筛完停在空页上。 */
function applyFilter() {
  page.value = 1;
  void load();
}

function resetFilter() {
  filterKind.value = "";
  filterStatus.value = "";
  applyFilter();
}

watch(page, () => void load());
watch(pageSize, () => {
  page.value = 1;
  void load();
});

// 面板首次挂载就拉一次；之后由外壳在切到这一档、或搜索/转存页有新提交时调 reload()。
// ⚠️ **不能只在挂载时拉**：「处理中」的记录要按离线任务的真实状态收敛，
// 一直停在首次那一份会让用户看到早就完成的任务还挂着「处理中」。
onMounted(() => {
  void load();
});

// 切回这一档、或搜索/转存页有新提交时，由外壳调它重拉。
// ⚠️ 必须写在 load 之后：script setup 是**顺序执行**的，写在前面会踩 load 的 TDZ。
defineExpose({ reload: () => load() });

function toggleRow(id: number) {
  const next = new Set(selected.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selected.value = next;
}

function toggleAll() {
  selected.value = allSelected.value ? new Set() : new Set(records.value.map((r) => r.id));
}

async function removeSelected() {
  const ids = [...selected.value];
  if (!ids.length) {
    toast.warning("请先选择要删除的记录");
    return;
  }
  // ⚠️ confirm 在「取消」时是 **reject**（不是 resolve(false)），必须 try/catch，
  // 否则点取消会冒一个未捕获的 promise 错误。
  try {
    await confirm({
      title: "删除转存记录",
      message: `确定删除选中的 ${ids.length} 条记录吗？只删记录，网盘里的文件不受影响。`,
      confirmText: "删除",
      danger: true,
      icon: "warning",
    });
  } catch {
    return;
  }
  try {
    const res = await searchTransferApi.deleteRecords(ids);
    toast.success(`已删除 ${res.deleted} 条记录`);
    emit("changed");
    await load();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "删除失败"));
  }
}

function kindLabel(kind: string) {
  return KIND_LABELS[kind] || kind || "—";
}

function statusMeta(status: string) {
  return STATUS_META[status] ?? { label: status || "—", tone: "muted" as const };
}

function timeText(unix: number) {
  if (!unix) return "—";
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}/${d.getMonth() + 1}/${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

async function copyLink(rec: TransferRecord) {
  const text = rec.password ? `${rec.link} 提取码: ${rec.password}` : rec.link;
  if (await copyTextToClipboard(text)) toast.success("已复制");
  else toast.error("复制失败");
}

function shortLink(link: string) {
  return link.length > 46 ? link.slice(0, 46) + "…" : link;
}
</script>

<template>
  <div class="tr-records">
    <div class="tr-records__toolbar">
      <AppSelect v-model="filterKind" :options="KIND_OPTIONS" />
      <AppSelect v-model="filterStatus" :options="STATUS_OPTIONS" />
      <AppButton type="button" variant="primary" size="sm" @click="applyFilter">查询</AppButton>
      <AppButton type="button" variant="secondary" size="sm" @click="resetFilter">重置</AppButton>
      <AppButton
        type="button"
        variant="danger"
        size="sm"
        :disabled="!selected.size"
        @click="removeSelected"
      >
        删除{{ selected.size ? `（${selected.size}）` : "" }}
      </AppButton>

      <div class="tr-records__spacer" />

      <AppDropdown align="right" :items="[]" :min-width="180">
        <template #trigger="{ toggle }">
          <AppButton type="button" variant="secondary" size="sm" @click="toggle">列设置</AppButton>
        </template>
        <template #panel>
          <label v-for="col in ALL_COLUMNS" :key="col.key" class="tr-records__col-item">
            <input
              type="checkbox"
              :checked="visibleSet.has(col.key)"
              @change="toggleColumn(col.key)"
            />
            <span>{{ col.label }}</span>
          </label>
        </template>
      </AppDropdown>
    </div>

    <div v-if="records.length" class="admin-panel-table-wrap tr-records__table">
      <table class="admin-table">
        <thead>
          <tr>
            <th class="tr-records__check">
              <input type="checkbox" :checked="allSelected" @change="toggleAll" />
            </th>
            <th v-if="visibleSet.has('id')" class="tr-records__num">id</th>
            <th v-if="visibleSet.has('kind')">类型</th>
            <th v-if="visibleSet.has('link')">链接</th>
            <th v-if="visibleSet.has('password')">密码</th>
            <th v-if="visibleSet.has('share_name')">分享名称</th>
            <th v-if="visibleSet.has('file_id')">文件id</th>
            <th v-if="visibleSet.has('file_name')">文件名</th>
            <th v-if="visibleSet.has('status')">状态</th>
            <th v-if="visibleSet.has('note')">备注</th>
            <th v-if="visibleSet.has('created_at')">创建时间</th>
            <th v-if="visibleSet.has('updated_at')">更新时间</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="rec in records" :key="rec.id">
            <td class="tr-records__check">
              <input
                type="checkbox"
                :checked="selected.has(rec.id)"
                @change="toggleRow(rec.id)"
              />
            </td>
            <td v-if="visibleSet.has('id')" class="tr-records__num">{{ rec.id }}</td>
            <td v-if="visibleSet.has('kind')">{{ kindLabel(rec.kind) }}</td>
            <td v-if="visibleSet.has('link')">
              <button type="button" class="tr-records__link" :title="rec.link" @click="copyLink(rec)">
                {{ shortLink(rec.link) }}
              </button>
            </td>
            <td v-if="visibleSet.has('password')">{{ rec.password || "—" }}</td>
            <td v-if="visibleSet.has('share_name')">
              <div class="tr-records__ellipsis" :title="rec.share_name">{{ rec.share_name || "—" }}</div>
            </td>
            <td v-if="visibleSet.has('file_id')">{{ rec.file_id || "—" }}</td>
            <td v-if="visibleSet.has('file_name')">
              <div class="tr-records__ellipsis" :title="rec.file_name">{{ rec.file_name || "—" }}</div>
            </td>
            <td v-if="visibleSet.has('status')">
              <AdminStatusPill :tone="statusMeta(rec.status).tone">
                {{ statusMeta(rec.status).label }}
              </AdminStatusPill>
            </td>
            <td v-if="visibleSet.has('note')">
              <div class="tr-records__ellipsis" :title="rec.note">{{ rec.note || "—" }}</div>
            </td>
            <td v-if="visibleSet.has('created_at')">{{ timeText(rec.created_at) }}</td>
            <td v-if="visibleSet.has('updated_at')">{{ timeText(rec.updated_at) }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <AdminEmptyState
      v-else-if="!loading"
      icon="🗂"
      title="还没有转存记录"
      description="在「搜索」页点结果上的转存，或在「转存」页粘贴链接提交，这里就会有记录。"
    />
    <div v-else class="tr-records__loading">加载中…</div>

    <footer v-if="total > 0" class="tr-records__foot">
      <span class="tr-records__total">共 {{ total }} 条</span>
      <AppPagination :page="page" :total-pages="totalPages" @update:page="page = $event" />
      <AppSelect
        :model-value="pageSize"
        :options="[
          { value: 10, label: '10 条/页' },
          { value: 20, label: '20 条/页' },
          { value: 50, label: '50 条/页' },
        ]"
        @update:model-value="pageSize = Number($event)"
      />
    </footer>
  </div>
</template>
