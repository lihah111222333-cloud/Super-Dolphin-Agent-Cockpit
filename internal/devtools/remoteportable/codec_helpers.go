package remoteportable

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func portableFramedDigest(domain string, payload []byte) string {
	material := appendPortableFrame(appendPortableFrame(nil, []byte(domain)), payload)
	digest := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func appendPortableFrame(material, value []byte) []byte {
	frame := make([]byte, 8+len(value))
	binary.BigEndian.PutUint64(frame[:8], uint64(len(value)))
	copy(frame[8:], value)
	return append(material, frame...)
}

func walkPortableManifestJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkPortableJSONValue(decoder); err != nil {
		return fmt.Errorf("portable JSON is not strict: %w", err)
	}
	return ensurePortableManifestEOF(decoder)
}

func walkPortableJSONValue(decoder *json.Decoder) error {
	tokenValue, err := decoder.Token()
	if err != nil {
		return err
	}
	if tokenValue == nil {
		return errors.New("JSON null is not allowed")
	}
	delimiter, ok := tokenValue.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return walkPortableJSONObject(decoder)
	case '[':
		for decoder.More() {
			if err := walkPortableJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("JSON array is unterminated")
		}
		return nil
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

func walkPortableJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("duplicate JSON object key %q", name)
		}
		seen[name] = struct{}{}
		if err := walkPortableJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON object is unterminated")
	}
	return nil
}

func ensurePortableManifestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("portable JSON has trailing data")
	}
	return nil
}
