<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from "vue";
import { coverExtractApi, type CoverFile, type CoverFrame, type CoverRuntime, type CoverStyle } from "@/api/coverExtract";
import { getApiErrorMessage } from "@/api/client";
import { toast } from "@/composables/useToast";
import { canvasToJPEG, createCoverPoster, type CoverPosterFocus } from "@/utils/coverPoster";
import AppButton from "@/components/base/AppButton.vue";
import AppModal from "@/components/base/AppModal.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import CloudToolCard from "@/components/admin/CloudToolCard.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import { useAccountsStore } from "@/stores/accounts";

type CaptureMode = "head" | "random" | "timestamp";

const props = withDefaults(defineProps<{ searchQuery?: string }>(), { searchQuery: "" });
const open = ref(false);
const loading = ref(false);
const downloading = ref(false);
const saving = ref(false);
const previewing = ref(false);
const toggleSaving = ref(false);
const targetPickerOpen = ref(false);
const helpOpen = ref(false);
// 默认只取片头 1 秒的一帧，需要更多候选时再由用户选择随机三帧。
const captureMode = ref<CaptureMode>("head");
const files = ref<CoverFile[]>([]);
const runtime = ref<CoverRuntime | null>(null);
const globalStyle = ref<CoverStyle>({ shape: "slant", height: 0.22, panel_color: "#3C4CC3", opacity: 0.8, text_color: "#fffdf8", packaged: false });
const activeID = ref("");
const selectedFrame = ref("");
const timeHour = ref(0);
const timeMinute = ref(0);
const timeSecond = ref(0);
const statusText = ref("");
const previewError = ref("");
const posterCanvas = ref<HTMLCanvasElement | null>(null);
const stylePanelOpen = ref(false);
const filesPage = ref(1);
const framesPage = ref(1);
const PAGE_SIZE = 6;
const candListEl = ref<HTMLElement | null>(null);
const filesListEl = ref<HTMLElement | null>(null);
const filePageSize = ref(PAGE_SIZE);
const candPageSize = ref(PAGE_SIZE);
const narrow = ref(window.innerWidth <= 900);
const adjustMode = ref(false);
const colorPickerFor = ref<"" | "panel" | "text">("");
const paletteColors = [
  "#000000", "#ffffff", "#3C4CC3", "#2563eb", "#0ea5e9", "#10b981", "#f59e0b", "#ef4444",
  "#8b5cf6", "#ec4899", "#64748b", "#92400e", "#065f46", "#1e293b", "#f1f5f9", "#fffdf8",
];
const titles = ref<Record<string, string>>({});
const packaged = ref<Record<string, boolean>>({});
const panelColors = ref<Record<string, string>>({});
const panelOpacities = ref<Record<string, number>>({});
const textColors = ref<Record<string, string>>({});
const panelShapes = ref<Record<string, "slant" | "straight">>({});
const panelHeights = ref<Record<string, number>>({});
const imageZooms = ref<Record<string, number>>({});
const frameFocuses = ref<Record<string, CoverPosterFocus>>({});
const draggingPreview = ref(false);
let previewTicket = 0;
let previewAnimationFrame = 0;
let dragState: {
  pointerID: number;
  frameID: string;
  startX: number;
  startY: number;
  startFocus: CoverPosterFocus;
} | null = null;

const active = computed(() => files.value.find((file) => file.id === activeID.value) ?? files.value[0]);
const enabled = computed(() => runtime.value?.enabled ?? false);
const accountsStore = useAccountsStore();
const accountName = computed(() => {
  if (!active.value) return "";
  return accountsStore.accounts.find((a) => a.id === active.value!.account_id)?.name ?? "";
});
const targetDisplay = computed(() => {
  const path = active.value ? `${active.value.target_path === "/" ? "" : active.value.target_path}/poster.jpg` : "/poster.jpg";
  // 前缀账号名，让用户知道封面保存到哪个网盘
  const name = accountName.value ? `${accountName.value} ` : "";
  return `${name}${path}`;
});
const visible = computed(() => !props.searchQuery.trim() || "视频海报生成封面提取".includes(props.searchQuery.trim()));
const selectedFrameInfo = computed(() => active.value?.frames.find((frame) => frame.id === selectedFrame.value));
const activeTitle = computed({
  get: () => active.value ? (titles.value[active.value.id] ?? inferTitle(active.value)) : "",
  set: (value: string) => {
    if (active.value) titles.value[active.value.id] = value;
  },
});
const activePackaged = computed({
  get: () => active.value ? (packaged.value[active.value.id] ?? globalStyle.value.packaged) : false,
  set: (value: boolean) => {
    if (active.value) packaged.value[active.value.id] = value;
  },
});
const activePanelColor = computed({
  get: () => active.value ? (panelColors.value[active.value.id] ?? globalStyle.value.panel_color) : "#000000",
  set: (value: string) => {
    if (active.value) panelColors.value[active.value.id] = value;
  },
});
const activePanelOpacity = computed({
  get: () => active.value ? (panelOpacities.value[active.value.id] ?? globalStyle.value.opacity) : 0.8,
  set: (value: number) => {
    if (active.value) panelOpacities.value[active.value.id] = Number(value);
  },
});
const activeTextColor = computed({
  get: () => active.value ? (textColors.value[active.value.id] ?? globalStyle.value.text_color) : "#fffdf8",
  set: (value: string) => {
    if (active.value) textColors.value[active.value.id] = value;
  },
});
const activePanelShape = computed({
  get: () => active.value ? (panelShapes.value[active.value.id] ?? globalStyle.value.shape) : "slant",
  set: (value: "slant" | "straight") => {
    if (active.value) panelShapes.value[active.value.id] = value;
  },
});
const activePanelHeight = computed({
  get: () => active.value ? (panelHeights.value[active.value.id] ?? globalStyle.value.height) : 0.22,
  set: (value: number) => {
    if (active.value) panelHeights.value[active.value.id] = Number(value);
  },
});
const activeImageZoom = computed({
  get: () => active.value ? (imageZooms.value[active.value.id] ?? 1) : 1,
  set: (value: number) => {
    if (active.value) imageZooms.value[active.value.id] = Number(value);
  },
});
const captureActionLabel = computed(() => {
  return "取帧";
});
const zoomPercent = computed(() => `${Math.round(activeImageZoom.value * 100)}%`);
const previewDragTitle = computed(() => (narrow.value ? "拖动调整画面位置，可点复位恢复居中" : "拖动调整画面位置，双击恢复居中"));
const adjustHintText = computed(() => (narrow.value ? "拖动画面调整位置 · 可点复位恢复居中" : "拖动画面调整位置 · 完成或双击复位"));
const pagedFiles = computed(() => {
  const size = filePageSize.value;
  const start = (filesPage.value - 1) * size;
  return files.value.slice(start, start + size);
});
const pagedFrames = computed(() => {
  if (!active.value) return [];
  const size = candPageSize.value;
  const start = (framesPage.value - 1) * size;
  return active.value.frames.slice(start, start + size);
});
const filesPageCount = computed(() => Math.max(1, Math.ceil(files.value.length / filePageSize.value)));
const framesPageCount = computed(() => Math.max(1, Math.ceil((active.value?.frames.length ?? 0) / candPageSize.value)));

function measureFilePageSize() {
  if (!narrow.value) {
    if (filePageSize.value !== PAGE_SIZE) filePageSize.value = PAGE_SIZE;
    return;
  }
  const el = filesListEl.value;
  if (!el) return;
  const first = el.querySelector<HTMLElement>(".c-file");
  if (!first) {
    if (filePageSize.value !== 3) filePageSize.value = 3;
    return;
  }
  const gap = 5;
  const itemHeight = first.getBoundingClientRect().height;
  if (itemHeight <= 0) return;
  const rows = Math.max(1, Math.floor((el.clientHeight + gap) / (itemHeight + gap)));
  if (rows !== filePageSize.value) filePageSize.value = rows;
}

// 候选画面分页自适应：按右栏可视高度计算每页行数（2 列），保证每页正好填满、不滚动不浪费。
function measureCandPageSize() {
  const el = candListEl.value;
  if (!el) return;
  if (narrow.value) {
    // 小屏：固定 2 列 1 行，每页 2 张
    if (candPageSize.value !== 2) {
      candPageSize.value = 2;
      const count = active.value?.frames.length ?? 0;
      framesPage.value = Math.min(framesPage.value, Math.max(1, Math.ceil(count / 2)));
    }
    return;
  }
  const width = el.clientWidth;
  const height = el.clientHeight;
  if (width <= 0 || height <= 0) {
    // 布局尚未稳定（初始渲染高度为 0），下一帧重试
    requestAnimationFrame(() => measureCandPageSize());
    return;
  }
  const gap = 8;
  const itemWidth = (width - gap) / 2;
  const itemHeight = itemWidth * 0.75;
  const rows = Math.max(1, Math.floor((height + gap) / (itemHeight + gap)));
  const size = rows * 2;
  if (size !== candPageSize.value) {
    candPageSize.value = size;
    const count = active.value?.frames.length ?? 0;
    framesPage.value = Math.min(framesPage.value, Math.max(1, Math.ceil(count / size)));
  }
}
let candObserver: ResizeObserver | null = null;
function ensureCandObserver() {
  if (candObserver || !candListEl.value) return;
  candObserver = new ResizeObserver(() => measureCandPageSize());
  candObserver.observe(candListEl.value);
}

function fmtDuration(ms?: number) {
  if (!ms) return "待提取";
  const seconds = Math.floor(ms / 1000);
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remain = seconds % 60;
  return hours ? `${hours}:${String(minutes).padStart(2, "0")}:${String(remain).padStart(2, "0")}` : `${minutes}:${String(remain).padStart(2, "0")}`;
}

function fmtTimestamp(ms: number) {
  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  return `${minutes}:${String(seconds % 60).padStart(2, "0")}`;
}

function inferTitle(file: CoverFile) {
  const targetName = file.target_path.split("/").filter(Boolean).at(-1)?.trim() ?? "";
  const genericDirectories = new Set(["电影", "电视剧", "剧集", "短剧", "视频", "movies", "movie", "tv"]);
  if (targetName && !genericDirectories.has(targetName.toLowerCase())) return targetName;
  const stem = file.name.replace(/\.[^.]+$/, "").replace(/[._]+/g, " ").trim();
  return stem
    .replace(/\s+(?:S\d{1,2}E\d{1,3}|E\d{1,3}|2160p|1080p|720p|WEB[- .]?DL|BluRay|HDTV)\b.*$/i, "")
    .trim() || stem;
}

function ensureFileOptions(file: CoverFile) {
  // 仅初始化片名；样式项不预写，未单独调整的文件实时读取全局默认样式
  if (!(file.id in titles.value)) titles.value[file.id] = inferTitle(file);
}

function frameFocus(frameID = selectedFrame.value): CoverPosterFocus {
  return frameFocuses.value[frameID] ?? { x: 0.5, y: 0.5 };
}

function clampFocus(value: number) {
  return Math.min(1, Math.max(0, value));
}

function schedulePreview() {
  if (previewAnimationFrame) return;
  previewAnimationFrame = window.requestAnimationFrame(() => {
    previewAnimationFrame = 0;
    void refreshPreview(true);
  });
}

function setFrameFocus(frameID: string, focus: CoverPosterFocus, immediate = false) {
  frameFocuses.value = { ...frameFocuses.value, [frameID]: focus };
  if (immediate) void refreshPreview();
  else schedulePreview();
}

function normalizeFiles(list: CoverFile[]) {
  return (list ?? []).map((file) => {
    const normalized = { ...file, frames: file.frames ?? [] };
    ensureFileOptions(normalized);
    return normalized;
  });
}

function ensureActiveSelection() {
  const current = active.value;
  if (!current) {
    selectedFrame.value = "";
    return;
  }
  if (!current.frames.some((frame) => frame.id === selectedFrame.value)) {
    selectedFrame.value = current.frames[0]?.id ?? "";
  }
}

async function load() {
  try {
    const [list, rt, style] = await Promise.all([coverExtractApi.files(), coverExtractApi.runtime(), coverExtractApi.getStyle()]);
    globalStyle.value = style;
    files.value = normalizeFiles(list.files ?? []);
    runtime.value = rt;
    if (!files.value.some((file) => file.id === activeID.value)) activeID.value = files.value[0]?.id ?? "";
    ensureActiveSelection();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载视频海报生成工具失败"));
  }
}

async function show() {
  open.value = true;
  await load();
}

async function toggleEnabled() {
  toggleSaving.value = true;
  try {
    runtime.value = await coverExtractApi.setEnabled(!enabled.value);
    toast.success(runtime.value.enabled ? "已启用视频海报生成，视频右键菜单已开放" : "已停用视频海报生成，视频右键菜单已隐藏");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "修改开关失败"));
  } finally {
    toggleSaving.value = false;
  }
}

async function downloadTool() {
  downloading.value = true;
  statusText.value = "正在下载、校验并安装 FFmpeg…";
  try {
    runtime.value = await coverExtractApi.download();
    toast.success("FFmpeg 安装完成");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "FFmpeg 安装失败"));
  } finally {
    downloading.value = false;
    statusText.value = "";
  }
}

async function extract(mode: CaptureMode) {
  if (!active.value) return;
  const sessionID = active.value.id;
  const previousFrames = new Set(active.value.frames.map((frame) => frame.id));
  loading.value = true;
  try {
    const timestampMs = (timeHour.value * 3600 + timeMinute.value * 60 + timeSecond.value) * 1000;
    // 先取时长（probe 只探测不取帧；时长缓存后逐帧提取不再重复探测）
    statusText.value = "正在读取视频信息…";
    const info = await coverExtractApi.extract({ session_file_id: sessionID, mode: "probe" });
    const duration = info.duration_ms ?? 0;
    // 计算取帧时间点
    let points: number[] = [];
    if (mode === "head") {
      points = [1000];
    } else if (mode === "random") {
      points = randomFramePoints(duration, 3);
    } else {
      points = [timestampMs];
    }
    if (!points.length || points.some((p) => p < 0 || p >= duration)) {
      toast.error("无法计算有效的取帧时间点");
      return;
    }
    // 逐帧提取，实时显示进度；approx=true 走关键帧极速路径，单帧失败快速跳过
    let latest: CoverFile | null = null;
    let succeeded = 0;
    for (let i = 0; i < points.length; i++) {
      statusText.value = `正在提取第 ${i + 1}/${points.length} 帧…`;
      try {
        const out = await coverExtractApi.extract({
          session_file_id: sessionID,
          mode: "timestamp",
          timestamp_ms: points[i],
          approx: mode !== "timestamp",
        });
        ensureFileOptions(out);
        latest = out;
        succeeded++;
      } catch {
        // 单帧失败跳过，继续下一帧
      }
    }
    if (latest) {
      files.value = files.value.map((file) => file.id === latest!.id ? { ...latest!, frames: latest!.frames ?? [] } : file);
      const newFrames = (latest.frames ?? []).filter((frame) => !previousFrames.has(frame.id));
      selectedFrame.value = newFrames[0]?.id ?? latest.frames[0]?.id ?? "";
    }
    statusText.value = "";
    toast.success(succeeded > 0 ? `已生成 ${succeeded} 张候选图` : "未能提取到候选画面");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "提取失败"));
    await load();
    statusText.value = "";
  } finally {
    loading.value = false;
  }
}

// 随机取帧点：避开片头片尾两端，在 10%~90% 区间按 count 段各随机一点（与后端一致）。
function randomFramePoints(duration: number, count: number): number[] {
  const start = Math.floor(duration / 10);
  const span = Math.floor((duration * 8) / 10);
  const points: number[] = [];
  for (let i = 0; i < count; i++) {
    const left = start + Math.floor((span * i) / count);
    const right = start + Math.floor((span * (i + 1)) / count);
    const width = right - left;
    const offset = width > 1 ? Math.floor(Math.random() * width) : 0;
    const ts = Math.min(left + offset, duration - 1);
    if (!points.length || ts !== points[points.length - 1]) points.push(ts);
  }
  return points;
}

async function buildPoster() {
  if (!selectedFrameInfo.value) throw new Error("请先选择候选画面");
  return createCoverPoster({
    imageURL: coverExtractApi.imageURL(selectedFrameInfo.value.id),
    title: activeTitle.value,
    packaged: activePackaged.value,
    focus: frameFocus(selectedFrameInfo.value.id),
    panelColor: activePanelColor.value,
    panelOpacity: activePanelOpacity.value,
    textColor: activeTextColor.value,
    panelShape: activePanelShape.value,
    panelHeight: activePanelHeight.value,
    imageZoom: activeImageZoom.value,
  });
}

function startPreviewDrag(event: PointerEvent) {
  // 小屏：仅「调整画面」模式下拦截拖动，其余情况交给页面滚动
  if (narrow.value && !adjustMode.value) return;
  if (event.button !== 0 || previewing.value || !selectedFrameInfo.value) return;
  const element = event.currentTarget as HTMLElement;
  dragState = {
    pointerID: event.pointerId,
    frameID: selectedFrameInfo.value.id,
    startX: event.clientX,
    startY: event.clientY,
    startFocus: { ...frameFocus(selectedFrameInfo.value.id) },
  };
  draggingPreview.value = true;
  element.setPointerCapture(event.pointerId);
  event.preventDefault();
}

function movePreviewDrag(event: PointerEvent) {
  if (!dragState || dragState.pointerID !== event.pointerId) return;
  const element = event.currentTarget as HTMLElement;
  const bounds = element.getBoundingClientRect();
  if (!bounds.width || !bounds.height) return;
  setFrameFocus(dragState.frameID, {
    x: clampFocus(dragState.startFocus.x - (event.clientX - dragState.startX) / bounds.width),
    y: clampFocus(dragState.startFocus.y - (event.clientY - dragState.startY) / bounds.height),
  });
  event.preventDefault();
}

function finishPreviewDrag(event: PointerEvent) {
  if (!dragState || dragState.pointerID !== event.pointerId) return;
  const element = event.currentTarget as HTMLElement;
  if (element.hasPointerCapture(event.pointerId)) element.releasePointerCapture(event.pointerId);
  dragState = null;
  draggingPreview.value = false;
}

function resetPreviewFocus() {
  if (!selectedFrameInfo.value) return;
  setFrameFocus(selectedFrameInfo.value.id, { x: 0.5, y: 0.5 }, true);
}

function toggleAdjust() {
  adjustMode.value = !adjustMode.value;
}

function toggleColorPicker(target: "panel" | "text") {
  colorPickerFor.value = colorPickerFor.value === target ? "" : target;
}

function pickColor(color: string) {
  if (colorPickerFor.value === "panel") activePanelColor.value = color;
  else if (colorPickerFor.value === "text") activeTextColor.value = color;
  colorPickerFor.value = "";
}

async function refreshPreview(silent = false) {
  const ticket = ++previewTicket;
  previewError.value = "";
  if (!open.value || !selectedFrameInfo.value) return;
  if (!silent) previewing.value = true;
  try {
    const rendered = await buildPoster();
    if (ticket !== previewTicket) return;
    await nextTick();
    const canvas = posterCanvas.value;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;
    canvas.width = rendered.width;
    canvas.height = rendered.height;
    ctx.drawImage(rendered, 0, 0);
  } catch (error) {
    if (ticket === previewTicket) previewError.value = error instanceof Error ? error.message : "海报预览生成失败";
  } finally {
    if (ticket === previewTicket && !silent) previewing.value = false;
  }
}

async function save() {
  if (!active.value || !selectedFrame.value) return;
  if (activePackaged.value && !activeTitle.value.trim()) {
    toast.error("请先填写海报片名");
    return;
  }
  saving.value = true;
  statusText.value = "正在合成 1000×1500 海报…";
  try {
    const canvas = await buildPoster();
    const blob = await canvasToJPEG(canvas);
    statusText.value = `正在保存到 ${targetDisplay.value}…`;
    const payload = { session_file_id: active.value.id, frame_id: selectedFrame.value, overwrite: false };
    let out = await coverExtractApi.saveComposed(payload, blob);
    if (out.conflict) {
      if (!window.confirm(`${out.filename} 已存在，确定覆盖吗？`)) {
        statusText.value = "";
        toast.info("已取消保存");
        return;
      }
      out = await coverExtractApi.saveComposed({ ...payload, overwrite: true }, blob);
    }
    if (out.ok) {
      toast.success(`已保存到 ${targetDisplay.value}`);
      statusText.value = "";
    }
  } catch (error) {
    toast.error(getApiErrorMessage(error, error instanceof Error ? error.message : "保存封面失败"));
    statusText.value = "";
  } finally {
    saving.value = false;
  }
}

async function remove(id: string) {
  try {
    const removedFrameIDs = files.value.find((file) => file.id === id)?.frames.map((frame) => frame.id) ?? [];
    await coverExtractApi.remove(id);
    files.value = files.value.filter((file) => file.id !== id);
    const nextTitles = { ...titles.value };
    const nextPackaged = { ...packaged.value };
    const nextPanelColors = { ...panelColors.value };
    const nextPanelOpacities = { ...panelOpacities.value };
    const nextTextColors = { ...textColors.value };
    const nextPanelShapes = { ...panelShapes.value };
    const nextPanelHeights = { ...panelHeights.value };
    const nextImageZooms = { ...imageZooms.value };
    delete nextTitles[id];
    delete nextPackaged[id];
    delete nextPanelColors[id];
    delete nextPanelOpacities[id];
    delete nextTextColors[id];
    delete nextPanelShapes[id];
    delete nextPanelHeights[id];
    delete nextImageZooms[id];
    titles.value = nextTitles;
    packaged.value = nextPackaged;
    panelColors.value = nextPanelColors;
    panelOpacities.value = nextPanelOpacities;
    textColors.value = nextTextColors;
    panelShapes.value = nextPanelShapes;
    panelHeights.value = nextPanelHeights;
    imageZooms.value = nextImageZooms;
    if (removedFrameIDs.length) {
      const nextFocuses = { ...frameFocuses.value };
      removedFrameIDs.forEach((frameID) => delete nextFocuses[frameID]);
      frameFocuses.value = nextFocuses;
    }
    if (activeID.value === id) activeID.value = files.value[0]?.id ?? "";
    ensureActiveSelection();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "移除失败"));
  }
}

async function clearAll() {
  try {
    await coverExtractApi.clear();
    files.value = [];
    activeID.value = "";
    selectedFrame.value = "";
    toast.success("已清空列表");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "清空列表失败"));
  }
}

async function setTarget(payload: { parentId: string; path: string }) {
  if (!active.value) return;
  try {
    const out = await coverExtractApi.setTarget(active.value.id, { parent_id: payload.parentId, path: payload.path || "/" });
    files.value = files.value.map((file) => file.id === out.id ? { ...out, frames: out.frames ?? [] } : file);
    targetPickerOpen.value = false;
  } catch (error) {
    toast.error(getApiErrorMessage(error, "修改封面保存目录失败"));
  }
}

function openTargetPicker() {
  if (!active.value || loading.value || saving.value) return;
  targetPickerOpen.value = true;
}

function select(file: CoverFile) {
  activeID.value = file.id;
  selectedFrame.value = file.frames[0]?.id ?? "";
}

function choose(frame: CoverFrame) {
  selectedFrame.value = frame.id;
}

async function removeFrame(frameID: string) {
  if (!active.value) return;
  const id = active.value.id;
  try {
    await coverExtractApi.removeFrame(id, frameID);
    files.value = files.value.map((file) => file.id === id ? { ...file, frames: file.frames.filter((frame) => frame.id !== frameID) } : file);
    if (selectedFrame.value === frameID) selectedFrame.value = active.value?.frames[0]?.id ?? "";
    const count = active.value?.frames.length ?? 0;
    framesPage.value = Math.min(framesPage.value, Math.max(1, Math.ceil(count / candPageSize.value)));
  } catch (error) {
    toast.error(getApiErrorMessage(error, "移除候选画面失败"));
  }
}

function toggleStylePanel() {
  stylePanelOpen.value = !stylePanelOpen.value;
}

function onPackagedToggle(event: Event) {
  // 打开包装海报时自动弹出样式面板，方便立即调整
  if ((event.target as HTMLInputElement).checked) stylePanelOpen.value = true;
}

async function saveAsDefault() {
  try {
    await coverExtractApi.saveStyle({
      shape: activePanelShape.value,
      height: activePanelHeight.value,
      panel_color: activePanelColor.value,
      opacity: activePanelOpacity.value,
      text_color: activeTextColor.value,
      packaged: activePackaged.value,
    });
    globalStyle.value = {
      shape: activePanelShape.value,
      height: activePanelHeight.value,
      panel_color: activePanelColor.value,
      opacity: activePanelOpacity.value,
      text_color: activeTextColor.value,
      packaged: activePackaged.value,
    };
    toast.success("已保存为默认样式，之后加入的视频将默认使用此样式");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存默认样式失败"));
  }
}

function onWindowClick() {
  stylePanelOpen.value = false;
  colorPickerFor.value = "";
}

function onResize() {
  narrow.value = window.innerWidth <= 900;
}

watch([selectedFrame, activeTitle, activePackaged, activePanelColor, activePanelOpacity, activeTextColor, activePanelShape, activePanelHeight, activeImageZoom, open], () => void refreshPreview(), { flush: "post" });
watch(files, (list) => {
  void nextTick(() => {
    measureFilePageSize();
    filesPage.value = Math.min(filesPage.value, Math.max(1, Math.ceil(list.length / filePageSize.value)));
  });
});
watch([narrow, open], () => {
  void nextTick(() => {
    measureFilePageSize();
    const count = files.value.length;
    filesPage.value = Math.min(filesPage.value, Math.max(1, Math.ceil(count / filePageSize.value)));
    ensureCandObserver();
    measureCandPageSize();
  });
});
watch(() => active.value?.id, () => {
  framesPage.value = 1;
  void nextTick(() => {
    ensureCandObserver();
    measureCandPageSize();
  });
});
watch(() => active.value?.frames.length, (count) => {
  framesPage.value = Math.min(framesPage.value, Math.max(1, Math.ceil((count ?? 0) / candPageSize.value)));
  if ((count ?? 0) > 0) {
    void nextTick(() => {
      ensureCandObserver();
      measureCandPageSize();
    });
  }
});
onMounted(() => {
  window.addEventListener("click", onWindowClick);
  window.addEventListener("resize", onResize);
  void accountsStore.loadAccounts();
  void load();
});
onUnmounted(() => {
  candObserver?.disconnect();
  candObserver = null;
  window.removeEventListener("click", onWindowClick);
  window.removeEventListener("resize", onResize);
  if (previewAnimationFrame) window.cancelAnimationFrame(previewAnimationFrame);
});
</script>

<template>
  <CloudToolCard v-show="visible" :enabled="enabled" name="视频海报生成" driver="视频截图· 保存到网盘" logo-src="/logos/CoverExtract.png" logo-alt="视频海报生成" :tags="[{ label: '实验性', variant: 'warn' }]" :stat-value="files.length" stat-label="个待处理视频">
    适用于 TMDB 未收录的视频（如生活日常、自己拍摄的小文件），可为 MP4 / MKV 格式生成海报。
    <template #toggle><button class="check-toggle" type="button" :class="{ on: enabled }" :aria-label="enabled ? '停用视频海报生成' : '启用视频海报生成'" :disabled="toggleSaving" @click="toggleEnabled"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M3.5 8.5 6.5 11.5 12.5 4.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg></button></template>
    <template #actions><AppButton size="sm" @click="show">打开工具</AppButton></template>
  </CloudToolCard>

  <AppModal :open="open" bare @close="open = false">
    <div class="cover-shell">
      <!-- 顶栏 -->
      <div class="c-top">
        <div class="c-title-block">
          <h1 class="c-title">视频海报生成</h1>
          <button class="c-help" type="button" title="取帧失败原因说明" aria-label="取帧失败原因说明" @click="helpOpen = true">?</button>
        </div>
        <div class="c-spacer" />
        <!-- 时分秒排在取帧切换左侧、紧挨右对齐 -->
        <div v-if="captureMode === 'timestamp'" class="time-input" @click.stop>
          <input v-model.number="timeHour" aria-label="时" type="number" min="0"><span>时</span>
          <input v-model.number="timeMinute" aria-label="分" type="number" min="0" max="59"><span>分</span>
          <input v-model.number="timeSecond" aria-label="秒" type="number" min="0" max="59"><span>秒</span>
        </div>
        <div class="seg">
          <button type="button" :class="{ on: captureMode === 'head' }" @click="captureMode = 'head'">片头取帧</button>
          <button type="button" :class="{ on: captureMode === 'random' }" @click="captureMode = 'random'">随机三帧</button>
          <button type="button" :class="{ on: captureMode === 'timestamp' }" @click="captureMode = 'timestamp'">按时间</button>
        </div>
        <button class="c-extract" type="button" :disabled="loading || !enabled || !runtime?.ready || !active" @click="extract(captureMode)">{{ loading ? "取帧中…" : captureActionLabel }}</button>
        <button class="c-close" type="button" aria-label="关闭" @click="open = false">✕</button>
      </div>

      <!-- ffmpeg 未就绪 -->
      <div v-if="runtime && !runtime.ready" class="cover-warning">
        {{ runtime.error }}<br>请将组件放到 {{ runtime.manual_path }}
        <AppButton v-if="runtime.auto_download_available" size="sm" :disabled="downloading" @click="downloadTool">{{ downloading ? "安装中…" : "自动安装" }}</AppButton>
      </div>

      <!-- 主体：三栏 -->
      <div class="c-layout">
        <aside class="c-files">
          <div class="c-files-cap"><b>待处理影片</b><button type="button" class="c-files-clear" @click="clearAll">清空</button></div>
          <div v-if="files.length" ref="filesListEl" class="c-files-list">
            <div v-for="file in pagedFiles" :key="file.id" class="c-file" :class="{ active: file.id === active?.id }" @click="select(file)">
              <span class="c-file-dot" />
              <span class="c-file-info">
                <b>{{ file.name }}</b>
                <small>{{ fmtDuration(file.duration_ms) }} · {{ file.frames.length }} 张候选</small>
              </span>
              <button type="button" class="c-file-rm" title="移除" @click.stop="remove(file.id)">✕</button>
            </div>
          </div>
          <div v-else class="c-file-empty">
            <span class="c-file-empty__icon" aria-hidden="true">🎬</span>
            <b class="c-file-empty__title">还没有待处理影片</b>
            <ol class="c-file-empty__steps">
              <li data-step="1">回到首页文件列表</li>
              <li data-step="2">右键视频文件（MP4 / MKV）</li>
              <li data-step="3">选择「生成视频海报」发送过来</li>
            </ol>
          </div>
          <div v-if="filesPageCount > 1" class="c-pager">
            <button type="button" :disabled="filesPage <= 1" @click="filesPage--">‹</button>
            <span>{{ filesPage }} / {{ filesPageCount }}</span>
            <button type="button" :disabled="filesPage >= filesPageCount" @click="filesPage++">›</button>
          </div>
        </aside>

        <section class="c-stage">
          <div
            v-if="active && active.frames.length"
            class="preview-wrap"
            :class="{ dragging: draggingPreview, 'adjust-mode': narrow && adjustMode }"
            :title="previewDragTitle"
            @pointerdown="startPreviewDrag"
            @pointermove="movePreviewDrag"
            @pointerup="finishPreviewDrag"
            @pointercancel="finishPreviewDrag"
            @dblclick.prevent="resetPreviewFocus"
          >
            <canvas ref="posterCanvas" :class="{ loading: previewing }" />
            <button v-if="narrow" type="button" class="adjust-btn" :class="{ active: adjustMode }" @click.stop="toggleAdjust">{{ adjustMode ? "完成" : "调整" }}</button>
            <button v-if="narrow && adjustMode" type="button" class="adjust-reset-btn" @click.stop="resetPreviewFocus">复位</button>
            <span v-if="adjustMode" class="adjust-hint">{{ adjustHintText }}</span>
            <label class="zoom" title="放大画面后可拖动调整位置" @pointerdown.stop @click.stop @dblclick.stop>
              <span>{{ zoomPercent }}</span>
              <input v-model.number="activeImageZoom" aria-label="画面缩放" type="range" min="1" max="1.5" step="0.01">
            </label>
            <span v-if="loading || downloading || saving || previewing" class="preview-loading"><i class="c-spinner" />{{ loading || downloading || saving ? statusText : "正在合成预览…" }}</span>
            <span v-if="previewError" class="preview-error">{{ previewError }}</span>
          </div>
          <div v-else class="stage-empty">
            <template v-if="!(loading || downloading || saving)">
              <p v-if="active && active.error">{{ active.error }}</p>
              <p v-else-if="active">取帧后可在这里选择画面并实时预览海报。</p>
            </template>
            <span v-if="loading || downloading || saving" class="stage-empty-loading"><i class="c-spinner" />{{ statusText }}</span>
          </div>
          <!-- 包装设置：仅一行（开关 + 片名），样式入口在底栏 -->
          <div v-if="active" class="stylebar">
            <label class="tb-switch" :class="{ on: activePackaged }">
              <input v-model="activePackaged" type="checkbox" @change="onPackagedToggle">
              <span class="tb-track" /><b>包装海报</b>
            </label>
            <div class="tb-title">
              <input v-model="activeTitle" maxlength="16" type="text" placeholder="输入片名（最多 16 字）">
            </div>
          </div>
        </section>

        <aside class="c-cands">
          <div class="c-cands-cap"><b>候选画面</b><span>{{ active?.frames.length ?? 0 }} 张</span></div>
          <div v-if="active?.frames.length" ref="candListEl" class="cand-list">
            <div v-for="frame in pagedFrames" :key="frame.id" class="cand" :class="{ on: selectedFrame === frame.id }" @click="choose(frame)">
              <img :src="coverExtractApi.imageURL(frame.id)" loading="lazy">
              <span class="cand-t">{{ fmtTimestamp(frame.time_ms) }}</span>
              <span class="cand-mark">✓</span>
              <button type="button" class="cand-rm" title="移除该候选画面" @click.stop="removeFrame(frame.id)">✕</button>
            </div>
          </div>
          <div v-else-if="active" class="cand-empty">{{ active.error || "暂无候选画面，请先取帧" }}</div>
          <div v-else class="cand-empty">未选择影片</div>
          <div v-if="framesPageCount > 1" class="c-pager">
            <button type="button" :disabled="framesPage <= 1" @click="framesPage--">‹</button>
            <span>{{ framesPage }} / {{ framesPageCount }}</span>
            <button type="button" :disabled="framesPage >= framesPageCount" @click="framesPage++">›</button>
          </div>
        </aside>
      </div>

      <!-- 底栏：仅保存 -->
      <div class="c-footer">
        <div class="footer-style-wrap" @click.stop>
          <button type="button" class="footer-style" :class="{ open: stylePanelOpen }" title="海报样式设置" @click="toggleStylePanel">≡</button>
          <div v-if="stylePanelOpen" class="style-panel" @click.stop>
            <div class="sp-item"><span class="sp-ic">▧</span><span class="sp-label">形状</span>
              <div class="sp-toggle">
                <button type="button" :class="{ on: activePanelShape === 'slant' }" @click="activePanelShape = 'slant'">斜切</button>
                <button type="button" :class="{ on: activePanelShape === 'straight' }" @click="activePanelShape = 'straight'">直角</button>
              </div>
            </div>
            <div class="sp-item"><span class="sp-ic">↕</span><span class="sp-label">高度</span><input v-model.number="activePanelHeight" type="range" min="0.15" max="0.3" step="0.01"><small>{{ Math.round(activePanelHeight * 100) }}%</small></div>
            <div class="sp-item"><span class="sp-ic">◐</span><span class="sp-label">底色</span>
              <button type="button" class="sp-swatch-btn" :class="{ open: colorPickerFor === 'panel' }" :style="{ background: activePanelColor }" @click.stop="toggleColorPicker('panel')" />
            </div>
            <div class="sp-item"><span class="sp-ic">◔</span><span class="sp-label">透明度</span><input v-model.number="activePanelOpacity" type="range" min="0" max="1" step="0.05"><small>{{ Math.round(activePanelOpacity * 100) }}%</small></div>
            <div class="sp-item"><span class="sp-ic">A</span><span class="sp-label">字色</span>
              <button type="button" class="sp-swatch-btn" :class="{ open: colorPickerFor === 'text' }" :style="{ background: activeTextColor }" @click.stop="toggleColorPicker('text')" />
            </div>
            <div v-if="colorPickerFor" class="sp-palette" @click.stop>
              <button v-for="c in paletteColors" :key="c" type="button" class="sp-palette-cell" :class="{ sel: (colorPickerFor === 'panel' ? activePanelColor : activeTextColor) === c }" :style="{ background: c }" :title="c" @click="pickColor(c)" />
            </div>
            <button type="button" class="sp-save" @click="saveAsDefault">设为默认样式</button>
          </div>
        </div>
        <AccountFolderField class="tb-path" :display="targetDisplay" :title="`封面保存到 ${targetDisplay}`" browse-label="目录" @browse="openTargetPicker" />
        <button class="tb-save-btn" type="button" :disabled="!enabled || !selectedFrame || saving || loading || previewing || !active" @click="save">{{ saving ? "保存中…" : "保存封面" }}</button>
      </div>
    </div>
  </AppModal>
  <FolderPickerModal :open="targetPickerOpen" nested :account-id="active?.account_id ?? null" :initial-path="active?.target_path ?? '/'" title="选择封面保存目录" confirm-text="保存到此目录" @close="targetPickerOpen = false" @resolve="setTarget" />

  <AppModal :open="helpOpen" title="为什么截取不到？" size="sm" @close="helpOpen = false">
    <div class="c-help-body">
      <p>取帧需要从网盘读取视频数据，快慢和成败主要取决于网络与网盘账号：</p>
      <ul>
        <li><strong>网络环境</strong>：带宽不足时读取慢，可能超时。</li>
        <li><strong>网盘会员</strong>：无会员（如夸克非 SVIP）下载直链会被限速，大文件或高码率视频可能很慢甚至失败。</li>
        <li><strong>视频规格</strong>：视频越大、码率越高，取帧需要读取的数据越多，耗时越长。</li>
      </ul>
      <p>建议：在带宽较好的网络下操作 / 开通网盘会员 / 改用其他网盘 / 稍后重试。同一文件多次取帧的候选图都会保留，可对比选择。</p>
    </div>
    <template #footer>
      <AppButton variant="primary" @click="helpOpen = false">知道了</AppButton>
    </template>
  </AppModal>
</template>

<style scoped>
/* ── 浅色工作区：局部变量覆盖，独立于全局深色主题 ── */
.cover-shell {
  --c-bg: #f6f7f9;
  --c-panel: #ffffff;
  --c-line: #e8eaee;
  --c-line2: #d8dbe1;
  --c-text: #1a1d23;
  --c-muted: #6b7280;
  --c-faint: #9aa1ac;
  --c-accent: #1f6feb;
  --c-accent-soft: rgba(31, 111, 235, 0.08);
  --c-dark: #0f141a;
  --c-danger: #dc2626;
  --c-serif: "Songti SC", "STSong", "Noto Serif SC", serif;

  width: min(900px, 94vw);
  max-height: calc(100vh - 96px);
  display: flex;
  flex-direction: column;
  background: var(--c-bg);
  color: var(--c-text);
  border-radius: 22px;
  overflow: hidden;
  box-shadow: 0 24px 70px rgba(20, 30, 50, 0.18);
  font-size: 14px;
}
.cover-shell * { box-sizing: border-box; }

/* ── 深色主题：局部变量整体切换，与全局 tokens 的 dark 值一致 ── */
:root[data-theme="dark"] .cover-shell {
  --c-bg: #101215;
  --c-panel: #181b20;
  --c-line: #2b3038;
  --c-line2: #3a4250;
  --c-text: #e7eaf0;
  --c-muted: #9099a8;
  --c-faint: #6a7380;
  --c-accent: #3b82f6;
  --c-accent-soft: rgba(59, 130, 246, 0.18);
  --c-dark: #3b82f6;
  --c-danger: #ef4444;
}
:root[data-theme="dark"] .cover-warning {
  background: rgba(245, 158, 11, 0.12);
  color: #fbbf24;
  border-bottom-color: var(--c-line);
}
:root[data-theme="dark"] .c-extract,
:root[data-theme="dark"] .tb-save-btn,
:root[data-theme="dark"] .footer-style {
  border-color: rgba(255, 255, 255, 0.14);
}
:root[data-theme="dark"] .c-extract:hover,
:root[data-theme="dark"] .tb-save-btn:hover {
  background: #2563eb;
}

/* 顶栏 */
.c-top { display: flex; align-items: center; gap: 12px; padding: 11px 20px; background: var(--c-panel); border-bottom: 1px solid var(--c-line); flex-shrink: 0; }
.c-title { margin: 0; font-size: 15px; font-weight: 650; letter-spacing: 0.2px; white-space: nowrap; }
.c-title-block { display: flex; align-items: center; gap: 6px; min-width: 0; }
.c-spacer { flex: 1; }

/* 左栏：待处理影片 */
.c-files { border-right: 1px solid var(--c-line); padding: 14px 12px; display: flex; flex-direction: column; gap: 8px; min-width: 0; min-height: 0; }
.c-files-cap { display: flex; justify-content: space-between; align-items: center; font-size: 12px; color: var(--c-muted); margin: 0 6px 2px; }
.c-files-cap b { color: var(--c-text); font-size: 13px; }
.c-files-clear { color: var(--c-muted); background: none; border: 0; font-size: 11px; cursor: pointer; font-family: inherit; }
.c-files-clear:hover { color: var(--c-danger); }
.c-files-list { display: flex; flex-direction: column; gap: 5px; overflow-y: auto; min-height: 0; flex: 1; }
.c-file { display: flex; align-items: center; gap: 10px; padding: 8px 11px; border-radius: 10px; cursor: pointer; border: 1px solid transparent; transition: background 0.15s, border-color 0.15s; }
.c-file:hover { background: var(--c-bg); }
.c-file.active { background: var(--c-accent-soft); border-color: rgba(31, 111, 235, 0.35); }
.c-file-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--c-line2); flex-shrink: 0; }
.c-file.active .c-file-dot { background: var(--c-accent); }
.c-file-info { min-width: 0; flex: 1; }
.c-file-info b { display: block; font-size: 13px; font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.c-file-info small { display: block; font-size: 11px; color: var(--c-muted); margin-top: 3px; }
.c-file-rm { width: 24px; height: 24px; border: 0; border-radius: 7px; background: transparent; color: var(--c-faint); font-size: 13px; cursor: pointer; flex-shrink: 0; opacity: 0; transition: opacity 0.12s; }
.c-file:hover .c-file-rm { opacity: 1; }
.c-file-rm:hover { color: var(--c-danger); background: rgba(220, 38, 38, 0.08); }
.c-file-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 22px 12px;
  text-align: center;
}
.c-file-empty__icon {
  font-size: 26px;
  line-height: 1;
  margin-bottom: 4px;
  opacity: 0.9;
}
.c-file-empty__title {
  font-size: 13px;
  font-weight: 600;
  color: var(--c-text);
}
.c-file-empty__steps {
  margin: 4px 0 0;
  padding: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 3px;
  font-size: 12px;
  color: var(--c-faint);
  line-height: 1.6;
}
.c-file-empty__steps li {
  display: flex;
  align-items: center;
  gap: 6px;
}
.c-file-empty__steps li::before {
  content: attr(data-step);
  flex-shrink: 0;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: var(--c-line2);
  color: var(--c-muted);
  font-size: 10px;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

/* 取帧模式 + 时间输入 */
.seg { display: flex; gap: 2px; background: var(--c-bg); border: 1px solid var(--c-line); border-radius: 10px; padding: 3px; flex-shrink: 0; }
.seg button { border: 0; background: transparent; padding: 7px 14px; border-radius: 8px; font-size: 13px; color: var(--c-muted); cursor: pointer; font-family: inherit; transition: background 0.15s, color 0.15s, box-shadow 0.15s; white-space: nowrap; }
.seg button.on { background: var(--c-panel); color: var(--c-text); box-shadow: 0 1px 4px rgba(20, 30, 50, 0.08); }
.time-input { display: flex; align-items: center; gap: 5px; flex-shrink: 0; }
.time-input input { width: 46px; padding: 7px 6px; border: 1px solid var(--c-line); border-radius: 8px; background: var(--c-panel); color: var(--c-text); font-size: 13px; text-align: center; outline: none; font-family: inherit; }
.time-input input:focus { border-color: var(--c-accent); }
.time-input span { font-size: 11px; color: var(--c-faint); }
.c-extract { padding: 8px 20px; border: 0; border-radius: 10px; background: var(--c-dark); color: #fff; font-size: 13px; font-weight: 500; cursor: pointer; font-family: inherit; transition: background 0.15s; flex-shrink: 0; }
.c-extract:hover { background: #1c232c; }
.c-extract:disabled { opacity: 0.4; cursor: not-allowed; }
.c-close { width: 34px; height: 34px; border: 1px solid var(--c-line); border-radius: 10px; background: var(--c-panel); color: var(--c-muted); font-size: 14px; cursor: pointer; flex-shrink: 0; }
.c-close:hover { color: var(--c-text); }
.c-help {
  width: 15px;
  height: 15px;
  border: 0;
  border-radius: 50%;
  background: var(--c-dark);
  color: #fff;
  font-size: 10px;
  font-weight: 700;
  line-height: 15px;
  text-align: center;
  cursor: pointer;
  padding: 0;
  flex-shrink: 0;
}
.c-help:hover {
  background: var(--c-accent);
}
.c-help-body {
  font-size: 13px;
  line-height: 1.8;
  color: var(--c-text);
}
.c-help-body p {
  margin: 0 0 8px;
}
.c-help-body ul {
  margin: 0 0 8px;
  padding-left: 18px;
}
.c-help-body li {
  margin-bottom: 4px;
}
.c-help-body strong {
  color: var(--c-danger);
}

/* ffmpeg 警告 */
.cover-warning { padding: 8px 20px; border-bottom: 1px solid var(--c-line); background: #fff8ec; color: #b45309; font-size: 12px; line-height: 1.6; display: flex; align-items: center; gap: 12px; flex-wrap: wrap; flex-shrink: 0; }

/* 主体：三栏 */
.c-layout { display: grid; grid-template-columns: 280px minmax(0, 1fr) 260px; flex: 1; min-height: 0; }
.c-cands { border-left: 1px solid var(--c-line); padding: 14px 12px; display: flex; flex-direction: column; gap: 8px; min-height: 0; }
.c-cands-cap { display: flex; justify-content: space-between; font-size: 12px; color: var(--c-muted); margin: 0 6px 2px; }
.c-cands-cap b { color: var(--c-text); font-size: 13px; }
.cand-list { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 8px; overflow-y: auto; min-height: 0; flex: 1; align-content: start; }
.cand { position: relative; min-width: 0; border: 2px solid transparent; border-radius: 11px; overflow: hidden; cursor: pointer; background: #000; padding: 0; transition: border-color 0.16s, box-shadow 0.16s; }
.cand:hover { border-color: var(--c-line2); }
.cand.on { border-color: var(--c-accent); box-shadow: 0 0 0 3px var(--c-accent-soft); }
.cand img { display: block; width: 100%; aspect-ratio: 4/3; object-fit: cover; }
.cand-t { position: absolute; left: 7px; bottom: 6px; font-size: 10px; color: #fff; background: rgba(0, 0, 0, 0.58); padding: 2px 6px; border-radius: 5px; }
.cand-mark { position: absolute; top: 6px; right: 6px; width: 18px; height: 18px; border-radius: 50%; background: var(--c-accent); color: #fff; font-size: 10px; display: flex; align-items: center; justify-content: center; opacity: 0; }
.cand.on .cand-mark { opacity: 1; }
.cand-rm { position: absolute; right: 6px; bottom: 5px; width: 20px; height: 20px; border: 0; border-radius: 50%; background: rgba(220, 38, 38, 0.85); color: #fff; font-size: 11px; cursor: pointer; display: flex; align-items: center; justify-content: center; opacity: 0; transition: opacity 0.12s; z-index: 2; }
.cand:hover .cand-rm { opacity: 1; }
.cand-rm:hover { background: #b91c1c; }
.cand-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 120px;
  font-size: 12px;
  color: var(--c-faint);
  line-height: 1.7;
  text-align: center;
  padding: 10px 8px;
}

/* 分页条 */
.c-pager { display: flex; align-items: center; justify-content: center; gap: 10px; padding: 4px 0 0; flex-shrink: 0; }
.c-pager button { width: 26px; height: 26px; border: 1px solid var(--c-line); border-radius: 7px; background: var(--c-panel); color: var(--c-muted); font-size: 12px; cursor: pointer; display: flex; align-items: center; justify-content: center; transition: border-color 0.15s, color 0.15s; }
.c-pager button:hover:not(:disabled) { border-color: var(--c-line2); color: var(--c-text); }
.c-pager button:disabled { opacity: 0.35; cursor: not-allowed; }
.c-pager span { font-size: 11px; color: var(--c-faint); }

.c-stage { padding: 12px 14px 14px; display: flex; flex-direction: column; align-items: center; min-width: 0; min-height: 0; overflow-y: auto; }
.preview-wrap { position: relative; width: min(280px, 100%); flex-shrink: 0; border-radius: 14px; overflow: hidden; background: #000; box-shadow: 0 16px 40px rgba(20, 30, 50, 0.16); cursor: grab; touch-action: none; user-select: none; }
.preview-wrap.dragging { cursor: grabbing; }
.preview-wrap canvas { display: block; width: 100%; aspect-ratio: 2/3; pointer-events: none; transition: opacity 0.15s; }
.preview-wrap canvas.loading { opacity: 0.5; }
.zoom { position: absolute; top: 12px; right: 12px; display: flex; align-items: center; gap: 8px; padding: 7px 11px; border-radius: 999px; background: rgba(10, 14, 22, 0.6); border: 1px solid rgba(255, 255, 255, 0.14); color: #fff; font-size: 11px; cursor: default; }
.adjust-btn { position: absolute; top: 12px; left: 12px; padding: 5px 13px; border-radius: 999px; background: rgba(10, 14, 22, 0.6); border: 1px solid rgba(255, 255, 255, 0.14); color: #fff; font-size: 11px; cursor: pointer; z-index: 3; backdrop-filter: blur(10px); }
.adjust-btn.active { background: var(--c-accent); border-color: var(--c-accent); }
.adjust-reset-btn { position: absolute; top: 12px; left: 72px; padding: 5px 12px; border-radius: 999px; background: rgba(10, 14, 22, 0.6); border: 1px solid rgba(255, 255, 255, 0.14); color: #fff; font-size: 11px; cursor: pointer; z-index: 3; backdrop-filter: blur(10px); }
.adjust-reset-btn:hover { background: rgba(10, 14, 22, 0.78); }
.adjust-hint { position: absolute; top: 50px; left: 50%; transform: translateX(-50%); padding: 4px 12px; border-radius: 999px; background: rgba(10, 14, 22, 0.66); border: 1px solid rgba(255, 255, 255, 0.14); color: #fff; font-size: 10px; white-space: nowrap; z-index: 3; pointer-events: none; }
.zoom input { appearance: none; -webkit-appearance: none; width: 84px; height: 14px; margin: 0; background: transparent; cursor: pointer; }
.zoom input::-webkit-slider-runnable-track { height: 3px; border: 0; border-radius: 999px; background: rgba(255, 255, 255, 0.32); }
.zoom input::-webkit-slider-thumb { -webkit-appearance: none; appearance: none; width: 12px; height: 12px; margin-top: -4.5px; border: 2px solid rgba(20, 25, 34, 0.48); border-radius: 50%; background: #fff; box-shadow: 0 2px 7px rgba(0, 0, 0, 0.28); transition: transform 0.15s; }
.zoom input:hover::-webkit-slider-thumb { transform: scale(1.12); }
.zoom input::-moz-range-track { height: 3px; border: 0; border-radius: 999px; background: rgba(255, 255, 255, 0.32); }
.zoom input::-moz-range-thumb { width: 10px; height: 10px; border: 2px solid rgba(20, 25, 34, 0.48); border-radius: 50%; background: #fff; box-shadow: 0 2px 7px rgba(0, 0, 0, 0.28); }
.preview-loading { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; gap: 8px; color: #fff; background: rgba(5, 7, 12, 0.28); font-size: 13px; pointer-events: none; z-index: 3; }
.preview-error { position: absolute; left: 0; right: 0; bottom: 0; padding: 6px 10px; background: rgba(127, 29, 29, 0.72); color: #fecaca; font-size: 11px; text-align: center; pointer-events: none; z-index: 3; }
.stage-empty { position: relative; display: flex; align-items: center; justify-content: center; width: min(280px, 100%); aspect-ratio: 2/3; border: 1px dashed var(--c-line2); border-radius: 14px; color: var(--c-muted); font-size: 13px; text-align: center; line-height: 1.9; padding: 20px; }
.stage-empty-loading { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; gap: 8px; background: var(--c-bg); color: var(--c-muted); font-size: 12px; border-radius: 14px; z-index: 2; }
.c-spinner { width: 14px; height: 14px; border: 2px solid var(--c-line2); border-top-color: var(--c-accent); border-radius: 50%; animation: c-spin 0.7s linear infinite; display: inline-block; flex-shrink: 0; }
@keyframes c-spin { to { transform: rotate(360deg); } }

/* 包装设置条：仅一行（开关 + 片名） */
.stylebar { position: relative; margin-top: 10px; width: min(320px, 100%); display: flex; align-items: center; gap: 12px; padding: 8px 12px; border: 1px solid var(--c-line); border-radius: 12px; background: var(--c-panel); }
/* 底栏样式入口：三横图标，面板向上弹出（只遮左栏） */
.footer-style-wrap { position: relative; flex-shrink: 0; }
.footer-style { width: 38px; height: 40px; border: 1px solid var(--c-line); border-radius: 10px; background: var(--c-panel); color: var(--c-muted); font-size: 16px; cursor: pointer; display: flex; align-items: center; justify-content: center; transition: border-color 0.15s, color 0.15s, background 0.15s; }
.footer-style:hover, .footer-style.open { color: var(--c-text); border-color: var(--c-line2); background: var(--c-bg); }
/* 样式浮层面板：浅色、竖列、在左栏内左右等距居中 */
.style-panel { position: absolute; bottom: calc(100% + 8px); left: -10px; width: 260px; display: flex; flex-direction: column; gap: 7px; padding: 12px; border-radius: 12px; background: var(--c-panel); color: var(--c-text); border: 1px solid var(--c-line); box-shadow: 0 14px 34px rgba(20, 30, 50, 0.14); z-index: 30; }
.style-panel::after { content: ""; position: absolute; top: 100%; left: 36px; border: 5px solid transparent; border-top-color: var(--c-panel); }
.sp-item { display: flex; align-items: center; gap: 10px; min-height: 30px; }
.sp-ic { width: 16px; font-size: 13px; color: var(--c-muted); text-align: center; flex-shrink: 0; }
.sp-label { width: 40px; font-size: 12px; color: var(--c-muted); flex-shrink: 0; }
.sp-toggle { flex: 1; min-width: 0; display: flex; border: 1px solid var(--c-line); border-radius: 7px; overflow: hidden; }
.sp-toggle button { flex: 1; padding: 5px 0; border: 0; background: var(--c-bg); color: var(--c-muted); font-size: 12px; cursor: pointer; font-family: inherit; transition: background 0.15s, color 0.15s; }
.sp-toggle button + button { border-left: 1px solid var(--c-line); }
.sp-toggle button.on { background: var(--c-accent); color: #fff; }
.sp-item input[type="range"] { appearance: none; -webkit-appearance: none; flex: 1; min-width: 0; height: 18px; margin: 0; background: transparent; cursor: pointer; }
.sp-item input[type="range"]::-webkit-slider-runnable-track { height: 3px; border: 0; border-radius: 999px; background: var(--c-line2); }
.sp-item input[type="range"]::-webkit-slider-thumb { -webkit-appearance: none; appearance: none; width: 13px; height: 13px; margin-top: -5px; border: 2px solid #fff; border-radius: 50%; background: var(--c-accent); box-shadow: 0 1px 4px rgba(20, 30, 50, 0.22); transition: transform 0.15s; }
.sp-item input[type="range"]:hover::-webkit-slider-thumb { transform: scale(1.12); }
.sp-item input[type="range"]::-moz-range-track { height: 3px; border: 0; border-radius: 999px; background: var(--c-line2); }
.sp-item input[type="range"]::-moz-range-thumb { width: 11px; height: 11px; border: 2px solid #fff; border-radius: 50%; background: var(--c-accent); box-shadow: 0 1px 4px rgba(20, 30, 50, 0.22); }
.sp-item input[type="color"] { width: 46px; height: 26px; border: 1px solid var(--c-line); border-radius: 6px; padding: 2px; background: var(--c-bg); cursor: pointer; }
/* 色块选择：自定义色板，无原生控件兼容问题 */
.sp-swatch-btn { flex: 1; min-width: 0; height: 26px; border: 1px solid var(--c-line); border-radius: 6px; cursor: pointer; padding: 0; }
.sp-swatch-btn.open { outline: 2px solid var(--c-accent); outline-offset: 1px; }
.sp-palette { display: grid; grid-template-columns: repeat(8, 1fr); gap: 6px; padding: 10px; border: 1px solid var(--c-line); border-radius: 8px; background: var(--c-bg); }
.sp-palette-cell { width: 100%; aspect-ratio: 1; border: 1px solid var(--c-line2); border-radius: 5px; cursor: pointer; padding: 0; }
.sp-palette-cell.sel { outline: 2px solid var(--c-accent); outline-offset: 1px; }
.sp-item small { width: 42px; text-align: right; font-size: 11px; color: var(--c-faint); flex-shrink: 0; }
.sp-save { width: 100%; padding: 7px; border: 1px solid var(--c-line); border-radius: 8px; background: var(--c-bg); color: var(--c-muted); font-size: 12px; cursor: pointer; font-family: inherit; transition: border-color 0.15s, color 0.15s; margin-top: 2px; }
.sp-save:hover { color: var(--c-text); border-color: var(--c-line2); }
.tb-switch { display: flex; align-items: center; gap: 9px; cursor: pointer; flex-shrink: 0; user-select: none; }
.tb-switch input { display: none; }
.tb-track { width: 36px; height: 20px; border-radius: 999px; background: var(--c-line2); position: relative; transition: background 0.2s; flex-shrink: 0; }
.tb-track::after { content: ""; position: absolute; top: 2.5px; left: 3px; width: 15px; height: 15px; border-radius: 50%; background: #fff; transition: left 0.2s; box-shadow: 0 1px 3px rgba(0, 0, 0, 0.25); }
.tb-switch.on .tb-track { background: var(--c-accent); }
.tb-switch.on .tb-track::after { left: 18px; }
.tb-switch b { font-size: 12.5px; font-weight: 600; }
.tb-title { flex: 1; min-width: 80px; max-width: 170px; }
.tb-title input { width: 100%; border: 0; border-bottom: 1px solid var(--c-line2); background: transparent; padding: 5px 2px; font-size: 14px; font-family: var(--c-serif); color: var(--c-text); outline: none; transition: border-color 0.15s; }
.tb-title input:focus { border-color: var(--c-accent); }
.tb-title input::placeholder { color: var(--c-faint); }
/* 底栏：仅保存（目录全宽 + 保存按钮） */
.c-footer { display: flex; align-items: center; gap: 12px; padding: 10px 20px; border-top: 1px solid var(--c-line); background: var(--c-panel); flex-shrink: 0; }
.tb-path { flex: 1; min-width: 0; }
.tb-save-btn { padding: 10px 26px; border: 0; border-radius: 10px; background: var(--c-dark); color: #fff; font-size: 13px; font-weight: 600; cursor: pointer; font-family: inherit; transition: background 0.15s; }
.tb-save-btn:hover { background: #1c232c; }
.tb-save-btn:disabled { opacity: 0.4; cursor: not-allowed; }

/* 卡片开关（保持原样） */
.check-toggle { width: 28px; height: 28px; border-radius: 50%; border: 0; padding: 0; flex-shrink: 0; display: inline-flex; align-items: center; justify-content: center; cursor: pointer; background: var(--border); color: var(--text-muted); transition: background 0.18s ease, color 0.18s ease, box-shadow 0.18s ease; }
.check-toggle svg { width: 14px; height: 14px; }
.check-toggle:hover { background: var(--surface-hover); }
.check-toggle.on { background: var(--success); color: #fff; box-shadow: 0 0 0 4px rgba(16, 185, 129, 0.16); }
.check-toggle:disabled { opacity: 0.5; cursor: not-allowed; }

@media (max-width: 900px) {
  /* 小屏：弹窗交给页面纵向滚动，横向永不溢出 */
  .cover-shell { max-height: none; overflow-x: hidden; overflow-y: visible; }
  .c-layout { grid-template-columns: 1fr; }
  .c-files { border-right: 0; border-bottom: 1px solid var(--c-line); padding: 14px; max-height: 210px; overflow: hidden; }
  .c-files-list { flex-direction: column; overflow: hidden; }
  .c-file { flex-shrink: 0; }
  .c-stage { overflow: visible; padding: 16px 14px 14px; }
  .preview-wrap { width: min(300px, 84vw); touch-action: pan-y; }
  .preview-wrap.adjust-mode { touch-action: none; }
  .stage-empty { width: min(300px, 84vw); }
  .stylebar { width: min(300px, 84vw); }
  .c-cands { border-left: 0; border-top: 1px solid var(--c-line); padding: 14px; }
  .cand-list { grid-template-columns: repeat(2, minmax(0, 1fr)); overflow-y: auto; align-content: start; }
  .cand { min-width: 0; }
  .c-files-list, .cand-list { scrollbar-width: none; }
  .c-files-list::-webkit-scrollbar, .cand-list::-webkit-scrollbar { display: none; }
  .c-top { flex-wrap: wrap; gap: 8px; }
  .seg button { padding: 6px 10px; font-size: 12px; }
  .time-input input { width: 40px; }
  .c-extract { padding: 8px 14px; }
  .c-footer { flex-wrap: wrap; gap: 12px; }
}
</style>
