import { defineComponent, h } from 'vue'
// Only the renderer is replaced; FilesPanel, file transport, tab controls and
// exposed terminal entry point are real in the isolated lifecycle test.
export default defineComponent({
  props: ['name','text','lineStart','lineEnd','jumpVersion','initialScrollTop','contentVersion'],
  emits: ['scroll-position'],
  setup: (props, {emit}) => () => h('div', {
    'data-testid':'fixture-reader', 'data-line':props.lineStart, 'data-scroll':props.initialScrollTop,
    onScroll:()=>emit('scroll-position',140),
  }, props.text || ''),
})
