import base64
import hashlib
import json
import unittest

from scripts.cross_file_go_receipts import SCHEMA, GoReceiptError, _go_json, validate_go_receipt_stream
from scripts.cross_file_signed_receipts import Ed25519Signer, Ed25519Verifier


class GoReceiptTests(unittest.TestCase):
    def setUp(self):
        self.signer = Ed25519Signer.generate("go-receipt")
        self.verifier = Ed25519Verifier(self.signer.public_key)

    def _line(self, sequence=1, previous=""):
        public = base64.b64decode(self.signer.public_key.public_key)
        unsigned = {
            "schema": SCHEMA,
            "sequence": sequence,
            **({"previous_digest": previous} if previous else {}),
            "payload": {"tool": "diagnostics", "status": "success", "arguments": {"file_path": "a.py"}, "result": {"rows": []}},
            "signer_public_key_sha256": "sha256:" + hashlib.sha256(public).hexdigest(),
        }
        material = _go_json(unsigned)
        return json.dumps({
            **unsigned,
            "event_digest": "sha256:" + hashlib.sha256(material).hexdigest(),
            "signature": base64.b64encode(self.signer.sign(material)).decode(),
        }, separators=(",", ":"))

    def test_valid_signed_stream_and_tamper(self):
        line = self._line()
        self.assertEqual(len(validate_go_receipt_stream(line, verifier=self.verifier)), 1)
        tampered = line.replace('"a.py"', '"b.py"')
        with self.assertRaises(GoReceiptError):
            validate_go_receipt_stream(tampered, verifier=self.verifier)


if __name__ == "__main__":
    unittest.main()
