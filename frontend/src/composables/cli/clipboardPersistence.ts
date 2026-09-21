// Async IndexedDB avoids serializing megabytes on every terminal paint. One
// record per authenticated endpoint; no clipboard text enters localStorage/logs.
let database: Promise<IDBDatabase> | undefined
function db(): Promise<IDBDatabase> {
  return (database ||= new Promise((resolve, reject) => {
    const req = indexedDB.open('dw-remote-clipboard', 1)
    req.onupgradeneeded = () => req.result.createObjectStore('history')
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error)
  }))
}
export async function readClipboardHistory(key: string): Promise<unknown> {
  const database = await db()
  return await new Promise((resolve, reject) => {
    const r = database.transaction('history').objectStore('history').get(key)
    r.onsuccess = () => resolve(r.result)
    r.onerror = () => reject(r.error)
  })
}
export async function saveClipboardHistory(
  key: string,
  value: unknown,
): Promise<void> {
  const database = await db()
  await new Promise<void>((resolve, reject) => {
    const t = database.transaction('history', 'readwrite')
    t.objectStore('history').put(value, key)
    t.oncomplete = () => resolve()
    t.onerror = () => reject(t.error)
    t.onabort = () => reject(t.error)
  })
}
