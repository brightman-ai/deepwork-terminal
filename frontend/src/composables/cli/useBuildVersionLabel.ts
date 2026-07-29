/**
 * useBuildVersionLabel — 顶栏版本徽标的数据来源，两壳共用一份。
 *
 * 取值走 `cliApi('/version')`，所以 standalone（`/api/version`）和 pro（`/api/cli/version`，
 * 经 host StripPrefix 转发给嵌入的 terminal）**同一行代码两边都对** —— 这正是 cliApi 存在的理由，
 * 也是这个仓吃过亏才立下的规矩：前缀差异归一到一处，不要 per-shell 各写一份。
 *
 * 服务端返回的是完整身份（Go 侧 terminal.BuildVersion）；`short` 是压给徽标看的，`full` 留给
 * title —— 格式规则在 buildVersion.ts（纯函数，带测试）。
 *
 * 取不到就保持空串：调用方据此把徽标整个藏掉。**宁可没有，也不要显示一个骗人的版本号** —— 这枚
 * 徽标唯一的用处就是回答"我跑的是不是我刚构建的那份"，一个猜出来的值比没有更糟。
 */
import { ref, computed, onMounted, watch, type ComputedRef, type Ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { formatFullVersion, formatShortVersion } from '@terminal/composables/cli/buildVersion'
import type { ReleaseState } from '@terminal/composables/cli/versionPanel'

export function useBuildVersionLabel(): {
  short: ComputedRef<string>
  full: ComputedRef<string>
  releaseState: Ref<ReleaseState>
  latest: Ref<string>
  latestUrl: Ref<string>
} {
  const { cliFetch, authCode } = useCliAuth()
  const raw = ref('')
  // 服务端定的四态（release_check.go 的 ReleaseState）。前端**只渲染，不重新推导** —— 语义化
  // 版本的比较规则属于"发布"这个领域，只该有一份实现，而且它在服务端。
  const releaseState = ref<ReleaseState>('local')
  const latest = ref('')
  const latestUrl = ref('')

  async function fetchVersion(): Promise<void> {
    try {
      const r = await cliFetch(cliApi('/version'))
      if (!r.ok) return
      const d = (await r.json()) as {
        version?: string; releaseState?: ReleaseState; latest?: string; latestUrl?: string
      }
      raw.value = d.version ?? ''
      releaseState.value = d.releaseState ?? 'local'
      latest.value = d.latest ?? ''
      latestUrl.value = d.latestUrl ?? ''
    } catch { /* 徽标保持隐藏 */ }
  }

  onMounted(fetchVersion)

  // 登录成功后补一次。挂载时这一枚徽标常常还没有资格拿到版本号：顶栏在**登录框之前**就渲染了，
  // 那次 `/version` 吃 401，而"只在 onMounted 取一次"意味着它**再也不会**去取 —— 于是首次进站
  // 的用户永远看不到版本号（夹具实测：先登录的会话有、后登录的没有，纯看时序，是个 flaky 缺陷）。
  // 挂在 authCode 上而不是盲目重试：**它才是原因**，拿到码的那一刻正是该再问一次的那一刻。
  watch(authCode, (code) => { if (code && !raw.value) void fetchVersion() })

  return {
    short: computed(() => formatShortVersion(raw.value)),
    full: computed(() => formatFullVersion(raw.value)),
    releaseState,
    latest,
    latestUrl,
  }
}
