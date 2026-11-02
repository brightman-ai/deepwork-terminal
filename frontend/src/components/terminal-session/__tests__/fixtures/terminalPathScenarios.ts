/** Expected identities are literal fixture data, independent of the parser.
 * The same physical records run against pure cells and real xterm/reflow. */
export interface TerminalPathScenario {
  id: string
  family: 'lexical' | 'title' | 'receipt' | 'prose' | 'label' | 'diff' | 'boundary'
  rows: string[]
  paths: string[]
}

export const terminalPathScenarios: TerminalPathScenario[] = [
  {id: 'single-line-title', family: 'title', rows: ['● Write(/work/docs/report.md)'], paths: ['/work/docs/report.md']},
  {id: 'write-title-aligned-seven', family: 'title', rows: ['● Write(/work/skills/sampletool', '       -t3/design/design.md)'], paths: ['/work/skills/sampletool-t3/design/design.md']},
  {id: 'update-title-aligned-eight', family: 'title', rows: ['● Update(/work/skills/sampletoo', '        l-tg2/design/文', '        文文.md)'], paths: ['/work/skills/sampletool-tg2/design/文文文.md']},
  {id: 'read-title-aligned-six', family: 'title', rows: ['● Read(/work/docs/long-', '      report.md · lines 12-20)'], paths: ['/work/docs/long-report.md']},
  {id: 'title-legacy-two', family: 'title', rows: ['⏺ Update(docs/文', '  文/报告.md)'], paths: ['docs/文文/报告.md']},
  {id: 'title-literal-spaces', family: 'title', rows: ['● Write("docs/my', '        report.md")'], paths: ['docs/my report.md']},
  {id: 'receipt-five-nbsp', family: 'receipt', rows: ['  ⎿ \u00a0Wrote 89 lines to /work/sample-asse', '     ts/skills/sampletool-t3/design/design.md'], paths: ['/work/sample-assets/skills/sampletool-t3/design/design.md']},
  {id: 'receipt-single-line-spaces', family: 'receipt', rows: ['  ⎿  Wrote 1 line to /work/my  docs/report.md'], paths: ['/work/my  docs/report.md']},
  {id: 'receipt-path-next-row', family: 'receipt', rows: ['  ⎿  Wrote 2 lines to ', '     /work/docs/report.md'], paths: ['/work/docs/report.md']},
  {id: 'reference-label-and-three-path-rows', family: 'receipt', rows: ['  ⎿ \u00a0Referenced file', '     ../../../../tmp/agent-1001/-home-user-code-demo-', '     project/00000000-1111-2222-3333-444444444444/scr', '     atchpad/experiment/v2/group-one/report.md'], paths: ['../../../../tmp/agent-1001/-home-user-code-demo-project/00000000-1111-2222-3333-444444444444/scratchpad/experiment/v2/group-one/report.md']},
  {id: 'reference-label-unequal-path-rows', family: 'receipt', rows: ['  ⎿ \u00a0Referenced file', '     ../../../../tmp/agent-1001/-home-user-code-demo-project/00000000-1111-2222', '     -3333-444444444444/scr', '     atchpad/experiment/v2/group-one/report.md'], paths: ['../../../../tmp/agent-1001/-home-user-code-demo-project/00000000-1111-2222-3333-444444444444/scratchpad/experiment/v2/group-one/report.md']},
  {id: 'receipt-metadata-wrap', family: 'receipt', rows: ['  ⎿  Wrote 2 lin', '     es to /work/docs/report.md'], paths: ['/work/docs/report.md']},
  {id: 'shell-output-counted-path', family: 'receipt', rows: ['  ⎿ \u00a015398 /work/other-project/skills/examp', '     le-tool/design/普通命令输出-中文路径', '     报告.md', '     5 /work/demo/results/first.json'], paths: ['/work/other-project/skills/example-tool/design/普通命令输出-中文路径报告.md', '/work/demo/results/first.json']},
  {id: 'shell-git-explicit-directory', family: 'receipt', rows: ['● Bash(cd /work/current-project && git', '      -C /work/other-project status --short', '      docs | head)', '  ⎿ \u00a0ok', '     ?? "docs/\\344\\270\\255\\346\\226\\207 ', '     \\346\\212\\245\\345\\221\\212.md"'], paths: ['/work/current-project', '/work/other-project', '/work/other-project/docs/中文 报告.md']},
  {id: 'receipt-quoted-spaces', family: 'receipt', rows: ['  └  Wrote 2 lines to "docs/my ', '      report.md"'], paths: ['docs/my  report.md']},
  {id: 'receipt-foreign-target', family: 'boundary', rows: ['  ⎿  Wrote 2 lines to https://example.com/long-', '     report.md'], paths: []},
  {id: 'receipt-variable-target', family: 'boundary', rows: ['  ⎿  Wrote 2 lines to ${ROOT}/long-', '     report.md'], paths: []},
  {id: 'label-colon', family: 'label', rows: ['文件：/work/sampl', '  etool/report.', '  md（393行）'], paths: ['/work/sampletool/report.md']},
  {id: 'paragraph-path-next-row', family: 'prose', rows: ['● 结文文结在', '  ~/docs/sampl', '  etool/报告.md。'], paths: ['~/docs/sampletool/报告.md']},
  {id: 'paragraph-inline-bare', family: 'prose', rows: ['● 已保存 ./docs/long-', '  report.md。'], paths: ['./docs/long-report.md']},
  {id: 'paragraph-explicit-reference', family: 'prose', rows: ['  文文记录 (topics/文文', '  文文/报告.md)已保存。'], paths: ['topics/文文文文/报告.md']},
  {id: 'paragraph-real-spaces', family: 'prose', rows: ['● "docs/my ', '   report.md"'], paths: ['docs/my  report.md']},
  {id: 'paragraph-directory', family: 'prose', rows: ['● 文文(docs/rev', '  iew/)'], paths: ['docs/review/']},
  {id: 'diff-blank-gutter', family: 'diff', rows: ['    45 +[文文](docs/long-repo', '        rt.md)'], paths: ['docs/long-report.md']},
  {id: 'diff-repeated-marker', family: 'diff', rows: ['    45 +[文文](docs/long-repo', '       +rt.md)'], paths: ['docs/long-report.md']},
  {id: 'source-location', family: 'lexical', rows: ['文件：docs/报告.md:121（源码）'], paths: ['docs/报告.md']},
  {id: 'markdown-basename', family: 'lexical', rows: ['3 +[原文](case-文文文文.md)'], paths: ['case-文文文文.md']},
  {id: 'markdown-space-target', family: 'lexical', rows: ['[报告](docs/my report.md)'], paths: ['docs/my report.md']},
  {id: 'filename-parentheses', family: 'lexical', rows: ['docs/report(final).md（.docx 重出）'], paths: ['docs/report(final).md']},
  {id: 'foreign-prose', family: 'boundary', rows: ['● 记录 "https://example.com/', '  report.md"'], paths: []},
  {id: 'independent-records', family: 'boundary', rows: ['  /tmp/report', '  other.md'], paths: ['/tmp/report', 'other.md']},
  {id: 'finished-file-and-next-file', family: 'boundary', rows: ['● 已保存 ~/docs/done.md', '  other.md'], paths: ['~/docs/done.md', 'other.md']},
]
