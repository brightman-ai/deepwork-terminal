/**
 * useTypeToComposeSetting — 把「打字即成框」总开关绑到服务端 KV（terminal-input REQ-TI-08）。
 *
 * 存在服务端 KV（useServerStore）而不是 localStorage：这是用户偏好，而 trycloudflare 每次换域名
 * localStorage 就整个换了一份——关掉的开关会在新域名上悄悄变回开。借的是同一条已经带"未加载成功
 * 不落盘"保护的通道（见 useShortcutsConfig 头注）。
 *
 * 单例：设置页和每个终端 surface 读的是同一个 ref，设置页一改，已打开的终端立刻生效。
 * 逻辑本体在 typeToComposeSetting.ts（可以脱离 store 单独测试）。
 */
import { createTypeToComposeSetting } from './typeToComposeSetting'
import { useServerStore } from './useServerStore'

let shared: ReturnType<typeof createTypeToComposeSetting> | null = null

export function useTypeToComposeSetting() {
  return (shared ??= createTypeToComposeSetting(useServerStore()))
}
