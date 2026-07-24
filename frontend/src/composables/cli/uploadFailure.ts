/**
 * classifyUploadFailure — turn an HTTP status into the two things the user actually needs:
 * what went wrong (in their language), and whether pressing 重试 can possibly help.
 *
 * That second answer is the point. A 4xx is the server's verdict on THESE bytes — it will
 * reach the same verdict every time — so offering a retry is a lie the user pays a full
 * re-upload to disprove. It happened: a .drawio refused by a server-side MIME allowlist
 * showed 重试, the user pressed it, and the file uploaded all over again only to be refused
 * identically 7 seconds later. 5xx and transport faults are conditions of the moment; those
 * keep the button honestly.
 *
 * Lives in its own module (not inside useClipboardPaste) because it is a pure function and
 * must stay testable without dragging in that module's browser-only side effects.
 */
export interface UploadFailure {
  message: string
  retryable: boolean
}

export interface UploadErrorBody {
  error?: string
  limit_mb?: number
}

export function classifyUploadFailure(status: number, body: UploadErrorBody | null): UploadFailure {
  if (status === 413) {
    // limit_mb is sent by the server (clipboardMaxUploadSize). Never hardcode it here — two
    // copies of one number drift exactly the way the two upload allowlists did.
    const mb = body?.limit_mb
    // Actionable next step: this channel accepts ANY file, so a large one usually compresses
    // well — point the user at zip rather than leaving them stuck at a hard "too big". (The
    // number still comes from the server's limit_mb; only the suggestion is fixed text.)
    return {
      message: mb ? `文件超过 ${mb} MB 上限，可压缩成 zip 再上传` : '文件超过大小上限，可压缩成 zip 再上传',
      retryable: false,
    }
  }
  if (status === 401 || status === 429) {
    // The only 4xx the user can clear: the caller has already re-prompted for the auth code,
    // and once it is accepted the same bytes go through untouched.
    return { message: '认证已失效，重新认证后可重试', retryable: true }
  }
  if (status >= 400 && status < 500) {
    // A case we did not anticipate. Keep the server's own words: an English string the user
    // can quote back to us beats a vague Chinese one we invented.
    return { message: body?.error || `上传被拒绝 (HTTP ${status})`, retryable: false }
  }
  return { message: `服务端错误 (HTTP ${status})`, retryable: true }
}
