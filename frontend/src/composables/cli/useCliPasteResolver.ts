import { useCliAuth } from '@/composables/cli/useCliAuth'
import { useClipboardPaste } from '@/composables/cli/useClipboardPaste'
import { detectCliRuntimeProfile, type CliRuntimeMode } from '@/composables/cli/useCliRuntimeMode'
import { createLogger, createTrace, traceHeaders, type TraceContext } from '@/utils/obs'

type HudKind = 'state' | 'error'
type PasteSource =
  | 'paste-event-file'
  | 'paste-event-image'
  | 'async-clipboard-image'
  | 'native-clipboard-path'
  | 'manual-attach'

export interface CliPasteResolverOptions {
  sessionId: () => string
  surface: string
  isActive?: () => boolean
  sendBinary: (data: Uint8Array) => void
  openAttachmentPicker: () => void
  hudRecord?: (kind: HudKind, message: string) => void
}

export interface ClipboardDataItemSummary {
  kind: string
  type: string
  hasFile: boolean
}

export interface ClipboardSnapshot {
  types: string[]
  files: File[]
  items: ClipboardDataItemSummary[]
  stringReads: Promise<ClipboardStringItem>[]
  hasPlainText: boolean
  hasUriList: boolean
  hasFileHint: boolean
  hasImageHint: boolean
}

interface ClipboardStringItem {
  type: string
  value: string
}

export interface PasteEnvironment {
  runtimeMode: CliRuntimeMode
  isWailsDesktop: boolean
  canUseServerNativeClipboard: boolean
  isLoopbackHost: boolean
  hostname: string
  protocol: string
  isSecureContext: boolean
  supportsAsyncClipboardRead: boolean
}

interface ClipboardMetrics {
  pasteEvents: number
  interceptedEvents: number
  debouncedEvents: number
  defaultTextEvents: number
  uploadFiles: number
  uploadBytes: number
  uploadErrors: number
  dedupedFileCandidates: number
  dedupedInjectedPaths: number
  injectedPayloads: number
  nativeProbes: number
  nativeHits: number
  nativeRejected: number
  nativePathInjections: number
  nativeMissFallbacks: number
  asyncReadAttempts: number
  asyncReadHits: number
  attachmentFallbacks: number
  errors: number
}

const log = createLogger('cli-paste-resolver')
const encoder = new TextEncoder()
const metrics: ClipboardMetrics = {
  pasteEvents: 0,
  interceptedEvents: 0,
  debouncedEvents: 0,
  defaultTextEvents: 0,
  uploadFiles: 0,
  uploadBytes: 0,
  uploadErrors: 0,
  dedupedFileCandidates: 0,
  dedupedInjectedPaths: 0,
  injectedPayloads: 0,
  nativeProbes: 0,
  nativeHits: 0,
  nativeRejected: 0,
  nativePathInjections: 0,
  nativeMissFallbacks: 0,
  asyncReadAttempts: 0,
  asyncReadHits: 0,
  attachmentFallbacks: 0,
  errors: 0,
}

export function useCliPasteResolver(options: CliPasteResolverOptions) {
  const clipboardPaste = useClipboardPaste(options.sessionId)
  const { cliFetch } = useCliAuth()
  let lastPasteAt = 0
  let lastFingerprint = ''
  let lastWasIntercepted = false

  async function handlePasteEvent(e: ClipboardEvent): Promise<boolean> {
    if (options.isActive && !options.isActive()) return false

    const snapshot = snapshotClipboardEvent(e)
    const env = detectPasteEnvironment()
    const trace = createTrace('terminal/clipboard')
    const fingerprint = clipboardFingerprint(snapshot)
    const now = Date.now()
    metrics.pasteEvents++

    if (now - lastPasteAt < 500 && fingerprint === lastFingerprint) {
      metrics.debouncedEvents++
      log.info('cli.clipboard.paste_debounced', {
        surface: options.surface,
        runtime_mode: env.runtimeMode,
        elapsed_ms: now - lastPasteAt,
        last_intercepted: lastWasIntercepted,
        metrics: metricSnapshot(),
      }, trace)
      if (lastWasIntercepted) preventPaste(e)
      return lastWasIntercepted
    }

    lastPasteAt = now
    lastFingerprint = fingerprint
    lastWasIntercepted = false

    log.info('cli.clipboard.paste_event', {
      surface: options.surface,
      ...environmentLog(env),
      ...snapshotLog(snapshot),
      metrics: metricSnapshot(),
    }, trace)

    let nativeProbeAttempted = false
    if (shouldPreferNativeClipboardPaths(snapshot, env)) {
      preventPaste(e)
      lastWasIntercepted = true
      metrics.interceptedEvents++
      nativeProbeAttempted = true
      const nativePaths = await readNativeClipboardPaths(trace, env)
      if (nativePaths.length > 0) {
        metrics.nativePathInjections++
        injectPaths(nativePaths, 'native-clipboard-path', trace)
        return true
      }
      metrics.nativeMissFallbacks++
      log.info('cli.clipboard.native_probe_miss_fallback', {
        surface: options.surface,
        reason: snapshot.files.length > 0 ? 'file-object-without-native-path' : 'file-hint-without-native-path',
        fallback: snapshot.files.length > 0 ? 'upload' : 'resolver',
        ...environmentLog(env),
        ...snapshotLog(snapshot),
        metrics: metricSnapshot(),
      }, trace)
    }

    if (snapshot.files.length > 0) {
      preventPaste(e)
      lastWasIntercepted = true
      if (!nativeProbeAttempted) metrics.interceptedEvents++
      const source: PasteSource = snapshot.files.every(file => file.type.startsWith('image/'))
        ? 'paste-event-image'
        : 'paste-event-file'
      await uploadAndInjectFiles(snapshot.files, source, trace)
      return true
    }

    if (!nativeProbeAttempted && shouldProbeNativeClipboard(snapshot, env)) {
      preventPaste(e)
      lastWasIntercepted = true
      metrics.interceptedEvents++
      const nativePaths = await readNativeClipboardPaths(trace, env)
      if (nativePaths.length > 0) {
        metrics.nativePathInjections++
        injectPaths(nativePaths, 'native-clipboard-path', trace)
        return true
      }
    }

    if (shouldTryAsyncClipboardImage(snapshot, env)) {
      preventPaste(e)
      lastWasIntercepted = true
      metrics.interceptedEvents++
      const files = await readAsyncClipboardImages(trace)
      if (files.length > 0) {
        await uploadAndInjectFiles(files, 'async-clipboard-image', trace)
        return true
      }
      if (snapshot.hasFileHint) {
        openAttachmentFallback('async-image-read-empty-with-file-hint', trace, snapshot, env)
      } else {
        metrics.errors++
        options.hudRecord?.('error', 'clipboard image is not readable in this runtime')
        log.warn('cli.clipboard.image_unreadable', {
          surface: options.surface,
          ...environmentLog(env),
          ...snapshotLog(snapshot),
          metrics: metricSnapshot(),
        }, trace)
      }
      return true
    }

    const strings = await readClipboardStrings(snapshot)
    const uriPaths = extractFileUriPaths(strings)
    if (uriPaths.length > 0) {
      preventPaste(e)
      lastWasIntercepted = true
      metrics.interceptedEvents++
      if (env.canUseServerNativeClipboard) {
        injectPaths(uriPaths, 'native-clipboard-path', trace)
      } else {
        openAttachmentFallback('remote-file-uri', trace, snapshot, env)
      }
      return true
    }

    if (snapshot.hasFileHint) {
      preventPaste(e)
      lastWasIntercepted = true
      metrics.interceptedEvents++
      openAttachmentFallback('file-hint-without-file-object', trace, snapshot, env)
      return true
    }

    if (snapshot.hasPlainText) {
      metrics.defaultTextEvents++
      log.info('cli.clipboard.text_default', {
        surface: options.surface,
        runtime_mode: env.runtimeMode,
        types: snapshot.types,
        metrics: metricSnapshot(),
      }, trace)
    }
    return false
  }

  async function uploadFilesFromInput(files: File[], source: PasteSource = 'manual-attach'): Promise<void> {
    const trace = createTrace('terminal/clipboard')
    log.info('cli.clipboard.manual_attach_selected', {
      surface: options.surface,
      count: files.length,
      total_bytes: files.reduce((sum, file) => sum + file.size, 0),
      files: files.map(fileLog),
      metrics: metricSnapshot(),
    }, trace)
    await uploadAndInjectFiles(files, source, trace)
  }

  async function uploadAndInjectFiles(files: File[], source: PasteSource, trace: TraceContext): Promise<boolean> {
    if (files.length === 0) return false
    const uploadFiles = await dedupeFilesForUpload(files, source, trace)
    log.info('cli.clipboard.upload_batch_started', {
      surface: options.surface,
      source,
      count: uploadFiles.length,
      original_count: files.length,
      deduped_file_candidates: files.length - uploadFiles.length,
      total_bytes: uploadFiles.reduce((sum, file) => sum + file.size, 0),
      files: uploadFiles.map(fileLog),
    }, trace)

    const paths: string[] = []
    for (const file of uploadFiles) {
      const mime = file.type || 'application/octet-stream'
      const result = await clipboardPaste.uploadFile(file, mime, {
        filename: file.name || uploadFallbackName(mime),
        source,
        trace,
      })
      if (result.savedPath) {
        paths.push(result.textForPTY)
        metrics.uploadFiles++
        metrics.uploadBytes += result.size || file.size
      } else {
        metrics.uploadErrors++
      }
    }

    if (paths.length === 0) {
      metrics.errors++
      options.hudRecord?.('error', `clipboard upload failed: ${clipboardPaste.lastError.value || 'no saved path'}`)
      log.warn('cli.clipboard.upload_batch_empty', {
        surface: options.surface,
        source,
        error: clipboardPaste.lastError.value,
        metrics: metricSnapshot(),
      }, trace)
      return false
    }

    injectPaths(paths, source, trace)
    return true
  }

  async function dedupeFilesForUpload(files: File[], source: PasteSource, trace: TraceContext): Promise<File[]> {
    if (files.length <= 1) return files

    const groups = new Map<string, number[]>()
    files.forEach((file, index) => {
      const key = fileUploadCandidateKey(file)
      const group = groups.get(key)
      if (group) group.push(index)
      else groups.set(key, [index])
    })

    if (![...groups.values()].some(group => group.length > 1)) return files

    const skip = new Set<number>()
    let deduped = 0

    for (const [candidateKey, indices] of groups) {
      if (indices.length === 1) continue

      const seenHashes = new Set<string>()
      for (const index of indices) {
        const file = files[index]
        if (!file) continue
        const hash = await fileContentHash(file)
        if (!hash) continue
        const hashKey = `${candidateKey}|${hash}`
        if (seenHashes.has(hashKey)) {
          skip.add(index)
          deduped++
          continue
        }
        seenHashes.add(hashKey)
      }
    }

    const result = files.filter((_, index) => !skip.has(index))

    if (deduped > 0) {
      metrics.dedupedFileCandidates += deduped
      log.info('cli.clipboard.file_candidates_deduped', {
        surface: options.surface,
        source,
        original_count: files.length,
        upload_count: result.length,
        deduped_file_candidates: deduped,
        metrics: metricSnapshot(),
      }, trace)
    }

    return result
  }

  async function readNativeClipboardPaths(trace: TraceContext, env: PasteEnvironment): Promise<string[]> {
    metrics.nativeProbes++
    if (!env.canUseServerNativeClipboard) return []
    const startedAt = performance.now()
    try {
      const resp = await cliFetch('/api/browser/clipboard/files', {
        headers: traceHeaders(trace),
      })
      if (resp.status === 403) metrics.nativeRejected++
      if (!resp.ok) {
        log.info('cli.clipboard.native_probe_empty', {
          surface: options.surface,
          status: resp.status,
          elapsed_ms: Math.round(performance.now() - startedAt),
          metrics: metricSnapshot(),
        }, trace)
        return []
      }
      const data = await resp.json() as { paths?: string[] }
      const paths = Array.isArray(data.paths) ? data.paths.filter(Boolean) : []
      if (paths.length > 0) metrics.nativeHits++
      log.info('cli.clipboard.native_probe_completed', {
        surface: options.surface,
        path_count: paths.length,
        elapsed_ms: Math.round(performance.now() - startedAt),
        metrics: metricSnapshot(),
      }, trace)
      return paths
    } catch (err) {
      metrics.errors++
      log.warn('cli.clipboard.native_probe_failed', {
        surface: options.surface,
        error: err instanceof Error ? err.message : String(err),
        elapsed_ms: Math.round(performance.now() - startedAt),
        metrics: metricSnapshot(),
      }, trace)
      return []
    }
  }

  async function readAsyncClipboardImages(trace: TraceContext): Promise<File[]> {
    metrics.asyncReadAttempts++
    const read = navigator.clipboard?.read
    if (!read) return []
    const startedAt = performance.now()
    try {
      const items = await read.call(navigator.clipboard)
      const files: File[] = []
      for (const item of items) {
        for (const type of item.types) {
          if (!type.toLowerCase().startsWith('image/')) continue
          const blob = await item.getType(type)
          files.push(blobToFile(blob, `clipboard.${extFromMime(type)}`, type))
        }
      }
      if (files.length > 0) metrics.asyncReadHits++
      log.info('cli.clipboard.async_read_completed', {
        surface: options.surface,
        item_count: items.length,
        image_count: files.length,
        elapsed_ms: Math.round(performance.now() - startedAt),
        metrics: metricSnapshot(),
      }, trace)
      return files
    } catch (err) {
      log.info('cli.clipboard.async_read_unavailable', {
        surface: options.surface,
        error: err instanceof Error ? err.name || err.message : String(err),
        elapsed_ms: Math.round(performance.now() - startedAt),
        metrics: metricSnapshot(),
      }, trace)
      return []
    }
  }

  function injectPaths(paths: string[], source: PasteSource, trace: TraceContext): void {
    const uniquePaths = uniqueOrderedPaths(paths)
    const deduped = paths.length - uniquePaths.length
    if (deduped > 0) metrics.dedupedInjectedPaths += deduped
    const payload = formatPathsForPty(uniquePaths)
    options.sendBinary(encoder.encode(payload))
    metrics.injectedPayloads++
    options.hudRecord?.('state', `clipboard ${source}: ${uniquePaths.join(' ')}`)
    log.info('cli.clipboard.injected_paths', {
      surface: options.surface,
      source,
      path_count: uniquePaths.length,
      original_path_count: paths.length,
      deduped_paths: deduped,
      payload_chars: payload.length,
      paths: uniquePaths,
      metrics: metricSnapshot(),
    }, trace)
  }

  function openAttachmentFallback(
    reason: string,
    trace: TraceContext,
    snapshot: ClipboardSnapshot,
    env: PasteEnvironment,
  ): void {
    metrics.attachmentFallbacks++
    options.hudRecord?.('state', 'clipboard file requires upload picker')
    options.openAttachmentPicker()
    log.info('cli.clipboard.attachment_fallback', {
      surface: options.surface,
      reason,
      ...environmentLog(env),
      ...snapshotLog(snapshot),
      metrics: metricSnapshot(),
    }, trace)
  }

  return {
    handlePasteEvent,
    uploadFilesFromInput,
    metrics,
  }
}

export function snapshotClipboardEvent(e: ClipboardEvent): ClipboardSnapshot {
  const dt = e.clipboardData
  const types = Array.from(dt?.types || [])
  const fileMap = new Map<string, File>()
  const summaries: ClipboardDataItemSummary[] = []
  const stringReads: Promise<ClipboardStringItem>[] = []

  function addFile(file: File | null): boolean {
    if (!file) return false
    const key = `${file.name}|${file.type}|${file.size}|${file.lastModified}`
    if (!fileMap.has(key)) fileMap.set(key, file)
    return true
  }

  for (const file of Array.from(dt?.files || [])) {
    addFile(file)
  }

  for (const item of Array.from(dt?.items || [])) {
    let hasFile = false
    if (item.kind === 'file') {
      hasFile = addFile(item.getAsFile())
    } else if (item.kind === 'string') {
      stringReads.push(readStringItem(item.type, item))
    }
    summaries.push({ kind: item.kind, type: item.type || '', hasFile })
  }

  const files = Array.from(fileMap.values())
  const hasPlainText = types.includes('text/plain') || summaries.some(item => item.type === 'text/plain')
  const hasUriList = types.includes('text/uri-list') || summaries.some(item => item.type === 'text/uri-list')
  const hasFileHint = files.length > 0
    || types.some(type => type === 'Files' || type.toLowerCase() === 'application/x-moz-file')
    || summaries.some(item => item.kind === 'file')
  const hasImageHint = files.some(file => file.type.startsWith('image/'))
    || types.some(type => type.toLowerCase().startsWith('image/'))
    || summaries.some(item => item.type.toLowerCase().startsWith('image/'))

  return {
    types,
    files,
    items: summaries,
    stringReads,
    hasPlainText,
    hasUriList,
    hasFileHint,
    hasImageHint,
  }
}

export function formatPathsForPty(paths: string[]): string {
  const uniquePaths = uniqueOrderedPaths(paths)
  return uniquePaths.length > 0 ? uniquePaths.map(shellQuoteArg).join(' ') + ' ' : ''
}

export function uniqueOrderedPaths(paths: string[]): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const path of paths) {
    if (!path || seen.has(path)) continue
    seen.add(path)
    result.push(path)
  }
  return result
}

export function shouldProbeNativeClipboard(snapshot: ClipboardSnapshot, env: PasteEnvironment): boolean {
  return env.canUseServerNativeClipboard
    && (snapshot.hasFileHint || snapshot.hasUriList)
}

export function shouldPreferNativeClipboardPaths(snapshot: ClipboardSnapshot, env: PasteEnvironment): boolean {
  return shouldProbeNativeClipboard(snapshot, env)
}

function shouldTryAsyncClipboardImage(snapshot: ClipboardSnapshot, env: PasteEnvironment): boolean {
  return env.supportsAsyncClipboardRead
    && snapshot.files.length === 0
    && (snapshot.hasImageHint || (snapshot.hasFileHint && !snapshot.hasPlainText))
}

function preventPaste(e: ClipboardEvent): void {
  e.preventDefault()
  e.stopPropagation()
}

function detectPasteEnvironment(): PasteEnvironment {
  const profile = detectCliRuntimeProfile()
  const hostname = window.location.hostname || ''
  const protocol = window.location.protocol || ''
  const isLoopbackHost = ['127.0.0.1', 'localhost', '::1'].includes(hostname)
  return {
    runtimeMode: profile.mode,
    isWailsDesktop: profile.isWailsDesktop,
    canUseServerNativeClipboard: isLoopbackHost || profile.isWailsDesktop,
    isLoopbackHost,
    hostname,
    protocol,
    isSecureContext: window.isSecureContext,
    supportsAsyncClipboardRead: typeof navigator.clipboard?.read === 'function',
  }
}

function readStringItem(type: string, item: DataTransferItem): Promise<ClipboardStringItem> {
  return new Promise(resolve => {
    try {
      item.getAsString(value => resolve({ type, value: value || '' }))
    } catch {
      resolve({ type, value: '' })
    }
  })
}

async function readClipboardStrings(snapshot: ClipboardSnapshot): Promise<ClipboardStringItem[]> {
  if (snapshot.stringReads.length === 0) return []
  return Promise.all(snapshot.stringReads)
}

function extractFileUriPaths(strings: ClipboardStringItem[]): string[] {
  const uriText = strings
    .filter(item => item.type === 'text/uri-list')
    .flatMap(item => item.value.split(/\r?\n/))
  return uriText
    .map(line => line.trim())
    .filter(line => line && !line.startsWith('#'))
    .filter(line => line.startsWith('file://'))
    .map(uri => {
      try {
        return decodeURIComponent(uri.replace(/^file:\/\//, ''))
      } catch {
        return uri.replace(/^file:\/\//, '')
      }
    })
    .filter(Boolean)
}

function blobToFile(blob: Blob, filename: string, type: string): File {
  return new File([blob], filename, { type, lastModified: Date.now() })
}

function shellQuoteArg(value: string): string {
  if (/^[A-Za-z0-9_./:@%+=,-]+$/.test(value)) return value
  return `'${value.replace(/'/g, `'\\''`)}'`
}

function clipboardFingerprint(snapshot: ClipboardSnapshot): string {
  const files = snapshot.files
    .map(file => `${file.name}:${file.type}:${file.size}:${file.lastModified}`)
    .join('|')
  const items = snapshot.items
    .map(item => `${item.kind}:${item.type}:${item.hasFile ? '1' : '0'}`)
    .join('|')
  return `${snapshot.types.join('|')}::${files}::${items}`
}

function snapshotLog(snapshot: ClipboardSnapshot): Record<string, unknown> {
  return {
    types: snapshot.types,
    file_count: snapshot.files.length,
    files: snapshot.files.map(fileLog),
    item_count: snapshot.items.length,
    items: snapshot.items,
    has_plain_text: snapshot.hasPlainText,
    has_uri_list: snapshot.hasUriList,
    has_file_hint: snapshot.hasFileHint,
    has_image_hint: snapshot.hasImageHint,
  }
}

function environmentLog(env: PasteEnvironment): Record<string, unknown> {
  return {
    runtime_mode: env.runtimeMode,
    is_wails_desktop: env.isWailsDesktop,
    can_use_server_native_clipboard: env.canUseServerNativeClipboard,
    is_loopback_host: env.isLoopbackHost,
    hostname: env.hostname,
    protocol: env.protocol,
    is_secure_context: env.isSecureContext,
    supports_async_clipboard_read: env.supportsAsyncClipboardRead,
  }
}

function fileLog(file: File): Record<string, unknown> {
  return {
    name: file.name || '',
    mime: file.type || 'application/octet-stream',
    size: file.size,
    last_modified: file.lastModified || 0,
    image: (file.type || '').startsWith('image/'),
  }
}

function metricSnapshot(): ClipboardMetrics {
  return { ...metrics }
}

function fileUploadCandidateKey(file: File): string {
  return `${file.name || ''}|${file.type || 'application/octet-stream'}|${file.size}`
}

async function fileContentHash(file: File): Promise<string | null> {
  try {
    if (typeof crypto === 'undefined' || !crypto.subtle) return null
    const digest = await crypto.subtle.digest('SHA-256', await file.arrayBuffer())
    return Array.from(new Uint8Array(digest))
      .slice(0, 16)
      .map(byte => byte.toString(16).padStart(2, '0'))
      .join('')
  } catch {
    return null
  }
}

function uploadFallbackName(mime: string): string {
  return `clipboard.${extFromMime(mime)}`
}

function extFromMime(mime: string): string {
  const map: Record<string, string> = {
    'image/png': 'png',
    'image/jpeg': 'jpg',
    'image/gif': 'gif',
    'image/webp': 'webp',
    'image/svg+xml': 'svg',
    'application/pdf': 'pdf',
    'text/plain': 'txt',
    'text/markdown': 'md',
    'application/json': 'json',
  }
  return map[mime.toLowerCase()] || 'bin'
}
