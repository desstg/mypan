<script setup lang="ts">
import { computed, ref, watch } from "vue";
import AppButton from "@/components/base/AppButton.vue";
import AppPlainModal from "@/components/base/AppPlainModal.vue";
import { getApiErrorMessage } from "@/api/client";
import { fetchJavUserShares, followJavUser, pushJavMagnet, unfollowJavUser, javImageURL } from "@/api/jav";
import { toast } from "@/composables/useToast";
import type { JavCommentShare, JavUserShare } from "@/types/jav";
import "@/styles/jav.css";

/**
 * 「某位分享者提交的资源」弹窗。
 *
 * 两处入口共用：影片详情「评论区分享」里点分享者名字，以及订阅页「用户」档点用户卡。
 *
 * **形态照内网项目那张弹窗**：一行一部影片，每行下面挂着他在这一部贴出来的链接
 * （名字 + 体积 + 复制 + 推送 + 角标），分页给。不是卡片网格 —— 找分享者本来
 * 就是为了拿链接，卡片还得再点一次。
 *
 * 基座用 AppPlainModal，**不是**订阅页那张影片弹窗的 `.jav-modal`：
 *   - 那张弹窗的 z-index 是 2500，**压在详情抽屉（elevated = 10040）下面** ——
 *     从抽屉里点开会被整个盖住；
 *   - 而且它的 `.jav-modal__*` 是 JavSubscribePanel 的 <style scoped>，这里拿不到。
 * AppModal 走 <Teleport to="body"> + var(--z-modal)（10100），天然在上面，
 * 还自带 Esc 关闭与模态栈。
 */
const props = defineProps<{
  open: boolean;
  userId: number;
  /** 已知的用户名。拉回来后服务端会给更准的一份，这里只是让标题先有字。 */
  username?: string;
  /** 打开时他是不是已经关注过（由调用方那边的关注列表判定）。 */
  followed?: boolean;
}>();

const emit = defineEmits<{
  close: [];
  /** 点影片行 → 打开那一部的详情。 */
  openMovie: [id: string];
  /** 关注状态变了（订阅页那档要跟着刷新）。 */
  followChanged: [userId: number, followed: boolean];
}>();

const loading = ref(false);
const loadingMore = ref(false);
const error = ref("");
const items = ref<JavUserShare[]>([]);
const total = ref(0);
const hasMore = ref(false);
const page = ref(1);
const username = ref("");
const pushing = ref(false);
/** 本地关注的即时状态。从 props.followed 起步，点一下就翻转。 */
const followed = ref(false);
const followBusy = ref(false);

const title = computed(() => {
  const name = username.value || props.username || "这位用户";
  return followed.value ? `${name} 提交的资源 · 已关注` : `${name} 提交的资源`;
});

async function load(append = false) {
  if (!props.userId) return;
  if (append) loadingMore.value = true;
  else {
    loading.value = true;
    page.value = 1;
  }
  error.value = "";
  try {
    const res = await fetchJavUserShares(props.userId, page.value);
    const list = res.items ?? [];
    items.value = append ? [...items.value, ...list] : list;
    total.value = res.total ?? 0;
    hasMore.value = !!res.has_more;
    username.value = res.username ?? "";
  } catch (err) {
    // 失败要说清楚。静默成空列表会让人以为「这人一部都没分享过」——
    // 而这个模块已经反复栽在「空得看不出是失败还是真没有」上。
    if (!append) {
      error.value = getApiErrorMessage(err, "加载失败");
      items.value = [];
      total.value = 0;
      toast.error(error.value);
    } else {
      toast.error(getApiErrorMessage(err, "加载更多失败"));
    }
  } finally {
    loading.value = false;
    loadingMore.value = false;
  }
}

function loadMore() {
  page.value += 1;
  void load(true);
}

async function toggleFollow() {
  if (!props.userId || followBusy.value) return;
  followBusy.value = true;
  const next = !followed.value;
  try {
    if (next) {
      await followJavUser(props.userId, username.value || props.username || "");
    } else {
      await unfollowJavUser(props.userId);
    }
    followed.value = next;
    toast.success(next ? `已关注「${username.value || props.username}」` : "已取消关注");
    emit("followChanged", props.userId, next);
  } catch (err) {
    toast.error(getApiErrorMessage(err, next ? "关注失败" : "取消关注失败"));
  } finally {
    followBusy.value = false;
  }
}

async function copyLink(uri: string) {
  try {
    await navigator.clipboard.writeText(uri);
    toast.success("链接已复制");
  } catch {
    toast.error("复制失败，请手动选择文本");
  }
}

/** 推这一条到网盘。目标是「番号相关设置」里的默认目标（与详情页那颗推送同一套）。 */
async function pushLink(movie: JavUserShare, link: JavCommentShare) {
  pushing.value = true;
  try {
    const res = await pushJavMagnet(movie.id, {
      uri: link.uri,
      name: link.name,
      size_text: link.size_text,
    });
    toast[res.ok ? "success" : "error"](res.message || (res.ok ? "已提交" : "推送失败"));
  } catch (err) {
    toast.error(getApiErrorMessage(err, "推送失败"));
  } finally {
    pushing.value = false;
  }
}

watch(
  () => [props.open, props.userId],
  ([open]) => {
    if (open) {
      followed.value = !!props.followed;
      void load();
    }
  },
  { immediate: true },
);
</script>

<template>
  <!-- steady：面板定高、内容区自己滚（与订阅页那张影片弹窗同一套观感）。
       分享多的人一屏放不下，不定高的话面板会随内容长短「一下大一下小」。 -->
  <AppPlainModal :open="open" :title="title" size="wide" body-flush steady @close="emit('close')">
    <div class="jav-us">
      <!-- 关注 + 计数 + 那句免责说明。都是「关于这个人」的事，挤一行就够。 -->
      <div class="jav-us__bar">
        <AppButton
          size="sm"
          :variant="followed ? 'ghost' : 'primary'"
          :disabled="followBusy"
          @click="toggleFollow"
        >
          <i class="fas fa-heart" /> {{ followed ? "已关注" : "关注 TA" }}
        </AppButton>
        <span class="jav-us__count">
          用户ID {{ userId }} · 已加载 <b>{{ items.length }}</b> / {{ total }} 部影片
        </span>
        <!-- 这句必须留着：上游没有「按用户查他发过的评论」的接口，只能从本地
             已入库的评论里聚合。不说的话，用户会以为「这人只分享过这几部」。 -->
        <span class="jav-us__note">仅统计已经抓过评论的影片，不是他在站上分享过的全部。</span>
      </div>

      <!-- 只有这一块滚：关注键与那句说明留在上面不动，
           翻到第几页都看得见「这是谁、只有抓过评论的片」。 -->
      <div class="jav-us__list">
      <div v-if="loading" class="jav-empty" style="padding: 28px 0">加载中…</div>
      <div v-else-if="error" class="jav-empty" style="padding: 28px 0">
        <div>{{ error }}</div>
        <AppButton variant="ghost" size="sm" style="margin-top: 12px" @click="load(false)">
          重试
        </AppButton>
      </div>
      <div v-else-if="items.length === 0" class="jav-empty" style="padding: 28px 0">
        在他评论过的影片里，还没有发现贴出来的链接。
      </div>

      <template v-else>
        <div v-for="m in items" :key="m.id" class="jav-us__row">
          <!-- 影片头：封面 + 番号 + 标题 + 日期。点它开详情。 -->
          <div class="jav-us__head" @click="emit('openMovie', m.id)">
            <img v-if="m.cover" class="jav-us__thumb" :src="javImageURL(m.cover)" loading="lazy" :alt="m.number" />
            <div v-else class="jav-us__thumb jav-us__thumb--none">无封面</div>
            <div class="jav-us__meta">
              <div class="jav-us__num">
                {{ m.number || m.id }}
                <span v-if="m.in_library" class="jav-us__lib">已入库</span>
              </div>
              <div class="jav-us__title" :title="m.title">{{ m.title }}</div>
              <div class="jav-us__dates">
                <span v-if="m.release_date">{{ m.release_date }}</span>
                <span v-if="m.links[0]?.date">· {{ m.links[0].date }} 分享</span>
              </div>
            </div>
          </div>

          <!-- 他在这一部贴出来的每一条链接，各占一行，直接能复制/推送。 -->
          <div v-for="l in m.links" :key="l.uri" class="jav-us__link">
            <i class="fas fa-link jav-us__linkicon" />
            <span class="jav-us__name" :title="l.name">{{ l.name }}</span>
            <span class="jav-us__size">{{ l.size_text || "—" }}</span>
            <span class="jav-us__tags">
              <span v-if="l.resolution_badge" class="jav-badge jav-badge--res">{{ l.resolution_badge }}</span>
              <span v-if="l.uncensored" class="jav-badge jav-badge--break">破解</span>
              <span v-if="l.subtitle" class="jav-badge jav-badge--on">字幕</span>
            </span>
            <span class="jav-us__acts">
              <button class="jd-btn jd-btn--icon" title="复制链接" @click="copyLink(l.uri)">
                <i class="fas fa-copy" />
              </button>
              <button class="jd-btn jd-btn--icon" title="推送到网盘" :disabled="pushing" @click="pushLink(m, l)">
                <i class="fas fa-paper-plane" />
              </button>
            </span>
          </div>
        </div>

        <div v-if="hasMore" class="jav-us__more">
          <AppButton variant="ghost" size="sm" :disabled="loadingMore" @click="loadMore">
            {{ loadingMore ? "加载中…" : `加载更多（还有 ${total - items.length} 部）` }}
          </AppButton>
        </div>
      </template>
      </div>
    </div>
  </AppPlainModal>
</template>

<style scoped>
/* 面板是定高的（AppModal 的 steady），这里撑满它并把列表区做成滚动的那一块。 */
.jav-us {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.jav-us__bar {
  flex: none;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  padding: 0 18px 12px;
}

/* `min-height: 0` 不能省：flex 子项默认 min-height:auto，
   不写它内容再长也只会把面板撑破，而不是出现滚动条。 */
.jav-us__list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
}

.jav-us__count {
  font-size: 12px;
  color: var(--text-regular);
}

.jav-us__note {
  font-size: 11.5px;
  color: var(--text-muted);
}

/* 一部影片一块：头一行是片子，下面挂它分享出来的链接。 */
.jav-us__row {
  border-top: 1px solid var(--border);
  padding: 12px 18px;
}

.jav-us__head {
  display: flex;
  gap: 10px;
  cursor: pointer;
}

.jav-us__thumb {
  width: 76px;
  height: 52px;
  flex: none;
  border-radius: var(--radius-sm);
  object-fit: cover;
  background: var(--surface-sunken);
}

.jav-us__thumb--none {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 11px;
  color: var(--text-muted);
}

.jav-us__meta {
  min-width: 0;
}

.jav-us__num {
  font-size: 13px;
  font-weight: 700;
  color: var(--brand);
}

.jav-us__lib {
  margin-left: 6px;
  padding: 1px 6px;
  border-radius: 5px;
  background: rgba(16, 185, 129, 0.14);
  color: var(--success);
  font-size: 10.5px;
  font-weight: 600;
}

.jav-us__title {
  font-size: 12.5px;
  line-height: 1.4;
  color: var(--text);
  margin: 2px 0 3px;
  /* 标题可以很长：两行截断，免得一部片把整屏占掉。 */
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.jav-us__dates {
  font-size: 11.5px;
  color: var(--text-muted);
}

/* 一条链接。链接名可能很长（种子名），所以给它弹性、其余各项 flex:none。 */
.jav-us__link {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  padding: 6px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-sunken);
  font-size: 12px;
}

.jav-us__linkicon {
  flex: none;
  color: var(--text-muted);
  font-size: 11px;
}

.jav-us__name {
  min-width: 0;
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text);
}

.jav-us__size {
  flex: none;
  color: var(--text-regular);
  font-weight: 600;
}

.jav-us__tags {
  flex: none;
  display: inline-flex;
  gap: 4px;
}

.jav-us__acts {
  flex: none;
  display: inline-flex;
  gap: 4px;
}

.jav-us__more {
  display: flex;
  justify-content: center;
  padding: 14px 0 4px;
}
</style>
