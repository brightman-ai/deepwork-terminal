import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('contexts', Path(__file__).resolve().parents[1] / 'audit-terminal-path-contexts.py')
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class TranscriptEvidenceTests(unittest.TestCase):
    def test_claude_declared_targets_keep_operation_but_skip_thinking(self):
        row = {'type': 'assistant', 'message': {'content': [
            {'type': 'thinking', 'thinking': 'private reasoning /private/not-for-export.md'},
            {'type': 'tool_use', 'name': 'Write', 'input': {'file_path': 'docs/中文（终稿）.md', 'content': 'payload'}},
            {'type': 'text', 'text': '已保存文件。'},
        ]}}
        blocks = list(audit.visible_blocks('claude', row))
        self.assertEqual(blocks[0]['path'], 'docs/中文（终稿）.md')
        self.assertEqual(blocks[0]['operation'], 'create')
        self.assertNotIn('private reasoning', str(blocks))
        self.assertNotIn('payload', str(blocks))

    def test_codex_patch_headers_are_authoritative_and_distinct(self):
        patch = '*** Begin Patch\n*** Add File: docs/new.md\n+new\n*** Update File: docs/旧稿.md\n@@\n-old\n+new\n*** Move to: docs/现行.md\n*** End Patch'
        row = {'type': 'response_item', 'payload': {'type': 'custom_tool_call', 'name': 'apply_patch', 'input': patch}}
        blocks = list(audit.visible_blocks('codex', row))
        refs = list(audit.patch_targets(blocks[0]['text']))
        self.assertEqual([(r[0], r[1]) for r in refs], [('docs/new.md', 'create'), ('docs/旧稿.md', 'edit'), ('docs/现行.md', 'move')])
        self.assertTrue(all(len(r[2]) <= 5 for r in refs))

    def test_codex_events_reasoning_and_user_prose_are_not_duplicate_samples(self):
        for row in [
            {'type': 'event_msg', 'payload': {'type': 'agent_message', 'message': 'docs/a.md'}},
            {'type': 'response_item', 'payload': {'type': 'reasoning', 'summary': 'docs/private.md'}},
            {'type': 'response_item', 'payload': {'type': 'message', 'role': 'developer', 'content': [{'type': 'text', 'text': 'docs/system.md'}]}},
            {'type': 'response_item', 'payload': {'type': 'message', 'role': 'user', 'content': [{'type': 'input_text', 'text': 'docs/example.md'}]}},
        ]:
            self.assertEqual(list(audit.visible_blocks('codex', row)), [])

    def test_context_has_two_neighbours_and_redacts_credentials_without_changing_task_paths(self):
        lines = ['before one', 'before two', 'file: docs/task-20261025-report.md', 'auth-code secret-value', 'after two', 'outside']
        ctx = audit.context(lines, 2, 'docs/task-20261025-report.md')
        self.assertEqual([r['line'] for r in ctx], [1, 2, 3, 4, 5])
        self.assertEqual(ctx[2]['text'], lines[2])
        self.assertNotIn('secret-value', str(ctx))
        self.assertIn('<REDACTED>', str(ctx))

    def test_balanced_sample_is_deterministic_and_not_conditioned_on_path_hits(self):
        files = [{'agent': agent, 'day': f'day-{n%3}', 'project': f'project-{n%2}', 'path': f'{agent}-{n}'}
                 for agent in ('claude', 'codex') for n in range(20)]
        one = audit.select(files, 10, 'frozen')
        self.assertEqual(one, audit.select(files, 10, 'frozen'))
        self.assertEqual(sum(r['agent'] == 'claude' for r in one), 10)
        self.assertEqual(sum(r['agent'] == 'codex' for r in one), 10)

    def test_signed_image_query_values_are_redacted(self):
        value = audit.scrub('https://host/report.png?UCloudPublicKey=secret-key&Signature=secret-signature&Expires=123')
        self.assertNotIn('secret-key', value)
        self.assertNotIn('secret-signature', value)
        self.assertIn('report.png?', value)

    def test_coarse_pass_keeps_notes_stats_and_markdown_without_declaring_them_targets(self):
        for text in ['文件：docs/报告.md（.docx 重出）', 'Edited docs/a.md (+4,-2)', '[文档](report.md)']:
            self.assertTrue(list(audit.coarse_candidates(text)))


if __name__ == '__main__':
    unittest.main()
