<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AppSelect from "@/components/base/AppSelect.vue";
import OrderedTagList from "@/components/base/OrderedTagList.vue";
import SettingsBoolSegment from "@/components/admin/SettingsBoolSegment.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  createTGQualityProfile,
  deleteTGQualityProfile,
  fetchTGQualityProfiles,
  previewTGQuality,
  updateTGQualityProfile,
  type TGQualityProfileInput,
} from "@/api/tgSubscribe";
import { confirm } from "@/composables/useConfirm";
import { useSettingsPageDirty } from "@/composables/useSettingsPageDirty";
import { bindSettingsPanelExpose } from "@/composables/useSettingsForm";
import { toast } from "@/composables/useToast";
import type { TGQualityConfig, TGQualityPreview, TGQualityProfile } from "@/types/tg-subscribe";

const emit = defineEmits<{ changed: [] }>();

const RESOLUTION_SUGGESTIONS = ["2160p", "1440p", "1080p", "720p", "480p"];
const SOURCE_SUGGESTIONS = ["Remux", "BluRay", "WEB-DL", "WEBRip", "HDTV", "DVD"];
const CODEC_SUGGESTIONS = ["AV1", "H.265", "H.264", "VP9", "MPEG-2"];

function emptyConfig(): TGQualityConfig {
  return {
    prefer_resolution: ["2160p", "1080p", "720p"],
    prefer_codec: ["AV1", "H.265", "H.264"],
    prefer_source: ["Remux", "BluRay", "WEB-DL", "HDTV"],
    exclude_keywords: ["CAM", "TS", "枪版", "抢先", "预告", "Trailer", "Sample"],
    min_resolution: "",
    max_size_gb: 0,
    weights: { resolution: 50, source: 30, codec: 20 },
  };
}

const loading = ref(false);
const saving = ref(false);
const profiles = ref<TGQualityProfile[]>([]);
const selectedId = ref<number | null>(null);

/** 草稿与基线用于脏判断：只比 config + name，避免 selectedId 这类界面状态干扰。 */
const draft = reactive<TGQualityProfileInput>({ name: "", config: emptyConfig(), is_default: false });
const baseline = ref("");

const nameDialogOpen = ref(false);
const newName = ref("");

// 试跑
const tryText = ref("");
const trying = ref(false);
const preview = ref<TGQualityPreview | null>(null);
let tryTimer: ReturnType<typeof setTimeout> | null = null;

const selected = computed(() => profiles.value.find((p) => p.id === selectedId.value) ?? null);
const isDirty = computed(() => JSON.stringify(snapshot()) !== baseline.value);

function snapshot() {
  return { name: draft.name, config: draft.config, is_default: draft.is_default };
}

async function load(preferId?: number) {
  loading.value = true;
  try {
    profiles.value = await fetchTGQualityProfiles();
    const target =
      (preferId !== undefined ? profiles.value.find((p) => p.id === preferId) : null) ??
      profiles.value.find((p) => p.id === selectedId.value) ??
      profiles.value[0] ??
      null;
    selectProfile(target);
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载画质方案失败"));
  } finally {
    loading.value = false;
  }
}

function selectProfile(profile: TGQualityProfile | null) {
  if (!profile) {
    selectedId.value = null;
    return;
  }
  selectedId.value = profile.id;
  draft.name = profile.name;
  // 深拷贝：draft 是本地草稿，不能和列表里的对象共享引用。
  draft.config = {
    ...profile.config,
    prefer_resolution: [...(profile.config.prefer_resolution ?? [])],
    prefer_codec: [...(profile.config.prefer_codec ?? [])],
    prefer_source: [...(profile.config.prefer_source ?? [])],
    exclude_keywords: [...(profile.config.exclude_keywords ?? [])],
    weights: { ...(profile.config.weights ?? {}) },
  };
  draft.is_default = profile.is_default;
  baseline.value = JSON.stringify(snapshot());
}

async function save() {
  if (selectedId.value === null) return;
  saving.value = true;
  try {
    await updateTGQualityProfile(selectedId.value, {
      name: draft.name,
      config: draft.config,
      is_default: draft.is_default,
    });
    toast.success("画质方案已保存");
    baseline.value = JSON.stringify(snapshot());
    await load(selectedId.value);
    emit("changed");
    scheduleTry();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "保存画质方案失败"));
  } finally {
    saving.value = false;
  }
}

function revert() {
  selectProfile(selected.value);
  toast.info("已撤销未保存的改动");
}

function openCreate() {
  newName.value = "";
  nameDialogOpen.value = true;
}

async function confirmCreate() {
  const name = newName.value.trim();
  if (!name) {
    toast.warning("请填写方案名称");
    return;
  }
  try {
    const { id } = await createTGQualityProfile({
      name,
      config: emptyConfig(),
      is_default: false,
    });
    nameDialogOpen.value = false;
    toast.success("方案已创建");
    await load(id);
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "创建画质方案失败"));
  }
}

/** 复制当前方案 —— 调优时比从零建一份快得多。 */
async function duplicate() {
  if (!selected.value) return;
  try {
    const { id } = await createTGQualityProfile({
      name: `${selected.value.name} 副本`,
      config: draft.config,
      is_default: false,
    });
    toast.success("已复制为新方案");
    await load(id);
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "复制画质方案失败"));
  }
}

async function removeProfile() {
  if (!selected.value || selected.value.is_default) return;
  try {
    await confirm({
      title: "删除画质方案",
      message: `确认删除「${selected.value.name}」？正在使用它的订阅会回落到默认方案。`,
      confirmText: "删除",
      danger: true,
      icon: "trash",
    });
  } catch {
    return;
  }
  try {
    await deleteTGQualityProfile(selected.value.id);
    toast.success("方案已删除");
    selectedId.value = null;
    await load();
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "删除画质方案失败"));
  }
}

/** 试跑：400ms 防抖，避免每敲一个字就打一次后端。 */
function scheduleTry() {
  if (tryTimer) clearTimeout(tryTimer);
  const raw = tryText.value.trim();
  if (!raw) {
    preview.value = null;
    return;
  }
  tryTimer = setTimeout(runTry, 400);
}

async function runTry() {
  const raw = tryText.value.trim();
  if (!raw) return;
  trying.value = true;
  try {
    preview.value = await previewTGQuality(raw, selectedId.value ?? 0);
  } catch (error) {
    preview.value = null;
    toast.error(getApiErrorMessage(error, "试跑失败"));
  } finally {
    trying.value = false;
  }
}

watch(tryText, scheduleTry);
onMounted(() => void load());

useSettingsPageDirty(isDirty, revert);

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
    <SettingsCard title="画质方案" accent="var(--brand)">
      <template #head-aside>
        <span>决定同一部影片出现多个版本时，推哪一个。</span>
      </template>
      <template #head-actions>
        <div class="tg-panel__toolbar">
          <AppButton type="button" variant="secondary" size="sm" @click="openCreate">新建</AppButton>
          <AppButton
            type="button"
            variant="secondary"
            size="sm"
            :disabled="!selected"
            @click="duplicate"
          >
            复制
          </AppButton>
          <AppButton
            type="button"
            variant="ghost"
            size="sm"
            :disabled="!selected || selected.is_default"
            @click="removeProfile"
          >
            删除
          </AppButton>
        </div>
      </template>

      <div v-if="profiles.length" class="tg-form">
        <div class="tg-form__field">
          <label class="tg-form__label">方案</label>
          <div class="tg-form__value">
            <AppSelect
              :model-value="selectedId ?? ''"
              :options="profiles.map((p) => ({ value: p.id, label: p.is_default ? `${p.name}（默认）` : p.name }))"
              @update:model-value="selectProfile(profiles.find((p) => p.id === $event) ?? null)"
            />
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">方案名称</label>
          <div class="tg-form__value">
            <AppInput v-model="draft.name" placeholder="例如：优先 4K / 只要原盘" />
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">设为默认</label>
          <div class="tg-form__value">
            <SettingsBoolSegment v-model="draft.is_default" label="设为默认方案" />
            <p class="tg-form__hint">新订阅没有单独指定方案时，用这个。</p>
          </div>
        </div>
      </div>
    </SettingsCard>

    <template v-if="selected">
      <SettingsCard title="优先级" accent="var(--brand)">
        <template #head-aside>
          <span>从上到下即从优到劣，用 ↑↓ 调序。改动会立刻影响选优结果。</span>
        </template>

        <div class="tg-form">
          <div class="tg-form__field tg-form__field--stack">
            <label class="tg-form__label">
              分辨率
              <SettingsHelpTooltip title="分辨率优先级">
                <p>按顺序取第一个命中的档位计分：第 1 名 100 分，之后线性递减，不在列表里记 0 分。</p>
              </SettingsHelpTooltip>
            </label>
            <div class="tg-form__value">
              <OrderedTagList
                v-model="draft.config.prefer_resolution"
                :suggestions="RESOLUTION_SUGGESTIONS"
                placeholder="例如 1080p"
                empty-hint="一个都没配时，分辨率不参与打分"
              />
            </div>
          </div>

          <div class="tg-form__field tg-form__field--stack">
            <label class="tg-form__label">片源</label>
            <div class="tg-form__value">
              <OrderedTagList
                v-model="draft.config.prefer_source"
                :suggestions="SOURCE_SUGGESTIONS"
                placeholder="例如 Remux"
                empty-hint="一个都没配时，片源不参与打分"
              />
            </div>
          </div>

          <div class="tg-form__field tg-form__field--stack">
            <label class="tg-form__label">编码</label>
            <div class="tg-form__value">
              <OrderedTagList
                v-model="draft.config.prefer_codec"
                :suggestions="CODEC_SUGGESTIONS"
                placeholder="例如 H.265"
                empty-hint="一个都没配时，编码不参与打分"
              />
            </div>
          </div>

          <div class="tg-form__field">
            <label class="tg-form__label">
              权重
              <SettingsHelpTooltip title="权重说明">
                <p>三项的相对权重，只有比例有意义。默认 50 / 30 / 20。</p>
                <p>把分辨率调高，就更容易为了清晰度容忍差一点的片源。</p>
              </SettingsHelpTooltip>
            </label>
            <div class="tg-form__value" style="display: flex; gap: 10px; max-width: 340px">
              <AppInput v-model.number="draft.config.weights.resolution" type="number" placeholder="50" />
              <AppInput v-model.number="draft.config.weights.source" type="number" placeholder="30" />
              <AppInput v-model.number="draft.config.weights.codec" type="number" placeholder="20" />
            </div>
          </div>
        </div>
      </SettingsCard>

      <SettingsCard title="硬门槛" accent="var(--brand)">
        <template #head-aside>
          <span>不满足就直接丢掉，不参与选优。</span>
        </template>

        <div class="tg-form">
          <div class="tg-form__field">
            <label class="tg-form__label">最低分辨率</label>
            <div class="tg-form__value" style="max-width: 200px">
              <AppSelect
                :model-value="draft.config.min_resolution"
                :options="[
                  { value: '', label: '不限' },
                  ...RESOLUTION_SUGGESTIONS.map((r) => ({ value: r, label: r })),
                ]"
                @update:model-value="draft.config.min_resolution = String($event)"
              />
              <p class="tg-form__hint">
                设了门槛之后，认不出清晰度的资源也会被拦下 —— 宁可不推，也不把 480p 当成 1080p。
              </p>
            </div>
          </div>

          <div class="tg-form__field">
            <label class="tg-form__label">
              最大体积
              <SettingsHelpTooltip title="体积说明">
                <p>单位 GB，填 0 表示不限。</p>
                <p>磁力链里的体积参数由发布者填写，经常不准 —— 所以体积未知的资源不会被这条拦下。</p>
              </SettingsHelpTooltip>
            </label>
            <div class="tg-form__value" style="max-width: 140px">
              <AppInput v-model.number="draft.config.max_size_gb" type="number" placeholder="0" />
            </div>
          </div>

          <div class="tg-form__field tg-form__field--stack">
            <label class="tg-form__label">
              排除词
              <SettingsHelpTooltip title="排除词说明">
                <p>发布名里出现任意一个词就直接丢掉，不区分大小写。</p>
                <p>默认挡掉枪版、抢先版、预告片和样片。</p>
              </SettingsHelpTooltip>
            </label>
            <div class="tg-form__value">
              <OrderedTagList
                v-model="draft.config.exclude_keywords"
                placeholder="输入排除词后回车"
                empty-hint="一个都没配时不做关键词排除"
              />
            </div>
          </div>
        </div>

      </SettingsCard>

      <SettingsCard title="试跑" accent="var(--brand)">
        <template #head-aside>
          <span>粘一段发布名，看它会被解析成什么、得多少分、会命中哪个订阅。</span>
        </template>

        <AppInput
          v-model="tryText"
          placeholder="例如：Dune.2021.2160p.UHD.BluRay.Remux.HDR.HEVC.Atmos-SGNT"
        />

        <div v-if="trying" class="tg-preview">正在试跑…</div>

        <div v-else-if="preview" class="tg-preview">
          <div
            class="tg-preview__verdict"
            :class="preview.quality.passed ? 'tg-preview__verdict--pass' : 'tg-preview__verdict--fail'"
          >
            <span>{{ preview.quality.passed ? "✓ 通过门槛" : "✕ 被门槛拦下" }}</span>
            <span v-if="preview.quality.passed">· 画质分 {{ preview.quality.score }}</span>
          </div>
          <div class="tg-preview__reason">{{ preview.quality.reason }}</div>

          <div class="tg-preview__grid">
            <div class="tg-preview__item">
              <span class="tg-preview__key">片名候选</span>
              <span class="tg-preview__value">{{ preview.parsed.title_candidates?.join(" / ") || "未识别" }}</span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">年份</span>
              <span class="tg-preview__value">{{ preview.parsed.year ?? "未识别" }}</span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">季 / 集</span>
              <span class="tg-preview__value">
                {{ preview.parsed.season ?? "—" }} / {{ preview.parsed.episode ?? "—" }}
                <template v-if="preview.parsed.episode_end">–{{ preview.parsed.episode_end }}</template>
              </span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">整季包</span>
              <span class="tg-preview__value">{{ preview.parsed.is_batch ? "是" : "否" }}</span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">分辨率</span>
              <span class="tg-preview__value">{{ preview.parsed.resolution || "未识别" }}</span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">片源</span>
              <span class="tg-preview__value">{{ preview.parsed.source || "未识别" }}</span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">编码</span>
              <span class="tg-preview__value">{{ preview.parsed.video_codec || "未识别" }}</span>
            </div>
            <div class="tg-preview__item">
              <span class="tg-preview__key">像资源</span>
              <span class="tg-preview__value">{{ preview.parsed.looks_like ? "是" : "否（会被直接丢弃）" }}</span>
            </div>
          </div>

          <div v-if="preview.candidates.length" class="tg-preview__candidates">
            <div class="tg-preview__reason">候选订阅（阈值：接受 ≥ {{ preview.thresholds.accept }}）：</div>
            <div
              v-for="item in preview.candidates"
              :key="item.subscription_id"
              class="tg-preview__candidate"
              :class="{ 'tg-preview__candidate--accept': item.accepted }"
            >
              <span class="tg-preview__candidate-score">{{ item.match_score }}</span>
              <div class="tg-preview__candidate-main">
                <div class="tg-preview__candidate-title">
                  {{ item.title }}<template v-if="item.year"> ({{ item.year }})</template>
                </div>
                <div class="tg-preview__candidate-reason">{{ item.reason }}</div>
              </div>
            </div>
          </div>
          <div v-else class="tg-preview__reason">
            {{ preview.unmatched_hint || "没有匹配上任何订阅" }}
          </div>
        </div>
      </SettingsCard>
    </template>

    <AppModal :open="nameDialogOpen" title="新建画质方案" size="sm" @close="nameDialogOpen = false">
      <div class="tg-form">
        <div class="tg-form__field">
          <label class="tg-form__label">名称</label>
          <div class="tg-form__value">
            <AppInput v-model="newName" placeholder="例如：只推 4K 原盘" />
          </div>
        </div>
      </div>
      <template #footer>
        <AppButton type="button" variant="secondary" @click="nameDialogOpen = false">取消</AppButton>
        <AppButton type="button" variant="primary" @click="confirmCreate">创建</AppButton>
      </template>
    </AppModal>
  </div>
</template>
