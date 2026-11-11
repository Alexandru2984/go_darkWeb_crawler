import json
from pathlib import Path
import tempfile
import unittest

import totp_keys


class TOTPKeyTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(prefix="onion-totp-key-test-")
        self.addCleanup(temporary.cleanup)
        self.path = Path(temporary.name) / "keyring.json"

    def test_initialize_and_rotate_preserve_old_keys_and_private_modes(self):
        self.assertEqual(totp_keys.update(self.path, "init"), 1)
        old = totp_keys.read_keyring(self.path)
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)
        self.assertEqual(totp_keys.update(self.path, "rotate"), 2)
        new = totp_keys.read_keyring(self.path)
        self.assertEqual(new["keys"][old["active"]], old["keys"][old["active"]])
        self.assertNotEqual(new["active"], old["active"])
        self.assertEqual(self.path.stat().st_mode & 0o777, 0o600)

    def test_reinitialization_does_not_replace_a_key(self):
        totp_keys.update(self.path, "init")
        original = self.path.read_bytes()
        with self.assertRaises(ValueError):
            totp_keys.update(self.path, "init")
        self.assertEqual(self.path.read_bytes(), original)

    def test_corrupt_or_public_keyring_cannot_rotate(self):
        for value in ({}, {"format_version": 2}, []):
            self.path.write_text(json.dumps(value))
            self.path.chmod(0o600)
            with self.assertRaises((ValueError, KeyError)):
                totp_keys.update(self.path, "rotate")
        self.path.unlink()
        totp_keys.update(self.path, "init")
        self.path.chmod(0o644)
        with self.assertRaises(ValueError):
            totp_keys.update(self.path, "rotate")


if __name__ == "__main__":
    unittest.main()
