<script setup lang="ts">
import AppHeader from "@/components/layout/AppHeader.vue";
import AppFooter from "@/components/layout/AppFooter.vue";
import FileBrowser from "@/components/file/FileBrowser.vue";
</script>

<template>
  <div class="page">
    <AppHeader />
    <main class="page__main">
      <div class="container">
        <FileBrowser />
      </div>
    </main>
    <AppFooter />
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
}
.page__main {
  flex: 1;
  background: var(--bg);
  /* footer 固定于视口底部，内容区预留其高度避免被遮挡 */
  padding-bottom: 88px;
}
</style>
