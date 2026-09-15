<script setup lang="ts">
import { ref } from "vue";
import AppDropdown from "@/components/base/AppDropdown.vue";

const open = ref(false);
</script>

<template>
  <AppDropdown v-model:open="open" trigger="click" align="left" :min-width="360">
    <template #trigger="{ open: isOpen, toggle }">
      <button
        type="button"
        class="tmdb-hosts-tip"
        :class="{ 'tmdb-hosts-tip--open': isOpen }"
        :aria-expanded="isOpen"
        @click.stop="toggle"
      >
        没有代理怎么办？
      </button>
    </template>
    <template #panel>
      <div class="tmdb-hosts-panel">
        <div class="tmdb-hosts-panel__title">用 Docker hosts 访问 TMDB</div>
        <p class="tmdb-hosts-panel__lead">
          没有可用代理时，可以给容器加上域名 hosts 映射，让
          <code>api.themoviedb.org</code> /
          <code>image.tmdb.org</code>
          解析到你能访问的 IP（IP 请自行查找可用值，会随网络环境变化）。
        </p>
        <p class="tmdb-hosts-panel__foot">
          如果你在程序内「目录整理 → TMDB 设置」把 API 主域名改成了
          <code>https://api.tmdb.org</code>，那下面的 API hosts 也要对应改成
          <code>api.tmdb.org</code>，否则不会生效。
        </p>
        <div class="tmdb-hosts-panel__subtitle">docker run</div>
        <pre class="tmdb-hosts-panel__code">--add-host=api.themoviedb.org:可用IP
--add-host=image.tmdb.org:可用IP</pre>
        <div class="tmdb-hosts-panel__subtitle">docker-compose.yml</div>
        <pre class="tmdb-hosts-panel__code">extra_hosts:
  - "api.themoviedb.org:可用IP"
  - "image.tmdb.org:可用IP"</pre>
        <p class="tmdb-hosts-panel__foot">改完后重建/重启容器即可；这与程序内的「启用代理」是两条路，一般二选一。</p>
      </div>
    </template>
  </AppDropdown>
</template>

<style scoped>
.tmdb-hosts-tip {
  border: none;
  padding: 0;
  background: transparent;
  color: var(--brand);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  white-space: nowrap;
}
.tmdb-hosts-tip:hover,
.tmdb-hosts-tip--open {
  color: var(--brand-strong);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.tmdb-hosts-panel {
  padding: 12px 14px;
  max-width: 380px;
}
.tmdb-hosts-panel__title {
  font-size: 13px;
  font-weight: 700;
  color: var(--text);
  margin-bottom: 6px;
}
.tmdb-hosts-panel__lead,
.tmdb-hosts-panel__foot {
  margin: 0;
  font-size: 12px;
  line-height: 1.55;
  color: var(--text-muted);
}
.tmdb-hosts-panel__foot {
  margin-top: 10px;
}
.tmdb-hosts-panel__subtitle {
  margin: 10px 0 4px;
  font-size: 12px;
  font-weight: 700;
  color: var(--text);
}
.tmdb-hosts-panel__code {
  margin: 0;
  padding: 8px 10px;
  border-radius: 8px;
  background: var(--surface-sunken);
  border: 1px solid var(--border-soft);
  font-size: 11px;
  line-height: 1.5;
  color: var(--text-regular);
  white-space: pre-wrap;
  word-break: break-all;
}
.tmdb-hosts-panel code {
  padding: 1px 4px;
  border-radius: 4px;
  background: var(--border-soft);
  font-size: 11px;
}
</style>
