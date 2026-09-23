<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AccountFolderField from "@/components/admin/AccountFolderField.vue";
import FolderPickerModal from "@/components/file/FolderPickerModal.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import SettingsRowLabel from "@/components/admin/SettingsRowLabel.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  fetchTGConfig,
  fetchTGQualityProfiles,
  saveTGConfig,
  testTGConnection,
} from "@/api/tgSubscribe";
import { useAccountsStore } from "@/stores/accounts";
import { confirm } from "@/composables/useConfirm";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { bindSettingsPanelExpose } from "@/composables/useSettingsForm";
import { toast } from "@/composables/useToast";
import type { TGConfig, TGConfigInput, TGQualityProfile } from "@/types/tg-subscribe";

const emit = defineEmits<{ changed: [] }>();

const accountsStore = useAccountsStore();

const ACCENT = "var(--brand)";

const loading = ref(false);
const saving = ref(false);
const testing = ref(false);
const profiles = ref<TGQualityProfile[]>([]);
const status = ref<TGConfig | null>(null);
const loaded = ref(false);

/** 服务端基线，用于脏判断与「还原」。 */
const baseline = ref("");

// 初值只是「后端还没回话时的占位」—— load() 一回来就被 applyConfig 覆盖。
// 这里跟后端 registry 的默认值保持一致（四个开关都开），免得加载那一瞬间
// 界面显示的是「全关」，用户以为默认关着。
const draft = reactive<TGConfigInput>({
  enabled: true,
  auto_push: true,
  default_account_id: 0,
  default_parent_id: "",
  default_display_path: "",
  default_quality_profile_id: 0,
  collect_window_min: 5,
  max_push_per_hour: 20,
  poll_interval_sec: 600,
  backfill_pages: 1,
  web_search_enabled: true,
  web_search_base_url: "",
  web_search_cloud_types: "magnet,115",
  web_search_token: "",
  web_search_use_proxy: false,
  web_search_auto: true,
  web_search_interval_sec: 21600,
});

const pickerOpen = ref(false);

/** 脏判断只看用户能编辑、且会回传的字段。 */
function snapshot() {
  return JSON.stringify({
    enabled: draft.enabled,
    auto_push: draft.auto_push,
    default_account_id: draft.default_account_id,
    default_parent_id: draft.default_parent_id,
    default_display_path: draft.default_display_path,
    default_quality_profile_id: draft.default_quality_profile_id,
    collect_window_min: draft.collect_window_min,
    max_push_per_hour: draft.max_push_per_hour,
    poll_interval_sec: draft.poll_interval_sec,
    backfill_pages: draft.backfill_pages,
    web_search_enabled: draft.web_search_enabled,
    web_search_base_url: draft.web_search_base_url,
    web_search_cloud_types: draft.web_search_cloud_types,
    web_search_token: draft.web_search_token,
    web_search_use_proxy: draft.web_search_use_proxy,
    web_search_auto: draft.web_search_auto,
    web_search_interval_sec: draft.web_search_interval_sec,
  });
}

const isDirty = computed(() => loaded.value && snapshot() !== baseline.value);

const profileOptions = computed(() => [
  { value: 0, label: "跟随默认方案" },
  ...profiles.value.map((p) => ({
    value: p.id,
    label: p.is_default ? `${p.name}（默认）` : p.name,
  })),
]);

/** 账号名 + 目录的展示串。 */
const defaultTargetText = computed(() => {
  if (!draft.default_account_id) return "";
  const account = accountsStore.accounts.find((a) => a.id === draft.default_account_id);
  return `${account?.name ?? `账号 ${draft.default_account_id}`} · ${draft.default_display_path || "/"}`;
});

function applyConfig(cfg: TGConfig) {
  status.value = cfg;
  draft.enabled = cfg.enabled;
  draft.auto_push = cfg.auto_push;
  draft.default_account_id = cfg.default_account_id;
  draft.default_parent_id = cfg.default_parent_id;
  draft.default_display_path = cfg.default_display_path;
  draft.default_quality_profile_id = cfg.default_quality_profile_id;
  draft.collect_window_min = cfg.collect_window_min;
  draft.max_push_per_hour = cfg.max_push_per_hour;
  draft.poll_interval_sec = cfg.poll_interval_sec || 600;
  draft.backfill_pages = cfg.backfill_pages;
  draft.web_search_enabled = cfg.web_search_enabled;
  // 读回来的是**生效值**（后端把留空回落成了默认），所以这里直接赋值 ——
  // 不用 `|| 默认` 兜底，否则用户清空地址后就再也看不出回落到了哪里。
  draft.web_search_base_url = cfg.web_search_base_url;
  draft.web_search_cloud_types = cfg.web_search_cloud_types;
  draft.web_search_token = cfg.web_search_token;
  draft.web_search_use_proxy = cfg.web_search_use_proxy;
  draft.web_search_auto = cfg.web_search_auto;
  draft.web_search_interval_sec = cfg.web_search_interval_sec;
  baseline.value = snapshot();
  loaded.value = true;
}

async function load() {
  loading.value = true;
  try {
    const [cfg, list] = await Promise.all([fetchTGConfig(), fetchTGQualityProfiles()]);
    profiles.value = list;
    applyConfig(cfg);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载配置失败"));
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (!isDirty.value) return;
  saving.value = true;
  try {
    applyConfig(await saveTGConfig({ ...draft }));
    toast.success("配置已保存");
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存配置失败"));
  } finally {
    saving.value = false;
  }
}

function revert() {
  if (status.value) applyConfig(status.value);
}

async function runTest() {
  testing.value = true;
  try {
    const { detail } = await testTGConnection();
    toast.success(`连接正常：${detail}`);
    status.value = await fetchTGConfig();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "连接 t.me 失败"));
  } finally {
    testing.value = false;
  }
}

function onTargetPicked(payload: { accountId: number; parentId: string; path: string }) {
  draft.default_account_id = payload.accountId;
  draft.default_parent_id = payload.parentId;
  draft.default_display_path = payload.path || "/";
  pickerOpen.value = false;
}

async function toggleAutoPush(next: boolean) {
  // 打开自动推送是「真的会往网盘写东西」的开关，值得确认一次。
  if (next) {
    try {
      await confirm({
        title: "开启自动推送",
        message:
          "开启后，匹配到的资源会按画质方案自动推送到网盘并开始下载。建议先在观察模式下看过匹配历史再打开。",
        confirmText: "确认开启",
        danger: false,
        icon: "warning",
      });
    } catch {
      return;
    }
  }
  draft.auto_push = next;
}

const fetchStatusText = computed(() => {
  const s = status.value?.status;
  if (s === "ok") {
    const at = status.value?.last_poll_at;
    return at ? `正常（上次抓取 ${timeAgo(at)}）` : "正常";
  }
  if (s === "error") return "抓取异常";
  return "未检测";
});

/** 把 RFC3339 时间戳说成「几分钟前」。 */
function timeAgo(iso: string): string {
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return "刚刚";
  const mins = Math.floor((Date.now() - ts) / 60000);
  if (mins < 1) return "刚刚";
  if (mins < 60) return `${mins} 分钟前`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

// 改了没保存就切页时要拦一下，和其余设置页一致。
useSettingsPageDirty(isDirty, revert);

onMounted(async () => {
  await accountsStore.loadAccounts();
  if (!loaded.value) await load();
});

defineExpose(
  bindSettingsPanelExpose({
    isDirty,
    saving,
    save,
    reload: load,
    revert,
  }),
);
</script>

<template>
  <div class="tg-panel">
    <div v-if="loading" class="settings-card__loading">加载中…</div>

    <template v-else>
      <SettingsCard title="抓取设置" :accent="ACCENT">
        <template #head-aside>
          <span>{{ fetchStatusText }}</span>
        </template>
        <template #head-actions>
          <AppButton type="button" variant="secondary" size="sm" :disabled="testing" @click="runTest">
            {{ testing ? "测试中…" : "测试连接" }}
          </AppButton>
        </template>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="启用 TG 订阅" />
          </template>
          <template #control>
            <SettingsBoolSegment v-model="draft.enabled" label="启用 TG 订阅" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="抓取间隔（秒）"
              help-title="抓取间隔"
              help-text="每个频道多久抓一次 Telegram 网页预览。t.me 不是给程序用的接口，填太小有被限流的风险，建议 300–900。频道多的时候会自动放大实际间隔。"
            />
          </template>
          <template #control>
            <AppInput v-model="draft.poll_interval_sec" type="number" placeholder="600" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="首次订阅回填页数" help-title="回填页数">
              <p>新加频道时往回翻多少页历史，每页 20 条。</p>
              <p>填 1 表示回填最近 20 条；填 0 表示只从最新一条开始追新。</p>
              <p>
                回填出来的历史帖同样会参与匹配。若已打开「自动推送」，它们也会被推送 ——
                受每小时推送上限约束，建议先看过匹配历史再开自动推送。
              </p>
              <p v-if="status?.effective_interval_sec">
                当前共 {{ status.channel_count }} 个频道，实际每
                {{ Math.round(status.effective_interval_sec / 60) }} 分钟抓完一轮。
              </p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput v-model="draft.backfill_pages" type="number" placeholder="1" />
          </template>
        </SettingsRow>

        <p class="tg-form__hint">
          抓取走「系统设置 → 其他设置 → 网络代理」里的全局代理；国内直连 t.me 通常不通，建议先去那里配好。
        </p>
      </SettingsCard>

      <SettingsCard title="网盘搜索" :accent="ACCENT">
        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="启用" help-title="网盘搜索">
              <p>
                拿订阅的片名去外部聚合搜索引擎搜资源。抓取是「等频道推过来」，
                这条是「主动去搜」—— 不受你订阅了几个频道限制。
              </p>
              <p>
                搜到的结果只会进「匹配历史」标成待确认，不会自动推送；
                你在影片详情里点「搜网盘」才会发起一次搜索。
              </p>
              <p>依赖第三方站点，默认关闭。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="draft.web_search_enabled" label="启用网盘搜索" />
          </template>
        </SettingsRow>

        <!-- 自动搜索这两行跟着总开关置灰。**但值不会被清掉**：总开关关着时后端
             照原样存着这两个字段，用户临时关掉再打开，自动搜索还在。 -->
        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="自动搜索" help-title="自动搜索">
              <p>打开后系统会按下面的间隔，自动替<b>还没收齐</b>的订阅发起搜索，不用一条条手点。</p>
              <p>只挑没收齐的下手：电影推过一次、剧集收齐已播出集数之后就不再搜它。</p>
              <p>
                搜到的结果和频道抓来的一视同仁 —— 同一套画质方案、同一个聚合窗口、
                同一套失败重试。唯一的分岔是「自动推送」关着时（观察模式）只记进匹配历史
                并标成待确认，等你点。
              </p>
              <p>它依赖上面的「网盘搜索」总开关，那个关着时这里什么都不做。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <SettingsBoolSegment
              v-model="draft.web_search_auto"
              label="自动搜索"
              :disabled="!draft.web_search_enabled"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="自动搜索间隔（秒）" help-title="自动搜索间隔">
              <p>多久替一条订阅搜一轮，默认 21600（6 小时）。</p>
              <p>
                下限 900（15 分钟）：外部搜索站没有给程序用的配额，而这是无人值守的轮询，
                填太小会一直打它。
              </p>
              <p>
                订阅多时实际间隔会按订阅数量自动放大（照抓取间隔那套做法），
                免得一轮还没跑完下一轮又开始了。
              </p>
              <p>
                与「抓取间隔」是<b>两个独立的值</b>，别指望改一个另一个跟着动：抓取一个频道
                只发一个请求，而一条订阅要搜最多 3 个关键词、每个还各自有一次超时，成本差着量级。
              </p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput
              v-model.number="draft.web_search_interval_sec"
              type="number"
              placeholder="21600"
              :disabled="!draft.web_search_enabled"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="搜索服务地址" help-title="搜索服务地址">
              <p>留空即用内置默认（公开的盘搜站点）。</p>
              <p>
                每次搜索会把这个地址下的 /api/search 打一遍，一个关键词一次请求，
                单次约 5–30 秒。
              </p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput
              v-model="draft.web_search_base_url"
              placeholder="https://so.252035.xyz"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="搜索的资源类型" help-title="资源类型">
              <p>
                逗号分隔。<strong>建议保留默认的 <code>magnet,115</code></strong> ——
                这个搜索站的国产内容里磁力占比只有 0%~4%，只填 <code>magnet</code>
                等于把绝大多数结果扔掉。
              </p>
              <p>
                这两类的区别：磁力走离线下载通道；115 分享走转存，
                需要目标账号配好「网页 Cookie」。
              </p>
              <p>
                夸克/百度/阿里等也可以填进来，但当前版本只会把它们收进匹配历史
                供你复制链接，**推不进网盘**（没有对应驱动与账号）。
              </p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput v-model="draft.web_search_cloud_types" placeholder="magnet,115" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="走全局代理" help-title="走全局代理">
              <p>默认关闭。搜索站是国内的，直连通常就通。</p>
              <p>
                「系统设置 → 其他设置 → 网络代理」里那个代理是给 t.me / TMDB 配的，
                套在搜索站上反而多一个失败点。只有直连不通时才需要打开。
              </p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="draft.web_search_use_proxy" label="走全局代理" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="访问令牌" help-title="访问令牌">
              <p>只有自建的搜索服务开了认证才需要填，公开站点留空即可。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput v-model="draft.web_search_token" placeholder="留空即可" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="推送" :accent="ACCENT">
        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="自动推送" help-title="自动推送说明">
              <p>关闭时是「观察模式」：只记录匹配历史，不往网盘推任何东西。</p>
              <p>建议先观察一段时间，确认匹配判定符合预期再打开。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <SettingsBoolSegment
              :model-value="draft.auto_push"
              label="自动推送"
              off-label="观察模式"
              @update:model-value="toggleAutoPush"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="默认目标目录" help-title="默认目标目录说明">
              <p>单条订阅没指定目标时用它。</p>
              <p>推送时会在该目录下自动创建「片名 (年份)」子目录，多个版本不会混在一起。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AccountFolderField
              :display="defaultTargetText"
              title="选择默认推送到的网盘目录"
              placeholder="点击选择网盘与目录"
              @browse="pickerOpen = true"
            />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="默认画质方案"
              help-title="默认画质方案说明"
              help-text="新订阅没指定方案时用它。"
            />
          </template>
          <template #control>
            <AppSelect v-model="draft.default_quality_profile_id" :options="profileOptions" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel label="聚合窗口" help-title="聚合窗口说明">
              <p>命中后先等这么久，把窗口内收到的所有版本一起比较，只推最优的一条。</p>
              <p>填 0 表示收到即推 —— 但那就变成「先到的先推」，先来的 720p 会把后面的 2160p 挤掉。</p>
            </SettingsRowLabel>
          </template>
          <template #control>
            <AppInput v-model.number="draft.collect_window_min" type="number" placeholder="5" />
          </template>
        </SettingsRow>

        <SettingsRow>
          <template #info>
            <SettingsRowLabel
              label="每小时推送上限"
              help-title="每小时推送上限说明"
              help-text="防止刚打开开关时聚合窗口里堆了几百条候选，一拥而上把网盘 API 打爆。"
            />
          </template>
          <template #control>
            <AppInput v-model.number="draft.max_push_per_hour" type="number" placeholder="20" />
          </template>
        </SettingsRow>
      </SettingsCard>
    </template>

    <FolderPickerModal
      :open="pickerOpen"
      title="选择默认推送目录"
      confirm-text="用这个目录"
      selectable-account
      :accounts="accountsStore.accounts"
      :account-id="draft.default_account_id || null"
      allow-create-folder
      @close="pickerOpen = false"
      @resolve="onTargetPicked"
    />
  </div>
</template>
