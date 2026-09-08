/**
 * wireImageZoom — 把一棵渲染好的 DOM 里的 <img> 接到共用的全屏查看器上。
 *
 * 抽出来是因为「涉及图片的要支持放大」横跨 docx / xlsx / xmind 三种预览：写三遍就会漂成三种
 * 手感（有的能点、有的不能，有的有提示、有的没有）。这里只做两件事——标记已接线（渲染器可能
 * 重复调用）、给出"可点"的信号（title + 光标）。
 */
export function wireImageZoom(root: HTMLElement, open: (src: string) => void): void {
  root.querySelectorAll<HTMLImageElement>('img').forEach((img) => {
    if (img.dataset.zoomWired === '1') return
    img.dataset.zoomWired = '1'
    img.addEventListener('click', () => open(img.src))
    img.title = '点击放大'
    img.style.cursor = 'zoom-in'
  })
}
