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
import { cliApi } from '@terminal/composables/cli/useCliApiPrefix'
import { createLogger, traceHeaders, type TraceContext } from '@ce/utils/obs'

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
}

const log = createLogger('cli-clipboard-paste')

/** Detect if the browser is accessing from the same machine as the server */
function detectIsLocal(): boolean {
  const host = window.location.hostname
  return host === '127.0.0.1' || host === 'localhost' || host === '::1'
}

export function useClipboardPaste(sessionId: () => string) {
  const uploading = ref(false)
  const lastError = ref('')
  const { cliFetch } = useCliAuth()

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

    // Priority 1: Check for image
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const blob = item.getAsFile()
        if (!blob) continue
        return await uploadImage(blob, item.type, isLocal)
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

    return { type: 'none', textForPTY: '', isLocal }
  }

  async function uploadFile(blob: Blob, mime: string, options: PasteUploadOptions = {}): Promise<PasteResult> {
    const local = detectIsLocal()
    uploading.value = true
    lastError.value = ''
    const startedAt = performance.now()
    const uploadName = uploadFilename(blob, mime, options.filename)
    const uploadKind = mime.toLowerCase().startsWith('image/') ? 'image' : 'file'
    try {
      const formData = new FormData()
      formData.append('file', blob, uploadName)
      formData.append('mime', mime)

      const resp = await cliFetch(cliApi(`/sessions/${sessionId()}/paste-upload`), {
        method: 'POST',
        body: formData,
        headers: traceHeaders(options.trace),
      })

      if (!resp.ok) {
        const err = await resp.json().catch(() => ({ error: `HTTP ${resp.status}` }))
        lastError.value = (err as { error?: string }).error || `Upload failed: ${resp.status}`
        log.warn('cli.clipboard.upload_failed', {
          source: options.source || 'unknown',
          upload_kind: uploadKind,
          mime,
          filename: uploadName,
          size: blob.size,
          status: resp.status,
          error: lastError.value,
          elapsed_ms: Math.round(performance.now() - startedAt),
        }, options.trace)
        return { type: 'none', textForPTY: '', isLocal: local }
      }

      const data = await resp.json() as { path: string; relPath: string; size: number; filename?: string; dedup?: boolean }
      // Single chokepoint for "an upload landed on disk": let the resource drawer
      // (and any other listener) refresh its /uploads listing without coupling.
      if (typeof window !== 'undefined') {
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
      lastError.value = err instanceof Error ? err.message : 'Upload failed'
      log.warn('cli.clipboard.upload_exception', {
        source: options.source || 'unknown',
        upload_kind: uploadKind,
        mime,
        filename: uploadName,
        size: blob.size,
        error: lastError.value,
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
    return uploadFile(blob, mime, { filename: `clipboard.${extFromMime(mime)}`, source: 'legacy-image' })
  }

  return { processPaste, uploadFile, uploadImage, uploading, lastError }
}

function uploadFilename(blob: Blob, mime: string, explicit?: string): string {
  const fileName = explicit || ((blob as File).name || '')
  if (fileName && fileName !== 'blob') return fileName
  return `clipboard.${extFromMime(mime)}`
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
  }
  return map[mime.toLowerCase()] || 'bin'
}

function getItemAsString(item: DataTransferItem): Promise<string> {
  return new Promise(resolve => {
    item.getAsString(s => resolve(s || ''))
  })
}
