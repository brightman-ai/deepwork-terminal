/**
 * composeTypedTrigger — 「这个输入框是被一次敲键唤起的」这件事的账（terminal-input REQ-TI-03）。
 *
 * 框是被一次终端从未见过的按键打开的。要让"误拦截一键撤回"成立，必须记住两件事：那次按键是什么，
 * 以及打开之前草稿是什么样。这样在框里只有那次输入、没被动过的时候按 Esc，才能把那次按键原样还给
 * 终端，并让草稿恢复成打字之前的样子 —— 一次判断错了，不丢一个字，也不往草稿里留一个不属于它的字。
 *
 * 纯函数、不碰 Vue 和 localStorage：账本身很容易错（输入法定稿滞后、草稿里原本就有字、编辑又改回来），
 * 值得脱离组件单独钉住；useComposeBox 只负责把结果写回 ref 和存草稿。
 */

/**
 * typed === null：框是被输入法唤起的，拼写还没定稿，触发内容未知；第一段定稿后才知道（noteCompositionEnd /
 * adoptCommitted）。baseline 是"触发刚被放进框时框里的全文"，之后任何编辑都会让全文偏离它。
 */
export interface TypedTrigger {
  typed: string | null
  draftBefore: string
  baseline: string | null
}

/** 可见字符唤起：字追加在已有草稿后面。 */
export function beginTyped(currentText: string, typed: string): { trigger: TypedTrigger; text: string } {
  const text = currentText + typed
  return { trigger: { typed, draftBefore: currentText, baseline: text }, text }
}

/** 输入法唤起：拼写发生在框里，定稿之前触发内容未知。 */
export function beginIme(currentText: string): TypedTrigger {
  return { typed: null, draftBefore: currentText, baseline: null }
}

/**
 * 别处拼完的定稿文字（Safari 的事件顺序：拼写发生在终端自己的 textarea 里）交给框。
 * 追加到全文末尾；若这是输入法唤起后的第一段定稿，它就是触发内容。
 */
export function adoptCommitted(
  trigger: TypedTrigger | null,
  currentText: string,
  committed: string,
): { trigger: TypedTrigger | null; text: string } {
  const text = currentText + committed
  if (trigger && trigger.typed === null && committed) {
    return { trigger: { typed: committed, draftBefore: trigger.draftBefore, baseline: text }, text }
  }
  return { trigger, text }
}

/**
 * 框里的一段拼写定稿了。输入法唤起后的**第一段**定稿定义了"触发是什么"：它是全文里草稿之后的那部分。
 * 之后的定稿是用户在编辑，不改账。定稿为空（拼写被取消）或全文已不以原草稿开头 → 没有可撤回的触发。
 */
export function noteCompositionEnd(trigger: TypedTrigger | null, currentText: string): TypedTrigger | null {
  if (!trigger || trigger.typed !== null) return trigger
  const typed = currentText.startsWith(trigger.draftBefore) ? currentText.slice(trigger.draftBefore.length) : ''
  return typed ? { typed, draftBefore: trigger.draftBefore, baseline: currentText } : null
}

/**
 * Esc：框里是不是还恰好是触发刚放进去的样子？
 * 是 → typed 是要还给终端的那次输入，restoreText 是框应该回到的内容（打字之前的草稿）；
 * 否（编辑过 / 不是敲键唤起的 / 输入法还没定稿）→ 都是 null，调用方就当普通关闭。
 * 比较的是全文：编辑后又改回一模一样，也算没动过 —— 没有任何字会因此丢失。
 */
export function consumePristine(
  trigger: TypedTrigger | null,
  currentText: string,
): { typed: string; restoreText: string } | null {
  if (!trigger || !trigger.typed || trigger.baseline === null || currentText !== trigger.baseline) return null
  return { typed: trigger.typed, restoreText: trigger.draftBefore }
}
