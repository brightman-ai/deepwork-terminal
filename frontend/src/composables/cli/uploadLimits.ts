/**
 * uploadLimits — the single FRONTEND source for the SESSION file-upload cap, shared by every
 * client-side pre-check (attach dialogs, WebChat's useWsAttachments in deepwork-pro) so the
 * number is written ONCE, not copied per composable the way it drifted before.
 *
 * SSOT relationship with the backend: the server (deepwork-terminal `ClipboardMaxUploadSize`,
 * which deepwork-pro's session upload route imports) is the AUTHORITY — it enforces the limit
 * and returns `limit_mb` with a 413 so the real rejection message never hardcodes a number
 * (see uploadFailure.ts). This constant exists only to pre-empt an obviously-too-large upload
 * before spending the round-trip; it is a UX shortcut, not a second source of truth. Keep it in
 * step with the backend const (both are 10). It is a DIFFERENT fact from chat-image attachments
 * (useChatImageAttachments — its own smaller cap, a separate bounded context): do not unify them.
 */
export const SESSION_UPLOAD_MAX_MB = 10
export const SESSION_UPLOAD_MAX_BYTES = SESSION_UPLOAD_MAX_MB * 1024 * 1024
