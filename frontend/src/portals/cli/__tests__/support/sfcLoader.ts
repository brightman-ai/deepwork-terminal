/**
 * 让 bun test 能 import `.vue`：仓库的其它测试都只测纯 .ts，没有 SFC 加载器；这里为 CliTerminalView
 * 的视图接线测试补一个最小的。把 SFC 用 @vue/compiler-sfc 编成 TS（模板内联进 setup），样式忽略。
 * 同时把两个重依赖换成替身（见 stubs.ts）。设备判定（手机/桌面）不换：用真的 useDeviceDetection，
 * 测试里靠伪造 window（innerWidth / ontouchstart）驱动。
 *
 * 只在需要它的测试文件里 import；注册的是进程级 bun 插件，但只拦 `.vue`（及其中两个被替身顶掉的）。
 */
import { plugin } from 'bun'
import { compileScript, parse } from '@vue/compiler-sfc'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

let installed = false
export function installVueSfcLoader(): void {
  if (installed) return
  installed = true
  plugin({
    name: 'dw-test-vue-sfc',
    setup(build) {
      // 注意：Bun 只对 `.vue` 结尾的说明符调用 onResolve，所以只能换 .vue；替身模块 re-export 同一个
      // stubs.ts 里的导出，保证测试与被测组件拿到同一份模块实例。
      build.onResolve({ filter: /terminal-session\/CliTerminalSurface\.vue$/ }, () => ({
        path: join(import.meta.dir, 'surfaceStub.ts'),
      }))
      build.onResolve({ filter: /terminal-session\/HoverTip\.vue$/ }, () => ({
        path: join(import.meta.dir, 'hoverTipStub.ts'),
      }))
      build.onLoad({ filter: /\.vue$/ }, ({ path }) => {
        const { descriptor, errors } = parse(readFileSync(path, 'utf8'), { filename: path })
        if (errors.length) throw errors[0]
        const script = compileScript(descriptor, {
          id: path,
          inlineTemplate: true,
          // 自定义渲染器没有 insertStaticContent，静态提升成 innerHTML 字符串会走不通。
          templateOptions: { compilerOptions: { hoistStatic: false } },
        })
        return { contents: script.content, loader: 'ts' }
      })
    },
  })
}
