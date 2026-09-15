<script setup lang="ts">
import { onMounted, onUnmounted, ref } from "vue";
import SvgIcon from "@/components/icons/SvgIcon.vue";
import { useHomeFooterStatus } from "@/composables/useHomeFooterStatus";
import { usePerformancePanel } from "@/composables/usePerformancePanel";
import { APP_URL, APP_VERSION_BADGE } from "@/version";

// 简约首页：footer 只留版本号 + 性能 + 传输任务（状态由 FileBrowser 经 useHomeFooterStatus 推送）。
const COMPACT_HOME_KEY = "litepan:index:compact-home-enabled";
const compactHome = ref(false);

function syncCompactHome() {
  compactHome.value = localStorage.getItem(COMPACT_HOME_KEY) === "1";
}

onMounted(() => {
  syncCompactHome();
  window.addEventListener("storage", syncCompactHome);
});
onUnmounted(() => {
  window.removeEventListener("storage", syncCompactHome);
});

const { status, openTaskPanel } = useHomeFooterStatus();
// 性能面板展开状态持久化（与工具栏性能面板共享同一状态/存储），刷新后保持
const { expanded: perfOpen, toggle: togglePerf } = usePerformancePanel();

// footer 只保留版本号；项目地址与联合测评入口已移除。
const badges = [
  {
    key: "docs",
    href: APP_URL,
    icon: "github",
    label: "当前版本",
    value: APP_VERSION_BADGE,
  },
] as const;
</script>

<template>
  <footer class="footer">
    <div class="container footer__inner">
      <template v-if="!compactHome">
        <template v-for="(item, index) in badges" :key="item.key">
          <span v-if="index > 0" class="footer__sep" aria-hidden="true">|</span>
          <a
            class="footer-item"
            :href="item.href"
            target="_blank"
            rel="noopener noreferrer"
          >
            <SvgIcon :name="item.icon" :size="14" class-name="footer-item__icon" />
            <span class="footer-item__label">{{ item.label }}</span>
            <span class="footer-item__value">{{ item.value }}</span>
          </a>
        </template>
      </template>

      <template v-else>
        <a
          class="footer-item"
          :href="badges[0].href"
          target="_blank"
          rel="noopener noreferrer"
        >
          <SvgIcon :name="badges[0].icon" :size="14" class-name="footer-item__icon" />
          <span class="footer-item__label">{{ badges[0].label }}</span>
          <span class="footer-item__value">{{ badges[0].value }}</span>
        </a>
        <span class="footer__sep" aria-hidden="true">|</span>

        <button
          type="button"
          class="footer-item footer-status-btn"
          :class="{ open: perfOpen }"
          :aria-expanded="perfOpen"
          :title="perfOpen ? '收起性能信息' : '展开性能信息'"
          @click="togglePerf"
        >
          <SvgIcon name="lightning" :size="14" class-name="footer-item__icon" />
          <template v-if="perfOpen">
            <span class="footer-item__label">响应</span>
            <span class="footer-item__value">{{ status.responseTime }}</span>
            <span class="footer-status-btn__sep" aria-hidden="true">·</span>
            <span class="footer-item__label">缓存</span>
            <span class="footer-item__value">{{ status.cacheRate }}</span>
          </template>
        </button>
        <span class="footer__sep" aria-hidden="true">|</span>

        <button
          type="button"
          class="footer-item footer-status-btn footer-status-btn--tasks"
          :class="{
            active: status.uploadTaskActive,
            failed: status.uploadTaskFailed && !status.uploadTaskActive,
            success: status.uploadTaskSuccess && !status.uploadTaskActive && !status.uploadTaskFailed,
          }"
          :title="status.uploadTaskLabel || '传输列表'"
          @click="openTaskPanel"
        >
          <span class="footer-status-btn__icon-wrap">
            <svg
              v-if="status.uploadTaskSuccess && !status.uploadTaskActive && !status.uploadTaskFailed"
              class="footer-status-btn__ok"
              viewBox="0 0 16 16"
              fill="none"
              stroke="currentColor"
              stroke-width="1.75"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"
            >
              <path d="m3.5 8.5 3 3 6-7" />
            </svg>
            <SvgIcon v-else name="upload" :size="14" />
          </span>
          <span v-if="status.uploadTaskCount > 0" class="footer-status-btn__badge">
            {{ Math.min(status.uploadTaskCount, 99) }}
          </span>
        </button>
      </template>
    </div>
  </footer>
</template>

<style scoped>
.footer {
  position: fixed;
  bottom: 0;
  left: 0;
  right: 0;
  z-index: 50;
  background: var(--surface-sunken);
  border-top: 1px solid var(--border);
  width: 100%;
}
.footer__inner {
  min-height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: 14px;
  padding: 12px 0;
}
.footer__sep {
  color: var(--border);
  font-size: 13px;
  line-height: 1;
  user-select: none;
}

.footer-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  text-decoration: none;
  color: inherit;
  font-size: 13px;
  line-height: 1.2;
  white-space: nowrap;
}
.footer-item__icon {
  color: var(--text-muted);
}
.footer-item__label {
  color: var(--text-muted);
}
.footer-item__label::after {
  content: ":";
}
.footer-item__value {
  color: var(--text-regular);
  font-weight: 500;
}

/* 简约首页 footer：性能/传输任务按钮（与链接同视觉，可点击） */
.footer-status-btn {
  border: 0;
  background: transparent;
  padding: 0;
  margin: 0;
  font-family: inherit;
  cursor: pointer;
  color: inherit;
  position: relative;
}
.footer-status-btn:hover .footer-item__icon {
  color: var(--brand-strong);
}
.footer-status-btn__sep {
  color: var(--text-muted);
  margin: 0 2px;
}
.footer-status-btn__ok {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  display: block;
}
.footer-status-btn--tasks.active {
  color: var(--brand);
}
.footer-status-btn--tasks.failed {
  color: var(--danger);
}
.footer-status-btn--tasks.success {
  color: var(--success);
}
.footer-status-btn__icon-wrap {
  display: inline-flex;
  align-items: center;
}
.footer-status-btn__badge {
  position: absolute;
  top: -7px;
  right: -9px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border: 2px solid var(--surface-sunken);
  border-radius: 999px;
  background: var(--danger);
  color: #fff;
  font-size: 10px;
  font-weight: 700;
  line-height: 12px;
  text-align: center;
  box-sizing: border-box;
}

@media (max-width: 640px) {
  .footer__inner {
    gap: 10px;
  }
  .footer-item {
    font-size: 12px;
  }
}
</style>
