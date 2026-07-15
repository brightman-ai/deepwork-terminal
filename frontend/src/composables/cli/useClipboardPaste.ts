/**
 * useClipboardPaste — Multi-type clipboard reader for terminal paste.
 *
 * Reads text, images, and file URIs from browser clipboard on paste events.
 * Images are uploaded to the server via REST; file paths are extracted from URIs.
 * The composable returns resolved content ready for PTY injection.
 *
 * Transport: REST multipart for images (not WS base64) to avoid blocking PTY frames.
 */
import { ref } from 'vue'
import { useCliAuth } from '@terminal/composables/cli/useCliAuth'
import { createUploadProgressStore } from '@terminal/composables/cli/useUploadProgress'
import { classifyUploadFailure, type UploadErrorBody } from '@terminal/composables/cli/uploadFailure'
import { pasteUploadHeaders, resolvePasteUploadTarget } from '@terminal/composables/cli/pasteUploadTarget'
import { createLogger, traceHeaders, type TraceContext } from '@ce/utils/obs'
import { apiUrl } from '@ce/utils/runtimeBase'

export interface PasteResult {
  type: 'text' | 'image' | 'file' | 'none'
  /** Text content for PTY injection (path for images, extracted path for files, raw text for text) */
  textForPTY: string
  /** Server-side path (only for uploaded images) */
  savedPath?: string
  /** Whether the file path refers to the local machine (vs remote client) */
  isLocal: boolean
  filename?: string
  mime?: string
  size?: number
  dedup?: boolean
}

export interface PasteUploadOptions {
  filename?: string
  source?: string
  trace?: TraceContext
  /** Wired by the caller that knows how to redo the FULL upload→inject flow
   * (useCliPasteResolver) — useClipboardPaste only owns the raw HTTP call, so it
   * cannot build a meaningful retry on its own. Stored on the progress-store entry
   * so the upload float's 重试 button can invoke it. */
  retry?: () => void
  /** Skip the 300ms delayed-reveal grace period. Used for retries the user just
   * triggered, where instant feedback beats hiding a fast retry. */
  revealImmediately?: boolean
}

export interface ClipboardPasteTarget {
  sessionId: () => string
  isRemote?: () => boolean
  httpBase?: () => string | undefined
  authToken?: () => string | undefined
  activeCwd?: () => string | undefined
}

const log = createLogger('cli-clipboard-paste')

/** Detect if the browser is accessing from the same machine as the server */
function detectIsLocal(): boolean {
  const host = window.location.hostname
  return host === '127.0.0.1' || host === 'localhost' || host === '::1'
}

export function useClipboardPaste(targetOrSessionId: ClipboardPasteTarget | (() => string)) {
  const target: ClipboardPasteTarget = typeof targetOrSessionId === 'function'
    ? { sessionId: targetOrSessionId }
    : targetOrSessionId
  const sessionId = target.sessionId
  const uploading = ref(false)
  const lastError = ref('')
  const { getAuthCode, clearAuthCode, promptAuth } = useCliAuth()
  // Per-tab SSOT of in-flight uploads (see useUploadProgress.ts). One instance per
  // useClipboardPaste() call — useCliPasteResolver creates exactly one per terminal
  // surface, so uploads never leak across tabs.
  const uploadProgress = createUploadProgressStore()

  /**
   * Process a paste event. Returns resolved PasteResult.
   * For images: uploads to server, returns saved path.
   * For file URIs: extracts path string.
   * For text: returns raw text.
   */
  async function processPaste(e: ClipboardEvent): Promise<PasteResult> {
    const items = e.clipboardData?.items
    if (!items || items.length === 0) {
      return { type: 'none', textForPTY: '', isLocal: detectIsLocal() }
    }

    const isLocal = detectIsLocal()

    // Read text/uri-list eagerly — synchronously, BEFORE any await — so the image
    // branch below can recover a copied file's original name from it without touching
    // a possibly-neutered DataTransferItemList after the first await. Some platforms
    // only expose the real filename via the file:// URI, not on the image blob.
    let uriListPromise: Promise<string> | null = null
    for (const item of items) {
      if (item.type === 'text/uri-list') { uriListPromise = getItemAsString(item); break }
    }

    // Priority 1: Check for image
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const blob = item.getAsFile()
        if (!blob) continue
        // Preserve the ORIGINAL filename when the user copied an image FILE. The File
        // carries it on most platforms; else recover the basename from the file:// URI.
        // A true nameless bitmap (screenshot) has neither → the clipboard.<ext>
        // placeholder tells the server to synthesize a hash name. Previously this
        // path always sent "clipboard.png", so "foo.png" was lost before upload.
        const uriName = uriListPromise ? basenameFromUri(await uriListPromise) : undefined
        const filename = firstRealName([(blob as File).name, uriName]) || `clipboard.${extFromMime(item.type)}`
        return await uploadFile(blob, item.type, { filename, source: 'paste-image' })
      }
    }

    // Priority 2: Check for file URI (text/uri-list)
    for (const item of items) {
      if (item.type === 'text/uri-list') {
        const uriText = await getItemAsString(item)
        const paths = extractFilePaths(uriText)
        if (paths.length > 0) {
          return {
            type: 'file',
            textForPTY: paths.join(' '),
            isLocal,
          }
        }
      }
    }

    // Priority 3: Plain text (fallback)
    for (const item of items) {
      if (item.type === 'text/plain') {
        const text = await getItemAsString(item)
        if (text) {
          return { type: 'text', textForPTY: text, isLocal }
        }
      }
    }

    // Priority 4: Any remaining file blob (docx/xlsx/pptx/zip/…). The earlier
    // image branch only catches image/* MIMEs, so an office/archive file pasted
    // as a blob would otherwise fall through to {type:'none'} and vanish. Upload
    // it via the same REST path images use — deepwork stores the file and the
    // agent reads it from the injected path (zero text extraction here).
    for (const item of items) {
      if (item.kind === 'file') {
        const blob = item.getAsFile()
        if (!blob) continue
        const mime = blob.type || 'application/octet-stream'
        return await uploadFile(blob, mime, { source: 'paste-event-file' })
      }
    }

    return { type: 'none', textForPTY: '', isLocal }
  }

  async function uploadFile(blob: Blob, mime: string, options: PasteUploadOptions = {}): Promise<PasteResult> {
    const local = detectIsLocal()
    uploading.value = true
    lastError.value = ''
    const startedAt = performance.now()
    const uploadName = uploadFilename(blob, mime, options.filename)
    const uploadKind = mime.toLowerCase().startsWith('image/') ? 'image' : 'file'
    // Register BEFORE the request starts so the 300ms delayed-reveal clock (or an
    // immediate reveal for a user-triggered retry) starts at the true beginning of
    // the upload. The retry wired here removes THIS entry then defers to the
    // caller's retry (useCliPasteResolver knows how to redo upload→inject; this
    // composable only owns the raw HTTP call) — so a retry never leaves a stale
    // duplicate entry sitting next to the fresh one.
    const entryId = uploadProgress.register(uploadName, blob.size, () => {
      uploadProgress.remove(entryId)
      options.retry?.()
    }, options.revealImmediately)
    try {
      const formData = new FormData()
      formData.append('file', blob, uploadName)
      formData.append('mime', mime)
      // Supplied by the terminal surface's live WS-backed tmux state. In particular,
      // a remote tab must not instantiate/fetch a same-origin tmux store just to route cwd.
      const cwd = target.activeCwd?.() || ''
      if (cwd) formData.append('cwd', cwd)

      // XHR, not fetch: only XMLHttpRequest exposes `upload.onprogress`, which is
      // the ONLY way to get real sent/total bytes for the progress float. Same
      // endpoint, response shape, and auth headers as cliFetch — duplicated by
      // hand here since cliFetch itself wraps fetch() and can't drive an XHR.
      const isRemote = !!target.isRemote?.()
      const uploadTarget = resolvePasteUploadTarget({
        sessionId: sessionId(),
        isRemote,
        httpBase: target.httpBase?.(),
        localAuth: getAuthCode(),
        remoteAuth: target.authToken?.(),
      })
      // Existing peers allow the RT-8 minimum CORS header set. Do not make a remote upload
      // depend on a peer upgrade by adding this host's observability header to its preflight;
      // client-side logs still carry the trace locally. Local same-origin requests keep it.
      const headers = pasteUploadHeaders(uploadTarget, traceHeaders(options.trace))
      const url = apiUrl(uploadTarget.url)
      const { status, json } = await xhrUploadFormData(url, formData, headers, (sent, total) => {
        uploadProgress.progress(entryId, sent, total || blob.size)
      })

      // Mirror cliFetch's 401/429 handling (clear stale code + prompt / throttle
      // notice) — XHR bypasses cliFetch entirely so this can't be shared, only
      // reproduced exactly via the same public useCliAuth() actions.
      if (!uploadTarget.isRemote && status === 401) { clearAuthCode(); promptAuth() }
      if (!uploadTarget.isRemote && status === 429) { promptAuth() }

      if (status < 200 || status >= 300) {
        const body = json as UploadErrorBody | null
        const { message, retryable } = classifyUploadFailure(status, body)
        lastError.value = message
        uploadProgress.fail(entryId, message, retryable)
        log.warn('cli.clipboard.upload_failed', {
          source: options.source || 'unknown',
          upload_kind: uploadKind,
          mime,
          filename: uploadName,
          size: blob.size,
          status,
          // The server's own words, kept for diagnosis even though the user sees `message`.
          error: body?.error || `HTTP ${status}`,
          retryable,
          target: uploadTarget.isRemote ? 'remote' : 'local',
          elapsed_ms: Math.round(performance.now() - startedAt),
        }, options.trace)
        return { type: 'none', textForPTY: '', isLocal: local }
      }

      const data = json as { path: string; relPath: string; size: number; filename?: string; dedup?: boolean }
      uploadProgress.complete(entryId)
      // Single chokepoint for "an upload landed on disk": let the resource drawer
      // (and any other listener) refresh its /uploads listing without coupling.
      if (!uploadTarget.isRemote && typeof window !== 'undefined') {
        window.dispatchEvent(new CustomEvent('dw:upload-success', {
          detail: { sessionId: sessionId(), relPath: data.relPath, kind: uploadKind },
        }))
      }
      log.info('cli.clipboard.upload_completed', {
        source: options.source || 'unknown',
        upload_kind: uploadKind,
        mime,
        filename: data.filename || uploadName,
        size: data.size,
        rel_path: data.relPath || '',
        dedup: !!data.dedup,
        target: uploadTarget.isRemote ? 'remote' : 'local',
        elapsed_ms: Math.round(performance.now() - startedAt),
      }, options.trace)
      return {
        type: uploadKind,
        textForPTY: data.relPath || data.path,
        savedPath: data.path,
        isLocal: local,
        filename: data.filename || uploadName,
        mime,
        size: data.size,
        dedup: !!data.dedup,
      }
    } catch (err) {
      // Transport died (offline, dropped connection) — genuinely transient, so this one
      // KEEPS its retry. The raw message goes to the log, not to the user.
      const raw = err instanceof Error ? err.message : 'upload failed'
      lastError.value = '网络中断，上传未完成'
      uploadProgress.fail(entryId, lastError.value, true)
      log.warn('cli.clipboard.upload_exception', {
        source: options.source || 'unknown',
        upload_kind: uploadKind,
        mime,
        filename: uploadName,
        size: blob.size,
        error: raw,
        elapsed_ms: Math.round(performance.now() - startedAt),
      }, options.trace)
      return { type: 'none', textForPTY: '', isLocal: local }
    } finally {
      uploading.value = false
    }
  }

  /** Upload image blob to server, return path for PTY injection. Exposed for direct use. */
  async function uploadImage(blob: Blob, mime: string, isLocal?: boolean): Promise<PasteResult> {
    void isLocal
    // Keep the image's real filename when it has one (a copied file); fall back to
    // the clipboard.<ext> placeholder only for a nameless bitmap.
    const filename = firstRealName([(blob as File).name]) || `clipboard.${extFromMime(mime)}`
    return uploadFile(blob, mime, { filename, source: 'legacy-image' })
  }

  return { processPaste, uploadFile, uploadImage, uploading, lastError, uploads: uploadProgress.entries }
}

interface XhrUploadResult {
  status: number
  json: unknown
}

/** POST a FormData body via XMLHttpRequest and resolve with {status, json}. The
 * only reason this exists instead of fetch(): XHR is the sole browser API that
 * exposes `upload.onprogress` (real bytes-sent), which fetch cannot report. */
function xhrUploadFormData(
  url: string,
  formData: FormData,
  headers: Record<string, string>,
  onProgress: (sent: number, total: number) => void,
): Promise<XhrUploadResult> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', url, true)
    for (const [key, value] of Object.entries(headers)) {
      xhr.setRequestHeader(key, value)
    }
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded, e.total)
    }
    xhr.onload = () => {
      let json: unknown = null
      try {
        json = xhr.responseText ? JSON.parse(xhr.responseText) : null
      } catch {
        json = null
      }
      resolve({ status: xhr.status, json })
    }
    xhr.onerror = () => reject(new Error('network error'))
    xhr.ontimeout = () => reject(new Error('upload timed out'))
    xhr.send(formData)
  })
}

function uploadFilename(blob: Blob, mime: string, explicit?: string): string {
  const fileName = explicit || ((blob as File).name || '')
  if (fileName && fileName !== 'blob') return fileName
  return `clipboard.${extFromMime(mime)}`
}

/** First candidate that looks like a real user filename — not empty, not the "blob"
 * placeholder, not a browser-generated nameless-bitmap name ("image.png") or our own
 * "clipboard.<ext>". Mirrors the server's isGenericClipboardName so both ends agree
 * on which names get preserved vs synthesized. */
function firstRealName(candidates: (string | undefined)[]): string | undefined {
  for (const c of candidates) {
    const n = (c || '').trim()
    if (!n) continue
    const lower = n.toLowerCase()
    if (lower === 'blob') continue
    const stem = lower.replace(/\.[^.]*$/, '')
    if (stem === 'image' || stem === 'clipboard') continue
    return n
  }
  return undefined
}

/** Basename of the first file:// path in a text/uri-list payload, if any. */
function basenameFromUri(uriText: string): string | undefined {
  const paths = extractFilePaths(uriText)
  if (paths.length === 0) return undefined
  const base = paths[0].split('/').pop()?.split('\\').pop()
  return base || undefined
}

/** Extract file paths from text/uri-list (one URI per line, skip comments) */
function extractFilePaths(uriText: string): string[] {
  return uriText
    .split('\n')
    .map(line => line.trim())
    .filter(line => line && !line.startsWith('#'))
    .map(uri => {
      if (uri.startsWith('file://')) {
        // Decode percent-encoded characters and strip file:// prefix
        try {
          return decodeURIComponent(uri.replace(/^file:\/\//, ''))
        } catch {
          return uri.replace(/^file:\/\//, '')
        }
      }
      return uri
    })
    .filter(Boolean)
}

function extFromMime(mime: string): string {
  const map: Record<string, string> = {
    'image/png': 'png',
    'image/jpeg': 'jpg',
    'image/gif': 'gif',
    'image/webp': 'webp',
    'image/svg+xml': 'svg',
    'application/pdf': 'pdf',
    'application/msword': 'doc',
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document': 'docx',
    'application/vnd.ms-excel': 'xls',
    'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet': 'xlsx',
    'application/vnd.ms-powerpoint': 'ppt',
    'application/vnd.openxmlformats-officedocument.presentationml.presentation': 'pptx',
    'application/zip': 'zip',
  }
  return map[mime.toLowerCase()] || 'bin'
}

function getItemAsString(item: DataTransferItem): Promise<string> {
  return new Promise(resolve => {
    item.getAsString(s => resolve(s || ''))
  })
}
