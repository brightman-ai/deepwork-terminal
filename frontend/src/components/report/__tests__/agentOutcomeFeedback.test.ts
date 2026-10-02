import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'

const SOURCE = readFileSync(new URL('../AgentReportDetail.vue', import.meta.url), 'utf8')

function sectionOf(tag: 'template' | 'script'): string {
  const open = SOURCE.indexOf(`<${tag}`)
  const close = SOURCE.lastIndexOf(`</${tag}>`)
  expect(open).toBeGreaterThan(-1)
  expect(close).toBeGreaterThan(open)
  return SOURCE.slice(open, close)
}

describe('Agent 结果反馈 UI 契约', () => {
  test('只允许对已完成但未验证的任务显示反馈入口', () => {
    const template = sectionOf('template')
    const feedback = template.slice(template.indexOf('class="ard-outcome-feedback"') - 180)

    expect(template).toContain('v-if="task.status === \'completed\' && task.outcome === \'completed_unverified\'"')
    expect(feedback).toContain("saveHumanOutcome(task, 'human_accepted')")
    expect(feedback).toContain("saveHumanOutcome(task, 'human_rework')")
    expect(feedback.match(/:disabled="loading \|\| !!savingOutcomeFor"/g)?.length).toBe(2)
  })

  test('成功后显示服务端证据并刷新已加载分页，失败时给出可恢复提示', () => {
    const script = sectionOf('script')
    const start = script.indexOf('async function saveHumanOutcome(')
    const end = script.indexOf('\nfunction close()', start)
    const handler = script.slice(start, end)

    expect(handler).toContain('await recordOutcome(task.id, outcome)')
    expect(handler).toContain('visibleTask.outcome = result.evidence.status')
    expect(handler).toContain('detail.value.outcome_evidence[task.id] = [...')
    expect(handler).toContain('await reloadLoadedPages(requestFilter())')
    expect(handler).toContain('已记录；汇总刷新失败，重新打开详情可读取最新结果')
    expect(handler).toContain('outcomeError.value = result.error')
    expect(handler).toContain("savingOutcomeFor.value = ''")
  })

  test('任务反馈写入具备幂等调用所需的固定任务 ID 与 outcome 请求体', () => {
    const hook = readFileSync(new URL('../useAgentDetail.ts', import.meta.url), 'utf8')
    expect(hook).toContain("cliApi('/usage/agent-report/outcome')")
    expect(hook).toContain('work_item_id: workItemId, outcome')
    expect(hook).toContain("response.status === 404")
    expect(hook).toContain("response.status === 409")
  })
})
