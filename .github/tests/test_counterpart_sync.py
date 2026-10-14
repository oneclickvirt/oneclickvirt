import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('counterpart', Path(__file__).resolve().parents[1]/'scripts/sync_private_counterpart.py')
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class CounterpartTests(unittest.TestCase):
    def test_private_edit_outside_public_patch_is_preserved(self):
        base = b'first\nsecond\nthird\nfourth\nfifth\n'
        private = base.replace(b'first',b'private')
        public = base.replace(b'fifth',b'updated')
        self.assertEqual(module.merge_content(private,base,public),b'private\nsecond\nthird\nfourth\nupdated\n')

    def test_conflict_stops_without_exporting_private_content(self):
        with self.assertRaisesRegex(RuntimeError, '^private edit conflicts with public patch$'):
            module.merge_content(b'private secret\n',b'base\n',b'public\n')

    def test_binary_conflict_cannot_overwrite_private_asset(self):
        with self.assertRaisesRegex(RuntimeError,'^binary conflict$'):
            module.merge_content(b'private\0',b'base\0',b'public\0')

    def test_public_patch_can_be_replayed(self):
        self.assertEqual(module.merge_content(b'new',b'old',b'new'),b'new')
