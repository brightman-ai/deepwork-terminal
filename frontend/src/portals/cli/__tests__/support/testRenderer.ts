/**
 * 一个最小的内存渲染器：没有 DOM 环境（这个仓库的测试都跑在纯 bun 里）也能把 Vue 组件真的挂起来、
 * 响应式更新、触发它绑的事件监听。只实现 CliTerminalView 视图接线测试用得到的部分。
 */
import { createRenderer, defineComponent, h, reactive, type Component } from 'vue'

export interface TNode {
  kind: 'element' | 'text' | 'comment'
  tag: string
  /** patchProp 收到的一切：属性、class、style 对象、onXxx 监听。 */
  props: Record<string, unknown>
  /** v-show 操作的 el.style（和 props.style 是两回事）。 */
  style: { display: string }
  children: TNode[]
  parent: TNode | null
  text: string
  clientWidth: number
  clientHeight: number
}

function node(kind: TNode['kind'], tag = '', text = ''): TNode {
  return { kind, tag, props: {}, style: { display: '' }, children: [], parent: null, text, clientWidth: 0, clientHeight: 0 }
}

function detach(child: TNode): void {
  if (!child.parent) return
  child.parent.children.splice(child.parent.children.indexOf(child), 1)
  child.parent = null
}

const { createApp } = createRenderer<TNode, TNode>({
  querySelector: selector => selector === 'body' ? teleportHost : null,
  createElement: (tag) => node('element', tag),
  createText: (text) => node('text', '', text),
  createComment: (text) => node('comment', '', text),
  setText: (n, text) => { n.text = text },
  setElementText: (el, text) => {
    el.children.forEach((c) => { c.parent = null })
    el.children = []
    if (text) {
      const t = node('text', '', text)
      t.parent = el
      el.children.push(t)
    }
  },
  insert: (child, parent, anchor) => {
    detach(child)
    const at = anchor ? parent.children.indexOf(anchor) : -1
    parent.children.splice(at < 0 ? parent.children.length : at, 0, child)
    child.parent = parent
  },
  remove: detach,
  parentNode: (n) => n.parent,
  nextSibling: (n) => n.parent?.children[n.parent.children.indexOf(n) + 1] ?? null,
  patchProp: (el, key, _prev, next) => {
    if (next == null) delete el.props[key]
    else el.props[key] = next
  },
})

const teleportHost = node('element', 'body')

export function walk(root: TNode, visit: (n: TNode) => void): void {
  visit(root)
  root.children.forEach((c) => walk(c, visit))
}

export function findAll(root: TNode, pred: (n: TNode) => boolean): TNode[] {
  const out: TNode[] = []
  walk(root, (n) => { if (pred(n)) out.push(n) })
  return out
}

export function byTestId(root: TNode, id: string): TNode[] {
  return findAll(root, (n) => n.props['data-testid'] === id)
}

/** v-show 把 display 写在 el.style.display：'none' = 被藏起。 */
export function isShown(n: TNode): boolean {
  return n.style.display !== 'none'
}

export function classList(n: TNode): string[] {
  const c = n.props.class
  return typeof c === 'string' ? c.split(/\s+/).filter(Boolean) : []
}

/** 调用节点上的事件监听（Vue 把 @click 存成 onClick，@pointerdown.capture.passive 存成
 *  onPointerdownCapturePassive）。找不到监听就抛——测试要的是"这个事件确实有人听"。 */
export function fire(n: TNode, prop: string, ...args: unknown[]): void {
  const handler = n.props[prop]
  const fns = Array.isArray(handler) ? handler : [handler]
  if (!fns.length || fns.some((f) => typeof f !== 'function')) {
    throw new Error(`no ${prop} listener on <${n.tag}>`)
  }
  fns.forEach((f) => (f as (...a: unknown[]) => void)(...args))
}

export function mountView<P extends Record<string, unknown>>(comp: Component, initial: P) {
  const props = reactive({ ...initial }) as P
  const host = node('element', 'host')
  const app = createApp(defineComponent({ render: () => h(comp, props) }))
  app.mount(host)
  return { host, teleports: teleportHost, props, unmount: () => app.unmount() }
}
