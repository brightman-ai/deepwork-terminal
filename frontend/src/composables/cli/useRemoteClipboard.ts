import { computed, onScopeDispose, reactive, ref } from 'vue'
import { useCliAuth } from './useCliAuth'
import { cliApi, peerApi } from './useCliApiPrefix'
import { copyTerminalTextOnClick } from './terminalClipboard'
import {
  readClipboardHistory,
  saveClipboardHistory,
} from './clipboardPersistence'
import {
  boundClipboardHistory,
  sameClipboardTarget,
  validClipboardEntry,
  type ClipboardEntry,
  type ClipboardTarget,
} from './clipboardHistory'

interface Endpoint {
  isRemote?: () => boolean
  httpBase?: () => string | undefined
  authToken?: () => string | undefined
  sessionId: () => string
  active: () => boolean
}
interface HistoryResponse {
  epoch: string
  cursor: number
  entries: ClipboardEntry[]
}
const clients = new Map<string, ReturnType<typeof createClient>>()
// Separate browser histories on auth rotation without using credentials as storage keys.
function fingerprint(s: string): string {
  let h = 2166136261
  for (let i = 0; i < s.length; i++)
    h = Math.imul(h ^ s.charCodeAt(i), 16777619)
  return (h >>> 0).toString(16)
}
function createClient(endpoint: Endpoint, key: string) {
  const auth = useCliAuth()
  // Small ID-only tombstones make delete/clear survive an immediate reload,
  // even before an asynchronous IndexedDB transaction has committed. No text.
  const removedKey = 'dw.clipboard.removed:' + key
  let removed = new Set<string>()
  try {
    removed = new Set(JSON.parse(localStorage.getItem(removedKey) || '[]'))
  } catch {
    /* IndexedDB remains available independently. */
  }
  const entries = ref<ClipboardEntry[]>([]),
    targets = ref<ClipboardTarget[]>([]),
    host = ref('远端')
  const targetsValid = ref(false)
  const connected = ref(false),
    error = ref(''),
    persistenceError = ref(''),
    loadingTargets = ref(false),
    busy = ref(false)
  const settings = reactive({
    autoCopy: true,
    paused: false,
    retentionDays: 7,
  })
  const view = reactive({
    tab: 'history' as 'history' | 'send',
    query: '',
    filter: 'all',
    selected: '',
    draft: '',
    target: null as ClipboardTarget | null,
    stage: 'edit' as 'edit' | 'draft',
    status: '',
    scroll: 0,
  })
  const owners = new Set<Endpoint>()
  let cursor = 0,
    epoch = '',
    seenAt = ref(0),
    known = new Set<string>(),
    timer: ReturnType<typeof setTimeout> | undefined
  let stopped = true,
    polling = false,
    initialized = false,
    writing = Promise.resolve(),
    saving = Promise.resolve()
  let localCopyRevision = 0
  async function request(path: string, init?: RequestInit, timeoutMs = 10000) {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), timeoutMs)
    try {
      let response: Response
      if (endpoint.isRemote?.()) {
        const base = endpoint.httpBase?.(),
          token = endpoint.authToken?.()
        if (!base || !token) throw new Error('此远端暂不可达')
        response = await fetch(base.replace(/\/$/, '') + peerApi(path), {
          ...init,
          signal: controller.signal,
          headers: { 'Content-Type': 'application/json', 'X-CLI-Auth': token },
        })
      } else
        response = await auth.cliFetch(cliApi(path), {
          ...init,
          signal: controller.signal,
          headers: { 'Content-Type': 'application/json' },
        })
      const data = await response.json()
      if (!response.ok)
        throw new Error(data.error || `请求失败 (${response.status})`)
      return data
    } finally {
      clearTimeout(timeout)
    }
  }
  const ready = (async () => {
    try {
      const data = (await readClipboardHistory(key)) as
        | {
            entries?: ClipboardEntry[]
            known?: string[]
            cursor?: number
            epoch?: string
            seenAt?: number
            settings?: Partial<typeof settings>
          }
        | undefined
      if (data) {
        entries.value = boundClipboardHistory(
          (data.entries || []).filter((entry) => !removed.has(entry.id)),
          data.settings?.retentionDays || 7,
        )
        known = new Set([...(data.known || []), ...removed])
        cursor = data.cursor || 0
        epoch = data.epoch || ''
        seenAt.value = data.seenAt || 0
        Object.assign(settings, data.settings)
      }
    } catch {
      persistenceError.value = '此浏览器暂不能保存历史；当前页面仍可使用'
    }
  })()
  function persist() {
    saving = saving
      .then(async () => {
        // Opening a panel can markSeen before hydration. Never let that empty
        // startup snapshot overwrite the stored history/cursor we are loading.
        await ready
        await saveClipboardHistory(key, {
          entries: entries.value.map((entry) => ({ ...entry })),
          known: [...known].slice(-2000),
          cursor,
          epoch,
          seenAt: seenAt.value,
          settings: { ...settings },
        })
      })
      .catch(() => {
        persistenceError.value = '历史保存失败；当前页面仍可使用'
      })
    return saving
  }
  function owned(e: ClipboardEntry) {
    return (
      document.visibilityState === 'visible' &&
      document.hasFocus() &&
      [...owners].some((o) => o.active() && o.sessionId() === e.sessionId)
    )
  }
  async function writeLocal(
    e: ClipboardEntry,
    automatic = false,
    revision = localCopyRevision,
  ) {
    if (!automatic) localCopyRevision++
    const allowed = () =>
      !automatic ||
      (revision === localCopyRevision &&
        settings.autoCopy &&
        Date.now() - Date.parse(e.at) < 5000 &&
        owned(e))
    if (!allowed()) return
    let copied = false
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(e.text)
        copied = true
      }
    } catch {
      /* Native copy fallback also works during a tmux mouse selection's activation. */
    }
    if (!copied && allowed()) copied = copyTerminalTextOnClick(e.text)
    const current = entries.value.find((item) => item.id === e.id)
    if (current && copied) {
      current.copiedAt = new Date().toISOString()
      current.action = '已复制到本机'
    } else if (current && !automatic)
      current.action = '浏览器未允许复制，可展开正文后手动选中复制'
    persist()
  }
  function accept(data: HistoryResponse) {
    const mayAuto = initialized && epoch === data.epoch
    const fresh: ClipboardEntry[] = []
    for (const e of data.entries) {
      if (!validClipboardEntry(e) || known.has(e.id) || removed.has(e.id))
        continue
      known.add(e.id)
      if (!settings.paused) fresh.unshift(e)
      if (
        mayAuto &&
        settings.autoCopy &&
        !e.replayed &&
        e.direction === 'remote' &&
        Date.now() - Date.parse(e.at) < 5000 &&
        owned(e)
      ) {
        const revision = localCopyRevision
        writing = writing
          .then(() => writeLocal(e, true, revision))
          .catch(() => {})
      }
    }
    entries.value = boundClipboardHistory(
      [...fresh, ...entries.value],
      settings.retentionDays,
    )
    known = new Set([...known].slice(-2000))
    cursor = data.cursor
    epoch = data.epoch
    initialized = true
    persist()
  }
  async function poll() {
    if (stopped || polling) return
    polling = true
    try {
      await ready
      const data = (await request(
        '/clipboard?' +
          new URLSearchParams({
            epoch,
            since: String(cursor),
            wait: initialized ? '1' : '0',
          }),
        undefined,
        25000,
      )) as HistoryResponse
      connected.value = true
      error.value = ''
      if (data.entries.length || epoch !== data.epoch) accept(data)
      else {
        cursor = data.cursor
        initialized = true
        const bounded = boundClipboardHistory(
          entries.value,
          settings.retentionDays,
        )
        if (bounded.length !== entries.value.length) {
          entries.value = bounded
          persist()
        }
      }
    } catch {
      connected.value = false
      error.value = '剪贴板连接暂不可用；历史和草稿已保留'
    } finally {
      polling = false
      if (!stopped)
        timer = setTimeout(
          () => void poll(),
          !connected.value
            ? 2000
            : document.visibilityState === 'hidden'
              ? 5000
              : 100,
        )
    }
  }
  async function refreshTargets() {
    if (loadingTargets.value) return
    loadingTargets.value = true
    try {
      const data = await request('/clipboard/targets')
      targets.value = data.targets
      targetsValid.value = true
      host.value = data.host || '远端'
      connected.value = true
      error.value = ''
    } catch (e) {
      targetsValid.value = false
      error.value = e instanceof Error ? e.message : '目标列表不可用'
    } finally {
      loadingTargets.value = false
    }
  }
  function selectDefault(sessionId: string, paneId?: string) {
    if (view.target) return
    view.target =
      targets.value.find(
        (t) =>
          t.sessionId === sessionId &&
          (paneId ? t.paneId === paneId : t.active),
      ) || null
  }
  const targetAvailable = computed(
    () =>
      connected.value &&
      targetsValid.value &&
      !!view.target &&
      targets.value.some((t) => sameClipboardTarget(t, view.target!)),
  )
  async function saveRemote() {
    if (!view.target || busy.value) return
    const target = { ...view.target },
      text = view.draft
    busy.value = true
    view.status = ''
    try {
      const result = await request('/clipboard', {
        method: 'POST',
        body: JSON.stringify({ target, text }),
      })
      const e = result.entry as ClipboardEntry
      known.add(e.id)
      if (!settings.paused)
        entries.value = boundClipboardHistory(
          [e, ...entries.value],
          settings.retentionDays,
        )
      view.status = '已存到 ' + target.name + ' 的远端剪贴板'
      persist()
    } catch (e) {
      view.status = e instanceof Error ? e.message : '保存失败，草稿已保留'
    } finally {
      busy.value = false
    }
  }
  async function sendDraft() {
    if (!view.target || busy.value || view.stage !== 'draft') return
    const target = { ...view.target },
      text = view.draft
    busy.value = true
    view.status = ''
    try {
      const result = await request('/clipboard/send', {
        method: 'POST',
        body: JSON.stringify({ target, text }),
      })
      const e = result.entry as ClipboardEntry
      known.add(e.id)
      if (!settings.paused)
        entries.value = boundClipboardHistory(
          [e, ...entries.value.filter((item) => item.id !== e.id)],
          settings.retentionDays,
        )
      persist()
      view.status = '已发送到 ' + target.name
      view.stage = 'edit'
      view.draft = ''
    } catch (e) {
      view.status =
        e instanceof Error
          ? e.message
          : '发送未确认，请检查目标后再决定是否重试'
    } finally {
      busy.value = false
    }
  }
  const unread = computed(
    () =>
      entries.value.filter(
        (e) =>
          e.direction === 'remote' &&
          !e.replayed &&
          !e.copiedAt &&
          Date.parse(e.at) > seenAt.value,
      ).length,
  )
  function markSeen() {
    seenAt.value = Date.now()
    persist()
  }
  function remove(id: string) {
    rememberRemoved([id])
    entries.value = entries.value.filter((e) => e.id !== id)
    persist()
  }
  function clear() {
    rememberRemoved([...known, ...entries.value.map((e) => e.id)])
    entries.value = []
    markSeen()
  }
  function rememberRemoved(ids: string[]) {
    removed = new Set([...removed, ...ids].slice(-2000))
    try {
      localStorage.setItem(removedKey, JSON.stringify([...removed]))
    } catch {
      /* The queued IndexedDB write still persists the change. */
    }
  }
  function pin(e: ClipboardEntry) {
    e.pinned = !e.pinned
    persist()
  }
  function updateSettings() {
    entries.value = boundClipboardHistory(entries.value, settings.retentionDays)
    persist()
  }
  function attach(owner: Endpoint) {
    owners.add(owner)
    if (stopped) {
      stopped = false
      void poll()
    }
    return () => {
      owners.delete(owner)
      if (!owners.size) {
        stopped = true
        clearTimeout(timer)
      }
    }
  }
  return {
    entries,
    targets,
    host,
    connected,
    error,
    persistenceError,
    settings,
    view,
    unread,
    busy,
    loadingTargets,
    targetAvailable,
    refreshTargets,
    selectDefault,
    markSeen,
    remove,
    clear,
    pin,
    updateSettings,
    saveRemote,
    sendDraft,
    writeLocal,
    attach,
    poll,
  }
}
export type RemoteClipboardClient = ReturnType<typeof createClient>
export function useRemoteClipboard(endpoint: Endpoint): RemoteClipboardClient {
  const auth = useCliAuth()
  const key =
    (endpoint.isRemote?.()
      ? endpoint.httpBase?.() || 'unreachable:' + endpoint.sessionId()
      : cliApi('')) +
    '#' +
    fingerprint(
      endpoint.isRemote?.() ? endpoint.authToken?.() || '' : auth.getAuthCode(),
    )
  let client = clients.get(key)
  if (!client) {
    client = createClient(endpoint, key)
    clients.set(key, client)
  }
  const detach = client.attach(endpoint)
  onScopeDispose(detach)
  return client
}
