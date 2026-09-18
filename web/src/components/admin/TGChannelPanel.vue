<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppInput from "@/components/base/AppInput.vue";
import AppModal from "@/components/base/AppModal.vue";
import AdminEmptyState from "@/components/admin/AdminEmptyState.vue";
import AdminEnableToggle from "@/components/admin/AdminEnableToggle.vue";
import AdminStatusPill from "@/components/admin/AdminStatusPill.vue";
import AdminTableActionBtn from "@/components/admin/AdminTableActionBtn.vue";
import SettingsCard from "@/components/admin/SettingsCard.vue";
import SettingsHelpTooltip from "@/components/admin/SettingsHelpTooltip.vue";
import { getApiErrorMessage } from "@/api/client";
import {
  createTGChannel,
  deleteTGChannel,
  fetchRecommendedTGChannels,
  fetchTGChannels,
  testTGChannel,
  updateTGChannel,
  type TGChannelInput,
} from "@/api/tgSubscribe";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import type { TGChannel, TGRecommendedChannel } from "@/types/tg-subscribe";
import { formatProbeCounts, undeliverableProbeCounts } from "@/types/tg-subscribe";

const emit = defineEmits<{ changed: [] }>();

const loading = ref(false);
const channels = ref<TGChannel[]>([]);

// 旧版按数字 ID 添加的频道没有用户名 —— 网页预览只能按用户名抓，这类记录
// 会一直静静地什么都不做，必须在界面上点名。
const legacyChannels = computed(() => channels.value.filter((c) => !c.username.trim()));

const dialogOpen = ref(false);
const saving = ref(false);
const editingId = ref<number | null>(null);
const form = reactive<TGChannelInput>({ chat: "", remark: "", level: 10, enabled: true });

// 推荐频道的**补加**入口。
//
// 内置推荐频道在首次启动时已经自动入库了（后端 SeedRecommendedChannels），
// 所以这张表正常情况下就是一份普通的频道列表。这里要处理的只剩一种残留情况：
// 那次播种因为没配代理而一条都没加成功 —— 清单里的这几条没进库，
// 用户配好代理后可以在这里手动补加，补上就走与手填完全相同的保存路径。
const recommended = ref<TGRecommendedChannel[]>([]);
const pendingRecommended = computed(() => recommended.value.filter((r) => !r.added));

// 正在补加哪一条（按用户名记，因为此时它还没有 id）。同时只允许一条在飞，
// 避免连点把几次首次回填叠在一起 —— 每次回填都要翻几页 t.me。
const addingRecommended = ref("");

async function loadRecommended() {
  try {
    recommended.value = await fetchRecommendedTGChannels();
  } catch {
    // 清单拿不到不该拦住频道管理 —— 静默降级成「没有待补加的推荐」。
    recommended.value = [];
  }
}

/** 补加一条推荐频道：走的路径与弹窗里手填完全一致。 */
async function addRecommended(item: TGRecommendedChannel) {
  if (item.added || addingRecommended.value) return;
  addingRecommended.value = item.username;
  try {
    const created = await createTGChannel({
      chat: "@" + item.username,
      remark: item.remark,
      level: 10,
      enabled: true,
    });
    toast.success(
      created.backfill_posts
        ? `「${item.remark}」已添加，并回填了 ${created.backfill_posts} 条历史帖子`
        : `「${item.remark}」已添加`,
    );
    await load();
    await loadRecommended();
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, `补加「${item.remark}」失败`));
  } finally {
    addingRecommended.value = "";
  }
}

async function load() {
  loading.value = true;
  try {
    channels.value = await fetchTGChannels();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "加载 TG 频道失败"));
  } finally {
    loading.value = false;
  }
}

function openCreate() {
  editingId.value = null;
  form.chat = "";
  form.remark = "";
  form.level = 10;
  form.enabled = true;
  dialogOpen.value = true;
}

function openEdit(channel: TGChannel) {
  editingId.value = channel.id;
  // 回填 @用户名 而不是数字 ID：抓取用的是用户名，而且旧版按数字 ID 添加的
  // 频道没有用户名 —— 正好让用户在这里顺手补上。
  form.chat = channel.username ? `@${channel.username}` : channel.chat_id;
  form.remark = channel.remark;
  form.level = channel.level;
  form.enabled = channel.enabled;
  dialogOpen.value = true;
}

async function submit() {
  if (!form.chat.trim()) {
    toast.warning("请填写频道地址");
    return;
  }
  saving.value = true;
  try {
    if (editingId.value === null) {
      // 后端在校验通过后会同步做一次首次回填，所以这次请求会久一点
      // （翻几页就发几次请求，页间还有限速间隔）。按钮文案已改成「校验中…」。
      const created = await createTGChannel({ ...form });
      toast.success(
        created.backfill_posts
          ? `频道已添加，并回填了 ${created.backfill_posts} 条历史帖子`
          : "频道已添加",
      );
    } else {
      await updateTGChannel(editingId.value, { ...form });
      toast.success("频道已更新");
    }
    dialogOpen.value = false;
    await load();
    emit("changed");
  } catch (error) {
    // 校验失败的原因（频道不存在、不是公开频道、网络不通等）由后端给出，直接透传最有用。
    toast.error(getApiErrorMessage(error, "保存频道失败"));
  } finally {
    saving.value = false;
  }
}

async function toggleEnabled(channel: TGChannel, enabled: boolean) {
  try {
    await updateTGChannel(channel.id, {
      chat: channel.chat_id,
      remark: channel.remark,
      level: channel.level,
      enabled,
    });
    channel.enabled = enabled;
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "切换频道状态失败"));
  }
}

async function test(channel: TGChannel) {
  try {
    const probe = await testTGChannel(channel.id);
    // 体检结果：把「这个频道抓得到什么」摊开说。
    // 只说「可用」会掩盖「帖子数一直涨、产出一直是 0」这类静默失败 ——
    // 实测两个频道分别是「下载地址在机器人深链后面」与「正文链接全在中转站上」。
    const counts = formatProbeCounts(probe.resource_counts);
    toast.success(
      `「${probe.title || channel.chat_id}」可用（@${probe.username}，最近帖子 #${probe.latest_message_id}）` +
        (counts ? `\n最近 ${probe.post_count} 条：${counts}` : ""),
    );
    // 识别到了但投不出去的类型要单独提一句，否则用户会以为订阅上就该有产出。
    const undeliverable = undeliverableProbeCounts(probe.resource_counts);
    if (undeliverable.length) {
      toast.info(
        `最近 ${probe.post_count} 条里有 ${undeliverable
          .map((c) => `${c.label} ${c.count}`)
          .join("、")} —— 当前版本只能识别、不能投递。`,
      );
    }
    // 校验时顺带诊断出来的兼容性问题（例如整页都抓不到下载链接），
    // 用户添加频道的那一刻是唯一会读提示的时刻，必须现在说。
    if (probe.warnings?.length) {
      toast.warning(probe.warnings.join(" "));
    }
    await load();
  } catch (error) {
    toast.error(getApiErrorMessage(error, "频道校验失败"));
    await load();
  }
}

async function remove(channel: TGChannel) {
  try {
    // confirm() 在用户取消时抛异常，所以放在 try 里 —— 这是项目里的既有约定。
    await confirm({
      title: "删除频道",
      message: `确定不再监听「${channel.remark || channel.title || channel.chat_id}」吗？已匹配的记录会保留。`,
      confirmText: "删除",
      danger: true,
      icon: "trash",
    });
  } catch {
    return;
  }
  try {
    await deleteTGChannel(channel.id);
    toast.success("频道已删除");
    await load();
    emit("changed");
  } catch (error) {
    toast.error(getApiErrorMessage(error, "删除频道失败"));
  }
}

function statusTone(channel: TGChannel) {
  switch (channel.status) {
    case "ok":
      return "success" as const;
    case "error":
      return "danger" as const;
    default:
      return "muted" as const;
  }
}

function statusText(channel: TGChannel) {
  switch (channel.status) {
    case "ok":
      return "正常";
    case "error":
      return "异常";
    default:
      return "未校验";
  }
}

function timeText(raw?: string) {
  if (!raw) return "—";
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString();
}

onMounted(() => {
  void load();
  void loadRecommended();
});

defineExpose({ load });
</script>

<template>
  <div class="tg-panel">
    <SettingsCard title="TG 频道" accent="var(--brand)">
      <template #head-aside>
        <span>把这些频道里的资源消息抓下来做匹配。同一份频道列表供全部订阅共用。</span>
      </template>
      <template #head-actions>
        <AppButton type="button" variant="primary" size="sm" @click="openCreate">+ 添加频道</AppButton>
      </template>

      <!-- 旧版允许直接填数字 ID 添加频道，那种记录没有用户名，网页预览抓不了。
           不提示的话它会一直静静地什么都不做。 -->
      <p v-if="legacyChannels.length" class="tg-form__hint tg-form__hint--warn">
        有 {{ legacyChannels.length }} 个频道是旧版按数字 ID 添加的，没有公开地址，抓不到任何东西。
        请编辑它们并填入 <code>@用户名</code> 或 <code>https://t.me/xxx</code>。
      </p>

      <!-- 正常情况下这里什么都看不到：内置推荐频道首次启动就已经入库，
           就是上面那些普通行、能编辑能删除。只有那次播种没成功（没配代理）
           才会剩几条在这里，让用户配好代理后手动补上。 -->
      <p v-if="pendingRecommended.length" class="tg-form__hint" style="margin-top: 0">
        下面 {{ pendingRecommended.length }} 个是内置推荐频道，本该在首次启动时自动添加，
        但当时没成功（通常是还没有配代理）。可以点「补加」手动添上，之后它们与普通频道完全等同。
      </p>

      <div v-if="channels.length || pendingRecommended.length" class="admin-panel-table-wrap">
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 30%">频道</th>
              <th style="width: 18%">公开地址</th>
              <th style="width: 90px">优先级</th>
              <th style="width: 90px">状态</th>
              <th style="width: 160px">最近消息</th>
              <th style="width: 80px">帖子数</th>
              <th style="width: 70px">产出</th>
              <th style="width: 180px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="channel in channels" :key="channel.id">
              <td>
                <div>{{ channel.remark || channel.title || "（未命名）" }}</div>
                <div v-if="channel.last_error" class="tg-record__hash">{{ channel.last_error }}</div>
              </td>
              <td class="tg-record__hash">
                {{ channel.username ? `@${channel.username}` : channel.chat_id }}
              </td>
              <td>{{ channel.level }}</td>
              <td>
                <AdminStatusPill :tone="statusTone(channel)">{{ statusText(channel) }}</AdminStatusPill>
              </td>
              <td>{{ timeText(channel.last_post_at) }}</td>
              <td>{{ channel.matched_count }}</td>
              <td>
                <!-- 抓了 20 条却一条都没产出，就是「这个频道抓不到内容」的信号。
                     具体是哪种原因（下载地址在机器人深链后面 / 正文链接都在中转站上 /
                     用「点击复制」按钮发资源）由「测试」按钮的体检结论给出。 -->
                <span :class="{ 'tg-channel__zero': channel.matched_count > 0 && channel.record_count === 0 }">
                  {{ channel.record_count }}
                </span>
              </td>
              <td>
                <div class="admin-table__actions">
                  <AdminEnableToggle
                    :enabled="channel.enabled"
                    aria-label="启用频道"
                    @enable="toggleEnabled(channel, $event)"
                  />
                  <AdminTableActionBtn icon="rotate" title="重新校验频道" @click="test(channel)" />
                  <AdminTableActionBtn icon="edit" title="编辑" @click="openEdit(channel)" />
                  <AdminTableActionBtn icon="delete" title="删除" danger @click="remove(channel)" />
                </div>
              </td>
            </tr>
            <!-- 播种没成功时残留的推荐：整行淡化，右侧只留一个「补加」。 -->
            <tr
              v-for="item in pendingRecommended"
              :key="'reco-' + item.username"
              class="tg-channel-row--reco"
            >
              <td>
                <div>
                  {{ item.remark }}
                  <span class="tg-reco__tag">未添加</span>
                </div>
                <div class="tg-reco__desc">{{ item.summary }}</div>
              </td>
              <td class="tg-record__hash">@{{ item.username }}</td>
              <td>—</td>
              <td><AdminStatusPill tone="muted">未添加</AdminStatusPill></td>
              <td>—</td>
              <td>—</td>
              <td>—</td>
              <td>
                <div class="admin-table__actions">
                  <AppButton
                    type="button"
                    variant="secondary"
                    size="sm"
                    :disabled="addingRecommended !== ''"
                    @click="addRecommended(item)"
                  >
                    {{ addingRecommended === item.username ? "校验中…" : "补加" }}
                  </AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <AdminEmptyState
        v-else-if="!loading"
        icon="📡"
        title="还没有添加任何 TG 频道"
        description="填一个公开频道的 @用户名 或 t.me 链接即可 —— 不需要 Bot，也不用把任何账号拉进频道。"
      />
    </SettingsCard>

    <AppModal
      :open="dialogOpen"
      :title="editingId === null ? '添加 TG 频道' : '编辑 TG 频道'"
      size="md"
      @close="dialogOpen = false"
    >
      <div class="tg-form">
        <div class="tg-form__field">
          <label class="tg-form__label">频道地址</label>
          <div class="tg-form__value">
            <AppInput v-model="form.chat" placeholder="@channelname 或 https://t.me/xxx" />
            <p class="tg-form__hint">
              只支持<b>公开频道</b>：填 <code>@用户名</code> 或 <code>https://t.me/xxx</code>。
              私有频道与邀请链接（<code>t.me/+xxx</code>）没有公开网页预览，无法订阅。
              校验通过后会记录频道标题与数字 ID，用来识别「频道改名」和「用户名被回收」。
            </p>
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">备注</label>
          <div class="tg-form__value">
            <AppInput v-model="form.remark" placeholder="便于辨认，留空则显示频道标题" />
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">
            优先级
            <SettingsHelpTooltip title="优先级说明">
              <p>0–100，越大越优先。</p>
              <p>同一条资源被多个频道转发时，高优先级频道先占住去重位，只会推送一次。</p>
            </SettingsHelpTooltip>
          </label>
          <div class="tg-form__value" style="max-width: 140px">
            <AppInput v-model="form.level" type="number" placeholder="10" />
          </div>
        </div>
        <div class="tg-form__field">
          <label class="tg-form__label">启用</label>
          <div class="tg-form__value">
            <AdminEnableToggle
              :enabled="form.enabled"
              aria-label="启用频道"
              @enable="form.enabled = $event"
            />
          </div>
        </div>
        <p class="tg-form__hint">
          通过 Telegram 的公开网页预览抓取，不需要 Bot，也不需要把任何账号拉进频道。
          保存时会跑一次体检，报告最近一页帖子抓到了哪些资源 —— 抓不到内容的情况有好几种
          （下载地址在机器人深链后面、正文链接都在中转站上、用「点击复制」按钮发资源），
          体检结论会直接说明是哪种。
        </p>
      </div>

      <template #footer>
        <AppButton type="button" variant="secondary" @click="dialogOpen = false">取消</AppButton>
        <AppButton type="button" variant="primary" :disabled="saving" @click="submit">
          {{ saving ? "校验中…" : "保存" }}
        </AppButton>
      </template>
    </AppModal>
  </div>
</template>
