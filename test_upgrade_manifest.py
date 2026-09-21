from pathlib import Path
import os
import tempfile
import unittest
from unittest.mock import patch

import upgrade_manifest as manifest


class ManifestTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / 'docs').mkdir(mode=0o700)
        (self.root / 'runtime.py').write_text('original runtime\n')
        (self.root / 'docs/guide.md').write_text('original guide\n')
        self.names = ('runtime.py', 'docs/guide.md')
        for name in self.names:
            (self.root / name).chmod(0o600)

    def test_repeat_is_exact_and_content_change_refuses(self):
        value = manifest.capture(self.root, self.names)
        self.assertEqual(manifest.verify(value), value)
        (self.root / 'runtime.py').write_text('modified runtime\n')
        with self.assertRaises(manifest.ManifestError):
            manifest.verify(value)
        self.assertEqual((self.root / 'runtime.py').read_text(), 'modified runtime\n')

    def test_missing_file_never_becomes_empty_manifest_entry(self):
        value = manifest.capture(self.root, self.names)
        (self.root / 'docs/guide.md').unlink()
        with self.assertRaises(FileNotFoundError):
            manifest.verify(value)

    def test_names_cannot_escape_alias_or_duplicate_selection(self):
        for names in ((), ('../outside',), ('/absolute',), ('docs//guide.md',),
                      ('./runtime.py',), ('runtime.py', 'runtime.py'), ('.',)):
            with self.subTest(names=names), self.assertRaises(manifest.ManifestError):
                manifest.capture(self.root, names)

    def test_source_symlink_directory_and_hardlink_are_refused(self):
        (self.root / 'alias').symlink_to(self.root / 'docs', target_is_directory=True)
        with self.assertRaises(manifest.ManifestError):
            manifest.capture(self.root, ('alias/guide.md',))
        os.link(self.root / 'runtime.py', self.root / 'hard.py')
        with self.assertRaises(manifest.ManifestError):
            manifest.capture(self.root, ('hard.py',))
        (self.root / 'link.py').symlink_to(self.root / 'runtime.py')
        with self.assertRaises(OSError):
            manifest.capture(self.root, ('link.py',))

    def test_writable_ancestor_is_refused_without_permission_changes(self):
        (self.root / 'docs').chmod(0o775)
        with self.assertRaises(manifest.ManifestError):
            manifest.capture(self.root, self.names)
        self.assertEqual((self.root / 'docs').stat().st_mode & 0o777, 0o775)

    def test_bounded_files_and_total_bytes(self):
        for limits in (dict(MAX_FILES=1), dict(MAX_FILE_BYTES=1), dict(MAX_TOTAL_BYTES=1)):
            with patch.multiple(manifest, **limits), self.assertRaises(manifest.ManifestError):
                manifest.capture(self.root, self.names)

    def test_tampered_manifest_is_not_silently_refingerprinted(self):
        value = manifest.capture(self.root, self.names)
        value['files']['runtime.py']['sha256'] = 'a' * 64
        with self.assertRaises(manifest.ManifestError):
            manifest.verify(value)

    def test_replacement_during_read_refuses(self):
        original_read = os.fdopen
        path = self.root / 'runtime.py'
        replacement = self.root / 'replacement.py'
        replacement.write_text('replacement')
        replacement.chmod(0o600)

        def replace_then_open(*args, **kwargs):
            os.replace(replacement, path)
            return original_read(*args, **kwargs)

        with patch.object(manifest.os, 'fdopen', side_effect=replace_then_open):
            with self.assertRaises(manifest.ManifestError):
                manifest.capture(self.root, ('runtime.py',))


    def test_malformed_manifest_fields_refuse_before_reading_files(self):
        import copy
        value = manifest.capture(self.root, self.names)
        variants = []
        for key, replacement in (('version', True), ('root', None), ('bytes', -1),
                                 ('sha256', 'not-a-digest'), ('files', {})):
            variant = copy.deepcopy(value)
            variant[key] = replacement
            variants.append(variant)
        variant = copy.deepcopy(value)
        variant['files']['runtime.py']['bytes'] = True
        variants.append(variant)
        for variant in variants:
            with patch.object(manifest, 'read_selected', side_effect=AssertionError('read attempted')):
                with self.assertRaises(manifest.ManifestError):
                    manifest.verify(variant)

    def test_in_place_change_during_read_refuses(self):
        original_read = os.fdopen
        path = self.root / 'runtime.py'

        def change_then_open(*args, **kwargs):
            path.write_text('different bytes of a different length')
            return original_read(*args, **kwargs)

        with patch.object(manifest.os, 'fdopen', side_effect=change_then_open):
            with self.assertRaises(manifest.ManifestError):
                manifest.capture(self.root, ('runtime.py',))

    def test_special_literal_names_are_not_interpreted(self):
        name = 'docs/quote" dollar$ space.md'
        path = self.root / name
        path.write_text('literal name')
        path.chmod(0o600)
        value = manifest.capture(self.root, (name,))
        self.assertEqual(list(value['files']), [name])
        self.assertEqual(manifest.verify(value), value)


if __name__ == '__main__':
    unittest.main()
