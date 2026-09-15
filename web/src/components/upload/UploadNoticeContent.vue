<script setup lang="ts">
import SvgIcon from "@/components/icons/SvgIcon.vue";
</script>

<template>
  <div class="upload-notice">
    <p class="upload-notice__intro">上传功能受浏览器影响，流程为：</p>
    <div class="upload-notice__card">
      <div class="upload-notice__flow">
        <div class="upload-notice__node">
          <div class="upload-notice__icon"><SvgIcon name="user" :size="26" /></div>
          <span>用户</span>
        </div>
        <div class="upload-notice__arrow">→</div>
        <div class="upload-notice__node upload-notice__node--wide">
          <div class="upload-notice__icon"><SvgIcon name="monitor" :size="26" /></div>
          <span>MyPan服务器</span>
        </div>
        <div class="upload-notice__arrow">→</div>
        <div class="upload-notice__node">
          <div class="upload-notice__icon"><SvgIcon name="cloud" :size="26" /></div>
          <span>网盘</span>
        </div>
      </div>
    </div>
    <div class="upload-notice__tips">
      <div class="upload-notice__tip">
        <span class="upload-notice__tip-bar" aria-hidden="true" />
        <span>建议电脑和服务器在同一内网，否则请使用网盘官方客户端上传。</span>
      </div>
      <div class="upload-notice__tip">
        <span class="upload-notice__tip-bar" aria-hidden="true" />
        <span>上传速度会受服务器网络带宽影响。</span>
      </div>
    </div>
  </div>
</template>
