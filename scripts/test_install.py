"""Offline install.sh acceptance tests; run with python3 scripts/test_install.py."""
import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).with_name('install.sh').resolve()
NEW_BINARY = b'#!/bin/sh\necho fixture-version\n'


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='aice-install-test-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.mocks = self.root / 'mocks'
        self.mocks.mkdir()
        self.fixtures = self.root / 'fixtures'
        self.fixtures.mkdir()
        self.scratch = self.root / 'tmp'
        self.scratch.mkdir()
        self.target = self.root / 'install dir' / 'aice'
        self.target.parent.mkdir()
        self.target.write_text('old binary')
        self.env = dict(os.environ, PATH=f'{self.mocks}:/usr/bin:/bin:/usr/sbin:/sbin',
                        INSTALL_DIR=str(self.target.parent), TMPDIR=str(self.scratch),
                        FIXTURES=str(self.fixtures), REQUEST_LOG=str(self.root / 'requests'),
                        AICE_VERSION='1.2.3', TEST_OS='Darwin', TEST_ARCH='arm64')
        self.mock('uname', 'case "$1" in -s) echo "$TEST_OS";; -m) echo "$TEST_ARCH";; esac\n')
        self.mock('curl', '''
printf '%s\\n' "$*" >> "$REQUEST_LOG"
[ "${DOWNLOAD_FAILURE:-}" != 1 ] || exit 22
while [ "$#" -gt 0 ]; do
 case "$1" in
  -o) dest="$2"; shift 2;;
  -w|--connect-timeout|--max-time|--retry|--retry-max-time) shift 2;;
  -*) shift;;
  *) url="$1"; shift;;
 esac
done
case "$url" in
 */releases/latest) printf '%s' 'https://github.com/ch1lam/aice-cli/releases/tag/v9.8.7';;
 */checksums.txt) cp "$FIXTURES/checksums.txt" "$dest";;
 *) cp "$FIXTURES/${url##*/}" "$dest";;
esac
''')
        self.bundle()

    def mock(self, name, body):
        path = self.mocks / name
        path.write_text('#!/bin/sh\nset -eu\n' + body)
        path.chmod(0o755)

    def bundle(self, os_name='darwin', arch='arm64', member='aice'):
        name = f'aice_{os_name}_{arch}.tar.gz'
        archive = self.fixtures / name
        with tarfile.open(archive, 'w:gz') as tar:
            info = tarfile.TarInfo(member)
            info.size = len(NEW_BINARY)
            info.mode = 0o755
            tar.addfile(info, io.BytesIO(NEW_BINARY))
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        (self.fixtures / 'checksums.txt').write_text(f'{digest}  {name}\n')

    def run_install(self, success=True):
        result = subprocess.run(['sh', str(SCRIPT)], cwd=self.root, env=self.env,
                                text=True, capture_output=True, timeout=15)
        self.assertEqual(result.returncode == 0, success, result.stderr)
        self.assertEqual(list(self.scratch.iterdir()), [], 'download temp leaked')
        self.assertEqual(list(self.root.rglob('.aice-install.*')), [], 'staging leaked')
        if not success:
            self.assertEqual(self.target.read_text(), 'old binary')
        return result

    def test_platforms_and_pinned_version(self):
        for os_name, goos in [('Darwin', 'darwin'), ('Linux', 'linux')]:
            for arch, goarch in [('arm64', 'arm64'), ('aarch64', 'arm64'), ('x86_64', 'amd64')]:
                with self.subTest(os=os_name, arch=arch):
                    self.env.update(TEST_OS=os_name, TEST_ARCH=arch)
                    self.bundle(goos, goarch)
                    self.run_install()
                    self.assertEqual(self.target.read_bytes(), NEW_BINARY)
                    self.assertTrue(os.access(self.target, os.X_OK))
        requests = (self.root / 'requests').read_text()
        self.assertNotIn('/releases/latest', requests)
        self.assertIn('/download/v1.2.3/', requests)

    def test_default_directory_fresh_install(self):
        self.env.pop('INSTALL_DIR')
        self.env['HOME'] = str(self.root / 'home')
        self.run_install()
        target = self.root / 'home' / '.local' / 'bin' / 'aice'
        self.assertEqual(target.read_bytes(), NEW_BINARY)
        self.assertEqual(subprocess.check_output([str(target), '--version'], text=True).strip(),
                         'fixture-version')

    def test_latest_is_resolved_once(self):
        self.env['AICE_VERSION'] = ''
        self.run_install()
        requests = (self.root / 'requests').read_text().splitlines()
        self.assertEqual(len(requests), 3)
        self.assertIn('/releases/latest', requests[0])
        for request in requests[1:]:
            self.assertIn('/download/v9.8.7/', request)
            self.assertNotIn('/latest/', request)

    def test_download_failure(self):
        self.env['DOWNLOAD_FAILURE'] = '1'
        self.assertIn('could not download', self.run_install(False).stderr)

    def test_checksum_failure(self):
        (self.fixtures / 'checksums.txt').write_text('0' * 64 + '  aice_darwin_arm64.tar.gz\n')
        self.assertIn('checksum mismatch', self.run_install(False).stderr)

    def test_missing_binary(self):
        self.bundle(member='README')
        self.run_install(False)

    def test_partial_copy_failure_preserves_old_binary(self):
        self.mock('install', 'for target do :; done\nprintf partial > "$target"\nexit 1\n')
        self.assertIn('could not stage', self.run_install(False).stderr)

    def test_rename_failure_preserves_old_binary(self):
        self.mock('mv', 'exit 1\n')
        self.assertIn('could not replace', self.run_install(False).stderr)

    def test_relative_path_and_shell_quoting(self):
        directory = "relative dir'$(false)[x]"
        self.env['INSTALL_DIR'] = directory
        result = self.run_install()
        target = self.root / directory / 'aice'
        self.assertEqual(target.read_bytes(), NEW_BINARY)
        hint = next(line.strip() for line in result.stderr.splitlines() if 'export PATH=' in line)
        check = subprocess.run(['sh', '-c', hint + '\nprintf "%s" "$PATH"'],
                               cwd='/', env={'PATH': '/usr/bin:/bin'}, text=True, capture_output=True)
        self.assertEqual(check.returncode, 0, check.stderr)
        self.assertEqual(check.stdout, f'{target.parent}:/usr/bin:/bin')

    def test_path_entry_does_not_repeat_hint(self):
        self.env['PATH'] += ':' + str(self.target.parent)
        self.assertNotIn('export PATH=', self.run_install().stderr)

    def test_unsupported_platform_does_not_download(self):
        self.env['TEST_ARCH'] = 'riscv64'
        self.run_install(False)
        self.assertFalse((self.root / 'requests').exists())


if __name__ == '__main__':
    unittest.main()
