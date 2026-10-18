<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { getApiErrorMessage } from "@/api/client";
import { testMediaOrganizeTmdb } from "@/api/mediaOrganize";
import {
  fetchStrmScrapeSettings,
  saveStrmScrapeSettings,
  type StrmScrapeSettings,
  type StrmScrapeWriteMode,
} from "@/api/strmScrape";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import AppStateBlock from "@/components/base/AppStateBlock.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsCheckboxRow, { type CheckboxOption } from "@/components/admin/SettingsCheckboxRow.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import SettingsRow from "@/components/admin/SettingsRow.vue";
import TmdbHostsHelpTip from "@/components/admin/TmdbHostsHelpTip.vue";
import { useSettingsForm, bindSettingsPanelExpose } from "@/composables/useSettingsForm";
import { useSettingsLoad } from "@/composables/useSettingsLoad";
import { toast } from "@/composables/useToast";

const SCRAPE_SETTINGS_ACCENT = "#38bdf8";

/** 「翻哪些字段」的两个选项（顺序即界面顺序）。 */
const translateFieldOptions: CheckboxOption[] = [
  { key: "summary", label: "简介" },
  { key: "title", label: "标题" },
];

const tmdbLanguageOptions = [
  { value: "zh-CN", label: "简体中文" },
  { value: "zh-TW", label: "繁体中文" },
  { value: "en-US", label: "English" },
];

const writeModeOptions = [
  { value: "missing_only", label: "仅补缺（推荐）" },
  { value: "overwrite", label: "覆盖已有 nfo / 海报" },
];

const { loading, loaded, runLoad } = useSettingsLoad();
const saving = ref(false);
const tmdbTesting = ref(false);

const {
  settings,
  isDirty: settingsChanged,
  isFieldChanged,
  snapshotBaseline,
  applyBaseline,
  revert: revertToBaseline,
} = useSettingsForm<StrmScrapeSettings>({
  write_mode: "missing_only",
  // 默认开：与「合并前那颗 AI 按钮默认的行为」一致（点一下本来就会用 AI）。
  // 用户关掉它就是不花钱那一档。
  ai_backfill: true,
  tmdb_api_key: "",
  theporndb_key: "",
  // 只读的回读字段：后端告诉界面「key 配了没」，供输入框 placeholder 用。
  // 必须出现在初始草稿里（类型是必填）—— 但**它不参与保存**（后端自己算）。
  theporndb_key_set: false,
  flaresolverr_url: "",
  translate_enabled: false,
  translate_fields: ["summary"] as string[],
  translate_prompt: "",
  tmdb_language: "zh-CN",
  tmdb_api_host: "https://api.themoviedb.org",
  tmdb_image_host: "https://image.tmdb.org",
  tmdb_request_interval_ms: 300,
});

/**
 * 「翻哪些字段」的勾选桥：界面用 `Record<string, boolean>`（SettingsCheckboxRow 的形状），
 * 接口用 `string[]`。两边的形状不同是刻意的 —— 那个组件是全项目复选框的统一形态
 * （与「番号元数据六项」共用），不该为一个字段改成数组。
 */
const translateFieldChecks = computed({
  get: () => ({
    summary: settings.translate_fields.includes("summary"),
    title: settings.translate_fields.includes("title"),
  }),
  set: (v: Record<string, boolean>) => {
    settings.translate_fields = ["summary", "title"].filter((k) => v[k]);
  },
});

async function loadSettings(opts?: { silent?: boolean }) {
  await runLoad(
    async () => {
      const data = await fetchStrmScrapeSettings();
      applyBaseline({
        write_mode: (data.write_mode as StrmScrapeWriteMode) || "missing_only",
        // ⚠️ 新增设置项**必须在这里补一行**（下面那段注释说的就是这个坑）：
        // 漏了的话基线停在初始值上，打开设置页看到的不是库里的值。
        ai_backfill: data.ai_backfill !== false,
        tmdb_api_key: data.tmdb_api_key || "",
        tmdb_language: data.tmdb_language || "zh-CN",
        tmdb_api_host: data.tmdb_api_host || "https://api.themoviedb.org",
        tmdb_image_host: data.tmdb_image_host || "https://image.tmdb.org",
        tmdb_request_interval_ms: Number(data.tmdb_request_interval_ms) || 300,
        // ⚠️ 这四项曾经漏在回填之外：`useSettingsForm` 的初始草稿就是「基线」，
        // 而 `applyBaseline` 只覆盖传进来的键 —— 漏传的键基线就停在初始值上。
        // 于是**打开设置页永远是空的**（theporndb key 本来就只回「配没配」，
        // flaresolverr / 翻译提示词 / 翻哪些字段却是有值的），
        // 而只要点一次「保存」就会把这份「空」原样写回库里，把已配好的冲掉。
        // 新增设置项时**必须在这里补一行**，否则就是同一个坑。
        theporndb_key: data.theporndb_key || "",
        theporndb_key_set: Boolean(data.theporndb_key_set),
        flaresolverr_url: data.flaresolverr_url || "",
        translate_enabled: Boolean(data.translate_enabled),
        translate_fields: data.translate_fields ?? ["summary"],
        translate_prompt: data.translate_prompt || "",
      });
    },
    "加载 STRM 刮削设置失败",
    { silent: opts?.silent },
  );
}

async function saveSettings() {
  if (saving.value) return;
  saving.value = true;
  try {
    const data = await saveStrmScrapeSettings({
      ...settings,
      tmdb_request_interval_ms: Number(settings.tmdb_request_interval_ms),
    });
    applyBaseline({
      ...settings,
      write_mode: (data.write_mode as StrmScrapeWriteMode) || settings.write_mode,
      tmdb_api_key: data.tmdb_api_key || settings.tmdb_api_key,
      // 密钥**回过就清空输入框**：后端从不回传它，留在框里会让人以为「又存了一遍」，
      // 而且下次保存会把它再当改动提交一次。保存成功即视为「已配置」。
      theporndb_key: "",
      theporndb_key_set: data.theporndb_key_set || Boolean(settings.theporndb_key),
    });
    snapshotBaseline();
    toast.success("刮削设置已保存");
  } catch (e) {
    toast.error(getApiErrorMessage(e, "保存失败"));
  } finally {
    saving.value = false;
  }
}

async function testTmdb() {
  if (tmdbTesting.value) return;
  tmdbTesting.value = true;
  try {
    const result = await testMediaOrganizeTmdb({
      tmdb_api_key: settings.tmdb_api_key,
      tmdb_language: settings.tmdb_language,
      tmdb_api_host: settings.tmdb_api_host,
      tmdb_image_host: settings.tmdb_image_host,
      tmdb_request_interval_ms: Number(settings.tmdb_request_interval_ms),
    });
    const apiOK = result.api_ok ?? result.ok;
    const imageOK = result.image_ok ?? true;
    if (apiOK && imageOK) {
      toast.success("TMDB 连通正常：API ✓ 图片 ✓");
    } else if (apiOK && !imageOK) {
      toast.error("TMDB 部分异常：API ✓ 图片 ×");
    } else if (!apiOK && imageOK) {
      toast.error("TMDB 部分异常：API × 图片 ✓");
    } else {
      toast.error("TMDB 全部异常：API × 图片 ×");
    }
  } catch (e) {
    toast.error(getApiErrorMessage(e, "TMDB 测试失败"));
  } finally {
    tmdbTesting.value = false;
  }
}

onMounted(() => {
  void loadSettings();
});

defineExpose(
  bindSettingsPanelExpose({
    isDirty: settingsChanged,
    saving,
    save: saveSettings,
    reload: () => loadSettings({ silent: loaded.value }),
    revert: revertToBaseline,
  }),
);
</script>

<template>
  <div class="settings-panel" :style="{ '--settings-accent': SCRAPE_SETTINGS_ACCENT }">
    <AppStateBlock v-if="loading && !loaded" message="加载中…" loading min-height="120px" />
    <template v-else>
      <SettingsCard title="刮削策略" :accent="SCRAPE_SETTINGS_ACCENT">
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('write_mode')">
          <template #info>
            <div class="settings-row__label">写入策略</div>
          </template>
          <template #control>
            <AppSelect v-model="settings.write_mode" :options="writeModeOptions" />
          </template>
        </SettingsRow>
        <!-- AI 补认：原来是一颗「AI 识别刮削」按钮，已并进页头那颗「开始刮削」
             （两颗只差请求里一个字段，合并后给「批量选择」腾出了位置）。
             那件事会**花钱**，所以开关必须留在设置里由用户自己勾 —— 而且入口只剩一颗
             按钮之后，界面上再没有第二个地方能表达「这次别用 AI」。 -->
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('ai_backfill')">
          <template #info>
            <div class="settings-row__label">AI 补认识别</div>
            <SettingsHelpTooltip title="什么时候会用到 AI">
              刮削时，<strong>本地目录</strong>数据源里那些<strong>规则解析不出片名</strong>的作品，
              交给 AI 认一次片名，认出来再拿去搜 TMDB。<br /><br />
              规则自己就能搜到的作品一秒都不多等 —— 只有认不出的那批会问 AI。<br /><br />
              <strong>STRM 任务永远不走 AI</strong>（那套的速度与结果与以前完全一致）。<br /><br />
              AI 本身在「系统设置 → AI 整理」里配置；<strong>没配好时这一项不起作用</strong>，
              刮削照常跑，只是不调 AI。
            </SettingsHelpTooltip>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.ai_backfill" label="启用" />
          </template>
        </SettingsRow>
      </SettingsCard>

      <SettingsCard title="TMDB 设置" :accent="SCRAPE_SETTINGS_ACCENT">
        <template #head-aside>
          <p class="scrape-settings-tip">与「目录整理」共用同一套 TMDB 配置，修改后两边同步生效。</p>
          <TmdbHostsHelpTip />
        </template>
        <template #head-actions>
          <AppButton type="button" variant="secondary" size="sm" :disabled="tmdbTesting" @click="testTmdb">
            {{ tmdbTesting ? "测试中…" : "测试连通性" }}
          </AppButton>
        </template>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_api_key')">
          <template #info>
            <div class="settings-row__label">TMDB API Key</div>
          </template>
          <template #control>
            <AppInput
              v-model="settings.tmdb_api_key"
              type="password"
              placeholder="请填写 TMDB API Key（必填）"
              :ignore-autofill="true"
            />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('theporndb_key')">
          <template #info>
            <div class="settings-row__label">ThePornDB API Key</div>
            <SettingsHelpTooltip title="刮削欧美片站点的 API">
              补<strong>欧美片</strong>的简介 / 演员 / 时长 / 片商 / 标签。
              欧美片的剧情 JAVDB 基本不给（实测 471 部里 470 部缺简介），这一家是目前唯一补得上的。
              key 到 theporndb.net 注册后获取。
              <br /><br />
              <strong>留空 = 不修改</strong>。没配时只有欧美那一档少一家源，其它档不受影响。
            </SettingsHelpTooltip>
          </template>
          <template #control>
            <AppInput
              v-model="settings.theporndb_key"
              type="password"
              :placeholder="settings.theporndb_key_set ? '已配置（留空 = 不修改）' : '粘贴 ThePornDB 的 API Key'"
              :ignore-autofill="true"
            />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('flaresolverr_url')">
          <template #info>
            <div class="settings-row__label">FlareSolverr 地址</div>
            <SettingsHelpTooltip title="破解站点防护（Cloudflare 盾 / 年龄确认墙）">
              有些站点普通请求过不去：<code>freejavbt</code> 要过 Cloudflare 盾、
              <code>dmm</code> 要过年龄确认墙。填上 FlareSolverr 的地址后，刮削会在
              「直连 → 代理」都失败时**最后**请它用真浏览器去取（一次几十秒，所以是最后一段）。
              <br /><br />
              格式：<code>http://主机:8191</code>（FlareSolverr 默认端口 8191）。
              <strong>留空 = 不启用</strong> —— 那两家站补不上，其它档不受影响。
            </SettingsHelpTooltip>
          </template>
          <template #control>
            <AppInput v-model="settings.flaresolverr_url" placeholder="http://127.0.0.1:8191" :ignore-autofill="true" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('translate_enabled')">
          <template #info>
            <div class="settings-row__label">AI 翻译简介 / 标题</div>
            <SettingsHelpTooltip title="把日文翻成中文">
              刮削抓回来的简介绝大多数是<strong>日文</strong>（DMM / avsox 那些站的原文），标题也大面积是日文。
              打开这一项后，刮削会在补到元数据之后用 AI 翻成中文再落库。
              <br /><br />
              <strong>已经是中文的不会翻</strong>（按汉字/假名占比判），免得白花额度还把通顺的中文改写坏。
              <br /><br />
              AI 本身（地址 / 密钥 / 模型）在<strong>「辅助工具 → 增强工具 → AI 辅助识别」</strong>
              那张卡片里配 —— 这里只管「刮削时要不要用它翻」。
              <br /><br />
              ⚠️ 那个卡片要<strong>同时</strong>开着开关、并填好一条配置（API 地址 / 模型 / API Key，
              且勾上「默认激活」），这里才会真的翻；否则静默不生效。
            </SettingsHelpTooltip>
          </template>
          <template #control>
            <SettingsBoolSegment v-model="settings.translate_enabled" label="启用" />
          </template>
        </SettingsRow>
        <SettingsRow v-if="settings.translate_enabled" :show-changed-badge="true" :changed="isFieldChanged('translate_fields')">
          <template #info>
            <div class="settings-row__label">翻译哪些字段</div>
            <SettingsHelpTooltip title="简介 / 标题">
              <strong>简介</strong>：译文覆盖简介，<strong>原文留在库里</strong>（`summary_original` 那一列）——
              翻错了或想换模型重翻时，原文是唯一依据。<br /><br />
              <strong>标题</strong>：译文写进「中文标题」那一栏（`title_zh`），
              <strong>不动</strong> JAVDB 那行原文 —— nfo 里会自动取中文标题。
            </SettingsHelpTooltip>
          </template>
          <template #control>
            <!-- ⚠️ `:missing-checked="false"`：缺项 = **不翻**（与「番号元数据六项」
                 那条默认全开相反 —— 这里是「没勾就不花额度」）。 -->
            <SettingsCheckboxRow
              v-model="translateFieldChecks"
              :options="translateFieldOptions"
              :missing-checked="false"
            />
          </template>
        </SettingsRow>
        <SettingsRow v-if="settings.translate_enabled" :show-changed-badge="true" :changed="isFieldChanged('translate_prompt')">
          <template #info>
            <div class="settings-row__label">翻译提示词</div>
            <SettingsHelpTooltip title="自定义提示词（可留空）">
              留空 = 用内置那份（「翻成简体中文、只输出译文、已是中文则原样返回、保留人名番号」）。
              <br /><br />
              想加要求就写在这里，例如「翻成台湾用语」或「人名保留原文不音译」。
              <br /><br />
              ⚠️ 别去掉「<strong>只输出译文</strong>」那条 —— 模型多说一句「以下是翻译：」
              就会永久留在你的 nfo 里。
            </SettingsHelpTooltip>
          </template>
          <template #control>
            <textarea
              v-model="settings.translate_prompt"
              class="scrape-settings__textarea"
              rows="4"
              placeholder="留空 = 用内置提示词"
            />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_language')">
          <template #info>
            <div class="settings-row__label">搜索语言</div>
          </template>
          <template #control>
            <AppSelect v-model="settings.tmdb_language" :options="tmdbLanguageOptions" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_api_host')">
          <template #info>
            <div class="settings-row__label">
              <span>TMDB API 主域名</span>
              <SettingsHelpTooltip title="TMDB API 主域名说明">
                <p>自建反代时填写主域名，程序自动补 /3；默认使用官方地址。</p>
                <p>国内网络可尝试填写 https://api.tmdb.org（与官方域名解析到不同节点，部分地区可直连，效果因网络环境而异）。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_api_host" placeholder="https://api.themoviedb.org" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_image_host')">
          <template #info>
            <div class="settings-row__label">
              <span>TMDB 图片主域名</span>
              <SettingsHelpTooltip title="TMDB 图片主域名说明">
                <p>自建反代时填写主域名，程序自动补 /t/p；默认使用官方地址。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model="settings.tmdb_image_host" placeholder="https://image.tmdb.org" />
          </template>
        </SettingsRow>
        <SettingsRow :show-changed-badge="true" :changed="isFieldChanged('tmdb_request_interval_ms')">
          <template #info>
            <div class="settings-row__label">
              <span>请求间隔（毫秒）</span>
              <SettingsHelpTooltip title="请求间隔说明">
                <p>连续请求 TMDB 的最小间隔，过小可能触发限流。</p>
              </SettingsHelpTooltip>
            </div>
          </template>
          <template #control>
            <AppInput v-model.number="settings.tmdb_request_interval_ms" type="number" min="200" max="5000" />
          </template>
        </SettingsRow>
      </SettingsCard>
    </template>
  </div>
</template>

<style scoped>
.scrape-settings-tip {
  margin: 0;
  padding: 0;
  color: var(--text-muted);
  font-size: 12px;
  line-height: 1.45;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.scrape-settings__textarea {
  width: 420px;
  max-width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm, 8px);
  background: var(--surface);
  color: var(--text);
  font: inherit;
  line-height: 1.5;
  resize: vertical;
}
.scrape-settings__textarea:focus {
  outline: none;
  border-color: var(--brand);
}
.settings-row__label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
}
</style>
