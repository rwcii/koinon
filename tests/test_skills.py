"""The session skills point to the installed guide instead of repeating its recipes.

A recipe copied into a skill stays behind when the runtime changes; the guide ships
with the runtime. `handoff`, `pickup` and `peer-tmux` may therefore name only
`session.py guide`, and no other `session.py` or `bridge.py` command.
"""
import re
import unittest

from repo_root import ROOT

SKILLS = ('handoff', 'pickup', 'peer-tmux')
COMMAND = re.compile(r'\b(session|bridge)\.py\b(\s+\S+)?')


def recipes(text):
    """Every session.py or bridge.py mention that is not `session.py guide`."""
    return [match.group(0) for match in COMMAND.finditer(text)
            if not (match.group(1) == 'session' and (match.group(2) or '').strip() == 'guide')]


class SkillRecipeTests(unittest.TestCase):
    def test_session_skills_name_only_the_guide(self):
        for name in SKILLS:
            with self.subTest(skill=name):
                text = (ROOT/'agents'/'skills'/name/'SKILL.md').read_text()
                self.assertEqual(recipes(text), [])
                self.assertIn('session.py guide', text)

    def test_the_rule_finds_a_copied_recipe(self):
        for text in ('run `session.py ensure`', 'the installed `bridge.py peers`',
                     'session.py stop --thread X', 'bridge.py'):
            with self.subTest(text=text):
                self.assertNotEqual(recipes(text), [])
        self.assertEqual(recipes('run `session.py guide --topic reconnect`'), [])


if __name__ == '__main__':
    unittest.main()
