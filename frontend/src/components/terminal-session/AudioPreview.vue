<script setup lang="ts">
/**
 * AudioPreview — 音频文件的预览就是"能放出来"。
 *
 * 工作仓里的 m4a 是会议录音（`9月8日 状态讨论.m4a` 这种），是产出物不是附件——之前它们落进
 * `{binary:true}` 分支，只能下载。用**原生 `<audio controls>`** 而不是自绘播放器：系统控件在
 * 手机上天然合规（触控目标、后台播放、锁屏控制），自绘一律不如它。
 *
 * 后端配套（archive_preview.go）：按 audio/* 的 Content-Type inline 服务，且走 ServeContent ——
 * **Range 请求是刚需**，没有它进度条拖不动，71MB 的录音只能从头听。
 */
const props = defineProps<{ name: string; src: string; size?: number }>()
const sizeText = props.size ? `${(props.size / (1 << 20)).toFixed(1)} MB` : ''
</script>

<template>
  <div class="flex h-full flex-col items-center justify-center gap-4 px-6" data-testid="fp-preview-audio">
    <div class="text-center">
      <p class="text-xs font-medium text-foreground break-all select-text">{{ name }}</p>
      <p v-if="sizeText" class="mt-1 text-[0.62rem] text-muted-foreground">{{ sizeText }}</p>
    </div>
    <audio :src="src" controls preload="metadata" class="w-full max-w-md" data-testid="fp-audio-el"></audio>
    <p class="text-[0.62rem] text-muted-foreground/70">拖动进度条即可跳转（服务端支持分段请求）</p>
  </div>
</template>
