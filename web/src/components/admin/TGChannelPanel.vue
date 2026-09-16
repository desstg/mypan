<script setup lang="ts">
import { onMounted, reactive, ref } from "vue";
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
  fetchTGChannels,
  testTGChannel,
  updateTGChannel,
  type TGChannelInput,
} from "@/api/tgSubscribe";
import { confirm } from "@/composables/useConfirm";
import { toast } from "@/composables/useToast";
import type { TGChannel } from "@/types/tg-subscribe";

const emit = defineEmits<{ changed: [] }>();

const loading = ref(false);
const channels = ref<TGChannel[]>([]);

const dialogOpen = ref(false);
const saving = ref(false);
const editingId = ref<number | null>(null);
const form = reactive<TGChannelInput>({ chat: "", remark: "", level: 10, enabled: true });

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
  form.chat = channel.chat_id;
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
      await createTGChannel({ ...form });
      toast.success("频道已添加");
    } else {
      await updateTGChannel(editingId.value, { ...form });
      toast.success("频道已更新");
    }
    dialogOpen.value = false;
    await load();
    emit("changed");
  } catch (error) {
    // 校验失败的原因（bot 不在频道、不是管理员等）由后端给出，直接透传最有用。
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
    toast.success(`「${probe.title || channel.chat_id}」校验通过，Bot ${probe.bot_name} 是管理员`);
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

onMounted(load);

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

      <div v-if="channels.length" class="admin-panel-table-wrap">
        <table class="admin-table">
          <thead>
            <tr>
              <th style="width: 30%">频道</th>
              <th style="width: 18%">Chat ID</th>
              <th style="width: 90px">优先级</th>
              <th style="width: 90px">状态</th>
              <th style="width: 160px">最近消息</th>
              <th style="width: 80px">命中</th>
              <th style="width: 180px">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="channel in channels" :key="channel.id">
              <td>
                <div>{{ channel.remark || channel.title || "（未命名）" }}</div>
                <div v-if="channel.last_error" class="tg-record__hash">{{ channel.last_error }}</div>
              </td>
              <td class="tg-record__hash">{{ channel.chat_id }}</td>
              <td>{{ channel.level }}</td>
              <td>
                <AdminStatusPill :tone="statusTone(channel)">{{ statusText(channel) }}</AdminStatusPill>
              </td>
              <td>{{ timeText(channel.last_post_at) }}</td>
              <td>{{ channel.matched_count }}</td>
              <td>
                <div class="admin-table__actions">
                  <AdminEnableToggle
                    :enabled="channel.enabled"
                    aria-label="启用频道"
                    @enable="toggleEnabled(channel, $event)"
                  />
                  <AdminTableActionBtn icon="rotate" title="重新校验 Bot 权限" @click="test(channel)" />
                  <AdminTableActionBtn icon="edit" title="编辑" @click="openEdit(channel)" />
                  <AdminTableActionBtn icon="delete" title="删除" danger @click="remove(channel)" />
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
        description="先把 Bot 拉进频道并设为管理员，再回到这里添加。"
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
            <AppInput v-model="form.chat" placeholder="@channelname 或 -1001234567890" />
            <p class="tg-form__hint">
              公开频道填 <code>@用户名</code> 或 <code>https://t.me/xxx</code>；私有频道必须填
              <code>-100</code> 开头的数字 ID。校验通过后会固化数字 ID —— 频道改名后用户名会失效。
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
          Bot 必须是频道<b>管理员</b>才能收到消息 —— 只是成员收不到，而且 Telegram 不会报错。
          另外频道本身要允许 Bot 读取消息，隐私模式不影响频道消息。
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
