import importlib.util
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest
from repo_root import ROOT

spec = importlib.util.spec_from_file_location('installer', ROOT/'scripts/install.py')
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

WORKFLOWS = ROOT/'.github/workflows'


def exempt_prefixes():
    """The agent-process prefixes that the required test jobs treat as exempt."""
    text = (WORKFLOWS/'tests.yml').read_text()
    match = re.search(r"grep -vE '\^\(([^)]*)\)'", text)
    return sorted(part.replace('\\.', '.') for part in match.group(1).split('|'))


def scope_script():
    """The shell that the required test jobs run to classify a pull request."""
    text = (WORKFLOWS/'tests.yml').read_text()
    match = re.search(r"- id: scope\n.*?run: \|\n(.*?)\n      - ", text, re.S)
    return textwrap.dedent(match.group(1))


def git(repo, *args):
    subprocess.run(['git', '-c', 'user.name=t', '-c', 'user.email=t@example.com',
                    '-c', 'commit.gpgsign=false', *args],
                   cwd=repo, check=True, capture_output=True)


class CiScopeTests(unittest.TestCase):
    def test_native_workflows_ignore_the_same_paths(self):
        expected = exempt_prefixes()
        native = sorted(WORKFLOWS.glob('native-*.yml'))
        self.assertTrue(native)
        for workflow in native:
            text = workflow.read_text()
            blocks = re.findall(r"paths-ignore:\n((?:\s+- '[^']+'\n)+)", text)
            self.assertEqual(len(blocks), 2, workflow.name)
            for block in blocks:
                paths = sorted(p + '/' for p in re.findall(r"- '([^']+)/\*\*'", block))
                self.assertEqual(paths, expected, workflow.name)

    def test_exempt_paths_are_never_installed(self):
        prefixes = tuple(exempt_prefixes())
        self.assertIn('agents/', prefixes)
        installed = [path for path in installer.FILES if path.startswith(prefixes)]
        self.assertEqual(installed, [])

    def test_an_exempt_change_still_runs_every_test_that_reads_agent_files(self):
        text = (WORKFLOWS/'tests.yml').read_text()
        self.assertIn("- if: steps.scope.outputs.exempt == 'true'\n"
                      "        run: python tests/run.py -v -p test_skills.py", text)
        readers = sorted(path.name for path in (ROOT/'tests').glob('test_*.py')
                         if path.name != 'test_ci_scope.py'
                         and re.search(r"ROOT\s*/\s*'(agents|docs/sprints|\.claude|\.codex|\.agents)\b",
                                       path.read_text()))
        self.assertEqual(readers, ['test_skills.py'])

    def classify(self, change):
        """Run the workflow's scope step on a synthetic pull request."""
        with tempfile.TemporaryDirectory() as tmp:
            repo = Path(tmp)
            git(repo, 'init', '-q')
            (repo/'runtime.py').write_text('code\n')
            (repo/'README.md').write_text('readme\n')
            git(repo, 'add', '-A')
            git(repo, 'commit', '-qm', 'base')
            base = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=repo, check=True,
                                  capture_output=True, text=True).stdout.strip()
            change(repo)
            git(repo, 'add', '-A')
            git(repo, 'commit', '-qm', 'change')
            output = repo/'.github-output'
            env = dict(os.environ, BASE=base, HEAD='HEAD', GITHUB_OUTPUT=str(output))
            subprocess.run(['bash', '-c', scope_script()], cwd=repo, env=env, check=True,
                           capture_output=True)
            return output.exists() and 'exempt=true' in output.read_text()

    def test_scope_step_exempts_only_agent_process_changes(self):
        def skill(repo):
            (repo/'agents/skills/x').mkdir(parents=True)
            (repo/'agents/skills/x/SKILL.md').write_text('skill\n')

        def skill_and_readme(repo):
            skill(repo)
            (repo/'README.md').write_text('changed\n')

        def move_runtime_into_agents(repo):
            (repo/'agents').mkdir()
            git(repo, 'mv', 'runtime.py', 'agents/runtime.py')

        self.assertTrue(self.classify(skill))
        self.assertFalse(self.classify(skill_and_readme))
        # Rename detection would report only the new path, hiding the removed runtime file.
        self.assertFalse(self.classify(move_runtime_into_agents))


if __name__ == '__main__':
    unittest.main()
