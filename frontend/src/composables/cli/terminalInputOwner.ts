import type { InjectionKey } from 'vue'

/** The owning portal can temporarily give terminal input to a modal. Per tree,
 * not a module-global flag: other portals/instances keep their own keyboard. */
export const terminalInputBlockedKey: InjectionKey<() => boolean> = Symbol('terminal-input-blocked')
