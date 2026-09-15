<script setup lang="ts">
import AppModal from "@/components/base/AppModal.vue";
import type { AnnouncementItem } from "@/api/announcement";

const props = defineProps<{
  open: boolean;
  item: AnnouncementItem | null;
}>();
const emit = defineEmits<{
  close: [];
}>();

// 底部三个操作入口（固定链接，新窗口打开）。
const GITHUB_URL = "https://github.com/Ponphil/LitePan";
const SPONSOR_URL = "https://www.litepan.top/sponsor.html";
const CHANGELOG_URL = "https://www.litepan.top/changelog.html";

function closeAll() {
  emit("close");
}
</script>

<template>
  <AppModal :open="open" size="lg" @close="closeAll">
    <template v-if="item" #header>
      <div class="announcement-modal__head">
        <span v-if="item.badge" class="announcement-modal__badge">{{ item.badge }}</span>
        <h3 class="announcement-modal__title">{{ item.dialog_title }}</h3>
      </div>
    </template>

    <div v-if="item" class="announcement-modal__body">
      <!-- 警示区：黄色警示框，纯文字保留换行 -->
      <div v-if="item.banner" class="announcement-modal__banner" role="note">
        {{ item.banner }}
      </div>

      <!-- 特别说明区只展示纯文本，避免公告触发第三方图片请求。 -->
      <div v-if="item.special" class="announcement-modal__special" role="note">
        {{ item.special }}
      </div>

      <p v-if="item.lead" class="announcement-modal__lead">{{ item.lead }}</p>

      <!-- 公告正文区：统一内容区内的简洁列表 -->
      <div v-if="item.issues.length" class="announcement-modal__list">
        <section
          v-for="(sec, i) in item.issues"
          :key="i"
          class="announcement-modal__section"
        >
          <h4 v-if="sec.title" class="announcement-modal__section-title">{{ sec.title }}</h4>
          <div v-if="sec.body" class="announcement-modal__section-body">{{ sec.body }}</div>
        </section>
      </div>

      <!-- 操作区：三个小卡片，新窗口打开 -->
      <div class="announcement-modal__links">
        <a
          class="announcement-modal__link"
          :href="GITHUB_URL"
          target="_blank"
          rel="noopener noreferrer"
        >
          <i class="fab fa-github announcement-modal__link-icon" aria-hidden="true" />
          <span class="announcement-modal__link-copy">
            <strong>GitHub 仓库</strong>
            <small>源码与问题反馈</small>
          </span>
        </a>
        <a
          class="announcement-modal__link"
          :href="SPONSOR_URL"
          target="_blank"
          rel="noopener noreferrer"
        >
          <i class="fas fa-heart announcement-modal__link-icon announcement-modal__link-icon--sponsor" aria-hidden="true" />
          <span class="announcement-modal__link-copy">
            <strong>打赏支持</strong>
            <small>赞助 MyPan 开发</small>
          </span>
        </a>
        <a
          class="announcement-modal__link"
          :href="CHANGELOG_URL"
          target="_blank"
          rel="noopener noreferrer"
        >
          <i class="fas fa-list-ul announcement-modal__link-icon announcement-modal__link-icon--log" aria-hidden="true" />
          <span class="announcement-modal__link-copy">
            <strong>更新日志</strong>
            <small>查看历史版本</small>
          </span>
        </a>
      </div>
    </div>

    <!-- 保留空 footer 结构（无分割线、无按钮），仅占底部留白 -->
    <template #footer><span class="announcement-modal__foot-spacer" /></template>
  </AppModal>
</template>

<style scoped>
.announcement-modal__head {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex-wrap: wrap;
}

.announcement-modal__badge {
  flex: none;
  padding: 2px 10px;
  border-radius: var(--radius-pill);
  background: color-mix(in srgb, var(--brand) 12%, var(--surface));
  color: var(--brand);
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.announcement-modal__title {
  margin: 0;
  font-size: 19px;
  font-weight: 800;
  color: var(--text);
  line-height: 1.35;
  min-width: 0;
  overflow-wrap: anywhere;
}

.announcement-modal__body {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 警示区：黄色警示框，纯文字 */
.announcement-modal__banner {
  padding: 10px 14px;
  border: 1px solid color-mix(in srgb, var(--warning) 32%, var(--border));
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--warning) 10%, var(--surface));
  color: var(--text);
  font-size: 13px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
}

/* 特别说明区：中性纯文本卡片 */
.announcement-modal__special {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
  color: var(--text);
  font-size: 13px;
  line-height: 1.6;
  word-break: break-word;
  white-space: pre-wrap;
}

.announcement-modal__lead {
  margin: 0;
  font-size: 14px;
  line-height: 1.7;
  color: var(--text);
  white-space: pre-wrap;
  word-break: break-word;
}

.announcement-modal__list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 28px;
  row-gap: 0;
  padding: 10px 16px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface-sunken);
  counter-reset: announcement-item;
}

.announcement-modal__section:only-child {
  grid-column: 1 / -1;
}

.announcement-modal__section {
  position: relative;
  min-width: 0;
  padding: 9px 0 9px 28px;
  counter-increment: announcement-item;
}

.announcement-modal__section::before {
  content: counter(announcement-item, decimal-leading-zero);
  position: absolute;
  top: 10px;
  left: 0;
  color: var(--brand);
  font-size: 11px;
  font-weight: 800;
  line-height: 1.4;
}

.announcement-modal__section-title {
  margin: 0 0 3px;
  font-size: 14px;
  font-weight: 700;
  color: var(--text);
}

.announcement-modal__section-body {
  font-size: 13px;
  line-height: 1.55;
  color: var(--text-muted);
  white-space: pre-wrap;
  word-break: break-word;
}

/* 操作区：三个小卡片 */
.announcement-modal__links {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 10px;
  margin-top: 2px;
}

.announcement-modal__link {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 14px;
  border: 1px solid var(--border-soft);
  border-radius: var(--radius-md);
  background: var(--surface);
  color: var(--text);
  text-decoration: none;
  transition:
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}

.announcement-modal__link:hover {
  border-color: color-mix(in srgb, var(--brand) 40%, var(--border));
  box-shadow: var(--shadow-card);
}

.announcement-modal__link-icon {
  flex: none;
  width: 34px;
  height: 34px;
  display: grid;
  place-items: center;
  border-radius: var(--radius-sm);
  background: color-mix(in srgb, var(--brand) 10%, var(--surface));
  color: var(--brand);
  font-size: 16px;
}

.announcement-modal__link-icon--sponsor {
  background: color-mix(in srgb, #ef4444 12%, var(--surface));
  color: #ef4444;
}

.announcement-modal__link-icon--log {
  background: color-mix(in srgb, #8b5cf6 12%, var(--surface));
  color: #8b5cf6;
}

.announcement-modal__link-copy {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.announcement-modal__link-copy strong {
  font-size: 13px;
  font-weight: 700;
}

.announcement-modal__link-copy small {
  font-size: 11px;
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

@media (max-width: 560px) {
  .announcement-modal__list,
  .announcement-modal__links {
    grid-template-columns: 1fr;
  }
}
</style>
