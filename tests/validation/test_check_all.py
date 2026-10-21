"""Exercise the real validation shell/Makefile with simulated Docker/tool exits.

These tests check orchestration, not Go/PostgreSQL correctness. check-all itself
runs the real validators and databases; this fixture adds deterministic failures.
Run: python3 -m unittest discover -s tests/validation -v
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
TOOLS = r'''
import json, os, pathlib, subprocess, sys, shutil
state_path = pathlib.Path(os.environ['FAKE_DOCKER_STATE'])
state = json.loads(state_path.read_text())
args = sys.argv[1:]
name = pathlib.Path(sys.argv[0]).name

def save():
    state_path.write_text(json.dumps(state))

def check(group):
    state['groups'].append(group)
    save()
    sys.exit(23 if os.environ.get('FAIL_GROUP') == group else 0)

if name == 'apt-get': sys.exit(0)
if name == 'go':
    if args[0] == 'fmt': check('format')
    if args[0] == 'vet': check('vet')
    if args[0] == 'build': check('build')
    if args[0] == 'test':
        check('integration' if '-tags=integration' in args else 'race' if '-race' in args else 'unit')
    sys.exit('unknown Go invocation')
if name == 'python':
    if args[:2] == ['-m', 'pip']: sys.exit(0)
    if args[:2] == ['-m', 'unittest']: check('orchestration')
    check('contracts')

command = args[0]
if command == 'network':
    if args[1] == 'create':
        state['network'] = args[2]; save(); print(args[2])
    elif args[1] == 'rm':
        state['network'] = None; save()
    else: sys.exit('unexpected network operation')
elif command == 'create':
    ident = 'container-' + str(state['next'])
    state['next'] += 1
    env = {}
    for index, arg in enumerate(args):
        if arg == '-e':
            key, value = args[index + 1].split('=', 1); env[key] = value
    shell = args[args.index('sh') + 2] if 'sh' in args else None
    state['containers'][ident] = {'shell': shell, 'env': env, 'exit': 0}
    save(); print(ident)
elif command == 'start':
    ident = args[-1]; record = state['containers'][ident]
    if '-ai' in args:
        workspace = state_path.parent / ident
        shell = record['shell'].replace('/workspace', str(workspace))
        result = subprocess.run(['sh', '-ec', shell], env=dict(os.environ, **record['env']))
        # Reload state because simulated tools recorded check groups.
        state = json.loads(state_path.read_text())
        state['containers'][ident]['exit'] = result.returncode
        save()
        # Intentionally return 0: shell must inspect the container exit code.
    else: print(ident)
elif command == 'inspect':
    print(state['containers'][args[-1]]['exit'])
elif command == 'exec':
    if 'psql' in args: print(0)
    elif 'pg_isready' not in args: sys.exit('unexpected container exec')
elif command == 'rm':
    for ident in args[1:]:
        if ident == '-f': continue
        del state['containers'][ident]
        shutil.rmtree(state_path.parent / ident, ignore_errors=True)
    save()
    if os.environ.get('FAIL_CLEANUP'): sys.exit(1)
else: sys.exit('unexpected docker operation: ' + command)
'''


class CheckAllTests(unittest.TestCase):
    def run_validation(self, failure=None, mode='--all', cleanup_failure=False):
        with tempfile.TemporaryDirectory(prefix='hpts-orchestration-') as directory:
            root = Path(directory)
            tools = root / 'tools'
            tools.mkdir()
            for name in ('docker', 'go', 'python', 'apt-get'):
                path = tools / name
                path.write_text('#!' + sys.executable + '\n' + TOOLS)
                path.chmod(0o700)
            state_path = root / 'state.json'
            state_path.write_text(json.dumps({'network': None, 'containers': {}, 'groups': [], 'next': 1}))
            env = dict(os.environ, PATH=str(tools) + os.pathsep + os.environ['PATH'],
                       TMPDIR=str(root), FAKE_DOCKER_STATE=str(state_path))
            env.pop('FAIL_GROUP', None)
            env.pop('FAIL_CLEANUP', None)
            if cleanup_failure:
                env['FAIL_CLEANUP'] = '1'
            if failure:
                env['FAIL_GROUP'] = failure
            result = subprocess.run(['sh', str(ROOT / 'scripts/test-postgres.sh'), mode],
                                    cwd=ROOT, env=env, capture_output=True, text=True, timeout=30)
            state = json.loads(state_path.read_text())
            self.assertFalse(state['containers'], result.stderr)
            self.assertIsNone(state['network'], result.stderr)
            self.assertFalse(list(root.glob('hpts-validation.*')))
            return result, state['groups']

    def test_success_runs_all_groups(self):
        result, groups = self.run_validation()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(set(groups), {'contracts', 'orchestration', 'format', 'vet', 'unit', 'build', 'race', 'integration'})

    def test_each_failure_propagates_and_cleans_up(self):
        for group in ('contracts', 'orchestration', 'format', 'vet', 'unit', 'build', 'race', 'integration'):
            with self.subTest(group=group):
                result, groups = self.run_validation(group)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                self.assertIn(group, groups)
                self.assertEqual(groups[-1], group)

    def test_cleanup_error_changes_success_to_failure(self):
        result, _ = self.run_validation(cleanup_failure=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Failed to remove validation container', result.stderr)

    def test_postgres_command_keeps_existing_scope(self):
        result, groups = self.run_validation(mode='postgres')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertNotIn('contracts', groups)
        self.assertNotIn('race', groups)
        self.assertIn('integration', groups)


if __name__ == '__main__':
    unittest.main()
