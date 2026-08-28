import copy
import json
import pathlib
import tempfile
import unittest

from scripts.cross_file_identity_envelope import (
    AuthorityBoundaryError,
    BENCHMARK_EVIDENCE_AUTHORITY,
    IDENTITY_COMPONENTS,
    IdentityDriftError,
    IdentityEnvelopeError,
    MissingFieldError,
    SCHEMA,
    UnknownFieldError,
    build_identity_envelope,
    canonical_json,
    load_identity_envelope,
    parse_identity_envelope,
    sha256_text,
    verify_identity_envelope,
)


def _digest(name: str) -> str:
    return sha256_text(name)


def _bindings() -> dict:
    result = {}
    for name in IDENTITY_COMPONENTS:
        if name == "base":
            result[name] = {
                "commit_sha": "a" * 40,
                "tree_sha": "b" * 40,
                "sha256": _digest("base"),
            }
        elif name == "final_tree":
            result[name] = {"tree_sha": "c" * 40, "sha256": _digest("final-tree")}
        else:
            result[name] = {"id": f"{name}-v1", "sha256": _digest(name)}
    return result


class IdentityEnvelopeTests(unittest.TestCase):
    def test_build_parse_and_json_round_trip(self):
        payload = build_identity_envelope(_bindings())
        self.assertEqual(payload["schema"], SCHEMA)
        self.assertEqual(payload["authority"], BENCHMARK_EVIDENCE_AUTHORITY)
        parsed = parse_identity_envelope(payload)
        self.assertEqual(parsed.to_dict(), payload)
        self.assertEqual(parse_identity_envelope(json.dumps(payload)).to_dict(), payload)

    def test_binding_digest_is_order_independent_but_value_sensitive(self):
        first = build_identity_envelope(_bindings())
        reordered = {key: _bindings()[key] for key in reversed(tuple(_bindings()))}
        second = build_identity_envelope(reordered)
        self.assertEqual(first["binding_sha256"], second["binding_sha256"])

        changed = copy.deepcopy(first)
        changed["bindings"]["model"]["id"] = "different-model"
        with self.assertRaises(IdentityDriftError):
            parse_identity_envelope(changed)

    def test_missing_component_and_nested_field_fail_fast(self):
        for field in IDENTITY_COMPONENTS:
            with self.subTest(field=field):
                value = _bindings()
                del value[field]
                with self.assertRaises(MissingFieldError):
                    build_identity_envelope(value)

        value = _bindings()
        del value["base"]["tree_sha"]
        with self.assertRaises(MissingFieldError):
            build_identity_envelope(value)

    def test_unknown_fields_and_malformed_hashes_fail_fast(self):
        value = _bindings()
        value["unexpected"] = {"id": "x", "sha256": _digest("x")}
        with self.assertRaises(UnknownFieldError):
            build_identity_envelope(value)

        value = _bindings()
        value["runner"]["extra"] = "drift"
        with self.assertRaises(UnknownFieldError):
            build_identity_envelope(value)

        value = _bindings()
        value["patch"]["sha256"] = "not-a-sha256"
        with self.assertRaises(IdentityEnvelopeError):
            build_identity_envelope(value)

        value = _bindings()
        value["final_tree"]["tree_sha"] = "A" * 40
        with self.assertRaises(IdentityEnvelopeError):
            build_identity_envelope(value)

    def test_authority_boundary_rejects_remote_ci_claims(self):
        payload = build_identity_envelope(_bindings())
        remote = copy.deepcopy(payload)
        remote["authority"] = "remote-ci-postgresql-primary/v1"
        with self.assertRaises(AuthorityBoundaryError):
            parse_identity_envelope(remote)

        remote = copy.deepcopy(payload)
        remote["operation_id"] = "op-1"
        with self.assertRaises(AuthorityBoundaryError):
            parse_identity_envelope(remote)

    def test_duplicate_json_keys_fail_fast(self):
        with self.assertRaises(IdentityEnvelopeError):
            parse_identity_envelope('{"schema":"x","schema":"y"}')

    def test_verify_detects_cross_task_drift(self):
        first = build_identity_envelope(_bindings())
        other_bindings = _bindings()
        other_bindings["task"]["id"] = "task-2"
        second = build_identity_envelope(other_bindings)
        with self.assertRaisesRegex(IdentityDriftError, "bindings.task"):
            verify_identity_envelope(first, second)
        self.assertEqual(verify_identity_envelope(first, first).to_dict(), first)

    def test_disk_loader_is_bounded_and_strict(self):
        payload = build_identity_envelope(_bindings())
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "identity.json"
            path.write_text(canonical_json(payload), encoding="utf-8")
            self.assertEqual(load_identity_envelope(path).to_dict(), payload)

            path.write_bytes(b"{\"schema\": \"unknown\"}")
            with self.assertRaises(IdentityEnvelopeError):
                load_identity_envelope(path)


if __name__ == "__main__":
    unittest.main()
