/** Session attachment begins only after its output consumer is installed.
 * A hidden, lazily mounted terminal can become active before xterm emits ready.
 * Opening its socket then loses the initial replay, leaving only later deltas. */
export function createTerminalConnectionGate(connect: () => void) {
  let ready = false
  let requested = false
  let disposed = false
  function flush() {
    if (!ready || !requested || disposed) return
    requested = false
    connect()
  }
  return {
    request() { requested = true; flush() },
    receiverAttached() { ready = true; flush() },
    dispose() { disposed = true; requested = false },
  }
}
