<script setup lang="ts">
import { computed } from "vue";
import { javImageURL } from "@/api/jav";
import type { JavMovieCard } from "@/types/jav";

/**
 * 番号影片卡片。
 *
 * 布局照源码 web/templates/_macros.html 的 videocard / top250.html 的 rank-card：
 * 3:2 封面 + 左上订阅按钮 + 左下角标（磁链数 / 时长）+ 右下入库标记 +
 * 右上排名角标 + 底部番号与片名。
 *
 * 排名角标是可选的 —— 只有榜单页才传 rank。
 */
const props = withDefaults(
  defineProps<{
    movie: JavMovieCard;
    /** 排名角标。传 0 或不传则不显示。 */
    rank?: number;
    /** 已订阅状态。由页面批量查出后按 id 传进来。 */
    subscribed?: boolean;
    /** 是否显示订阅按钮。搜索/榜单/影库都显示。 */
    showSubscribe?: boolean;
  }>(),
  { rank: 0, subscribed: false, showSubscribe: true },
);

const emit = defineEmits<{ open: []; subscribe: [] }>();

/** 排名角标的配色：1 金 2 银 3 铜，其余半透明黑。 */
const rankClass = computed(() => {
  switch (props.rank) {
    case 1:
      return "jav-rank-num jav-rank-num--r1";
    case 2:
      return "jav-rank-num jav-rank-num--r2";
    case 3:
      return "jav-rank-num jav-rank-num--r3";
    default:
      return "jav-rank-num jav-rank-num--rn";
  }
});

const cover = computed(
  () => props.movie.cover || props.movie.cover_url || props.movie.javbus_cover || props.movie.thumb_url,
);

const title = computed(
  () => props.movie.title || props.movie.origin_title || "（无标题）",
);

/** 番号缺失时退回 id —— 卡片上总得有个能认的东西。 */
const label = computed(() => props.movie.number || props.movie.id);
</script>

<template>
  <div class="jav-card" role="button" tabindex="0" @click="emit('open')" @keyup.enter="emit('open')">
    <div class="jav-card__cover">
      <img v-if="cover" :src="javImageURL(cover)" :alt="label" loading="lazy" />
      <div v-else class="jav-card__placeholder">无封面</div>

      <button
        v-if="showSubscribe"
        type="button"
        class="jav-card__sub"
        :class="{ 'jav-card__sub--on': subscribed }"
        :title="subscribed ? '取消订阅' : '订阅'"
        @click.stop="emit('subscribe')"
      >
        {{ subscribed ? "已订阅" : "订阅" }}
      </button>

      <span v-if="rank > 0" :class="rankClass">{{ rank }}</span>

      <div class="jav-card__overlays">
        <span v-if="movie.magnets_count" class="jav-chip jav-chip--brand">
          🔗 {{ movie.magnets_count }}
        </span>
        <span v-if="movie.duration" class="jav-chip jav-chip--dark">
          {{ movie.duration }}分钟
        </span>
      </div>

      <span v-if="movie.number" class="jav-card__flag" :class="{ 'jav-card__flag--missing': !movie.in_library }">
        {{ movie.in_library ? "已入库" : "未入库" }}
      </span>
    </div>

    <div class="jav-card__body">
      <div class="jav-card__meta">
        <span class="jav-card__num">{{ label }}</span>
        <span v-if="movie.release_date" class="jav-card__date">{{ movie.release_date }}</span>
      </div>
      <div class="jav-card__title" :title="title">{{ title }}</div>
    </div>
  </div>
</template>
