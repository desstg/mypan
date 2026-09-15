<template>
  <div class="notice-body">
    <div class="notice-card">
      <div class="notice-flow">
        <div class="notice-node">
          <div class="notice-icon"><SvgIcon name="folder" :size="26" /></div>
          <span>源文件指纹</span>
        </div>
        <div class="notice-arrow">→</div>
        <div class="notice-node wide">
          <div class="notice-icon"><SvgIcon name="monitor" :size="26" /></div>
          <span>MyPan 试探</span>
        </div>
        <div class="notice-arrow">→</div>
        <div class="notice-node">
          <div class="notice-icon"><SvgIcon name="cloud" :size="26" /></div>
          <span>目标网盘检测</span>
        </div>
      </div>
    </div>

    <div class="notice-tips">
      <div class="notice-tip">
        <span class="notice-tip-bar" aria-hidden="true"></span>
        <span>先试探哪些文件能秒传；若直接传输且选了「覆盖」，误覆盖后无法撤销。</span>
      </div>
      <div class="notice-tip">
        <span class="notice-tip-bar" aria-hidden="true"></span>
        <span>支持预判的网盘只查询文件是否命中，不会真的转存一遍。</span>
      </div>
      <div class="notice-tip">
        <span class="notice-tip-bar" aria-hidden="true"></span>
        <span>无法预判的网盘（如 123）才会在临时目录真实试传；结束后自动删除，文件可能留在回收站。</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import SvgIcon from "@/components/icons/SvgIcon.vue";
</script>

<style scoped>
.notice-body {
  width: 100%;
}

.notice-card {
  padding: 14px 16px;
  border-radius: 12px;
  background: var(--surface-sunken);
  border: 1px solid var(--border);
  overflow: hidden;
}

.notice-flow {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto minmax(0, 1.2fr) auto minmax(0, 1fr);
  align-items: center;
  gap: 10px;
}

.notice-node {
  min-width: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 10px 8px;
  border-radius: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  color: var(--text-regular);
  font-size: 13px;
  text-align: center;
}

.notice-node.wide {
  min-width: 0;
}

.notice-icon {
  line-height: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.notice-arrow {
  color: var(--text-muted);
  font-size: 20px;
}

.notice-tips {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  color: var(--text-regular);
  font-size: 14px;
  line-height: 1.8;
}

.notice-tip {
  display: flex;
  align-items: stretch;
  gap: 10px;
}

.notice-tip-bar {
  flex-shrink: 0;
  width: 4px;
  border-radius: 999px;
  background: var(--brand-gradient);
}

@media (max-width: 640px) {
  .notice-flow {
    grid-template-columns: 1fr;
  }

  .notice-arrow {
    justify-self: center;
    transform: rotate(90deg);
  }
}

:global(:root[data-skin="brutal"]) .notice-card,
:global(:root[data-skin="brutal"]) .notice-node,
:global(:root[data-skin="brutal"]) .notice-tip-bar {
  border-radius: 0 !important;
}

:global(:root[data-skin="brutal"]) .notice-card,
:global(:root[data-skin="brutal"]) .notice-node {
  border: var(--brutal-bw) solid var(--brutal-ink);
}
</style>
