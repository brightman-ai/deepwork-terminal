/**
 * useTreeUpload — the 目录树 file-upload QUEUE with progress / cancel / resume.
 *
 * WHY a queue (not a one-shot POST): a directory-tree upload is the "sftp put" half — arbitrary
 * size, over a Cloudflare quick tunnel whose single request body caps at ~100MB, on flaky mobile
 * links. So every tree upload goes through the CHUNKED protocol (api/files.ts chunkedUpload*):
 * slice the file into server-sized chunks, send the missing ones, reassemble server-side. This is
 * a DIFFERENT bounded context from clipboard-paste (small images, dedup + PTY-inject resolver) —
 * that stays on the single-shot /paste-upload path; do not fold the two together.
 *
 * Resumability is SERVER-anchored: the uploadId is derived from cwd|dir|name|size, so a failed
 * upload's already-landed chunks persist on disk and a plain retry (even after a reload) re-inits
 * to the same id and skips them. The client keeps no durable state — the disk IS the truth.
 *
 * Files upload one-at-a-time, chunks in order, each chunk retried a few times before the job parks
 * as 'error' (resumable) rather than restarting — friendliest to a weak link, and progress stays
 * monotonic. One instance per FilesPanel (its own reactive queue), not a module singleton.
 */
import { ref } from 'vue'
import {
  chunkedUploadInit,
  chunkedUploadChunk,
  chunkedUploadComplete,
  chunkedUploadAbort,
  type ChunkCompleteResult,
} from '@terminal/api/files'

export type UploadJobStatus = 'active' | 'done' | 'error' | 'canceled'

export interface UploadJob {
  /** Stable client id (v-for key + cancel/retry handle). */
  id: string
  name: string
  size: number
  /** cwd-relative target directory ('.' = root). */
  dir: string
  /** Server upload id, empty until init succeeds. */
  uploadId: string
  /** Chunks confirmed landed (drives the progress bar). */
  sent: number
  /** Total chunks (0 until init). */
  total: number
  status: UploadJobStatus
  /** Human reason when status==='error'. */
  error: string
}

const CHUNK_RETRIES = 3

/** onComplete(dir, result): a job finished landing — the panel refreshes that dir + recent. */
export function useTreeUpload(opts: {
  sessionId: () => string
  cwd: () => string | undefined
  onComplete: (dir: string, result: ChunkCompleteResult) => void
}) {
  const jobs = ref<UploadJob[]>([])
  let seq = 0
  // Cancel is cooperative: the pipeline checks this set between chunks. A set (not a job flag)
  // survives the job object being reactive-replaced and reads cleanly from inside async loops.
  const canceled = new Set<string>()
  // The File blobs live OUTSIDE reactivity (a Vue proxy around a File breaks .slice on some
  // engines) and outlive the <input> that gets cleared — so retry can re-slice without the caller
  // re-picking. Dropped when a job settles-and-is-cleared or is canceled.
  const fileOf = new Map<string, File>()

  function find(id: string): UploadJob | undefined {
    return jobs.value.find((j) => j.id === id)
  }

  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

  async function sendChunk(uploadId: string, index: number, blob: Blob): Promise<boolean> {
    for (let attempt = 0; attempt < CHUNK_RETRIES; attempt++) {
      if (await chunkedUploadChunk(uploadId, index, blob)) return true
      await sleep(300 * (attempt + 1)) // small linear backoff between retries
    }
    return false
  }

  // Runs one file to completion (or error/cancel). Idempotent w.r.t. its own job — safe to
  // re-invoke on retry: init returns the already-received set and we skip those chunks.
  async function runJob(job: UploadJob, file: File): Promise<void> {
    job.status = 'active'
    job.error = ''
    const init = await chunkedUploadInit(opts.sessionId(), file, job.dir, opts.cwd())
    if (canceled.has(job.id)) return
    if (!init.ok) {
      job.status = 'error'
      job.error =
        init.status === 413
          ? `超过上传上限${init.limitMb ? ` ${init.limitMb} MB` : ''}（可在 ⚙ 调高或压缩）`
          : '登记失败，请重试'
      return
    }
    const { uploadId, chunkSize, totalChunks, received } = init.info
    job.uploadId = uploadId
    job.total = totalChunks
    const have = new Set(received)
    job.sent = have.size
    for (let i = 0; i < totalChunks; i++) {
      if (canceled.has(job.id)) return
      if (have.has(i)) continue
      const blob = file.slice(i * chunkSize, Math.min((i + 1) * chunkSize, file.size))
      if (!(await sendChunk(uploadId, i, blob))) {
        if (canceled.has(job.id)) return
        job.status = 'error'
        job.error = '网络中断，可点重试续传' // landed chunks persist server-side → retry resumes
        return
      }
      job.sent++
    }
    if (canceled.has(job.id)) return
    const result = await chunkedUploadComplete(opts.sessionId(), uploadId)
    if (!result) {
      job.status = 'error'
      job.error = '合并失败，可点重试'
      return
    }
    job.status = 'done'
    opts.onComplete(job.dir, result)
  }

  // The queue runs strictly one file at a time (a shared runner promise chain) so a batch of big
  // files can't saturate a weak uplink or race the same target dir. runJob MUST mutate the LIVE
  // reactive element (find(id)) — the pushed-in object is the raw target, and mutating it directly
  // bypasses the ref proxy so the progress bar would never move (the same prod-only reactivity trap
  // the tree hit). find() returns the proxied element, so mutations through it are tracked.
  let tail: Promise<void> = Promise.resolve()
  function schedule(id: string): void {
    tail = tail.then(() => {
      const file = fileOf.get(id)
      const live = find(id)
      if (!live || !file || canceled.has(id)) return Promise.resolve()
      return runJob(live, file)
    })
  }

  /** Enqueue picked files for upload into `dir` (cwd-relative). */
  function enqueue(files: File[], dir: string): void {
    for (const file of files) {
      const job: UploadJob = {
        id: `up-${++seq}`,
        name: file.name,
        size: file.size,
        dir: dir || '.',
        uploadId: '',
        sent: 0,
        total: 0,
        status: 'active',
        error: '',
      }
      fileOf.set(job.id, file)
      jobs.value.push(job)
      schedule(job.id)
    }
  }

  /** Retry a parked (error) job — re-runs the pipeline, which resumes from received chunks. */
  function retry(id: string): void {
    const job = find(id)
    if (!job || !fileOf.has(id)) return
    canceled.delete(id)
    job.status = 'active'
    schedule(id)
  }

  /** Cancel a job: stop scheduling more chunks + abort the server's staging (best-effort). */
  function cancel(id: string): void {
    const job = find(id)
    if (!job) return
    canceled.add(id)
    job.status = 'canceled'
    fileOf.delete(id)
    if (job.uploadId) void chunkedUploadAbort(job.uploadId)
  }

  /** Drop finished/canceled/errored rows from the visible list. */
  function clearSettled(): void {
    for (const j of jobs.value) if (j.status !== 'active') fileOf.delete(j.id)
    jobs.value = jobs.value.filter((j) => j.status === 'active')
  }

  return { jobs, enqueue, retry, cancel, clearSettled }
}
