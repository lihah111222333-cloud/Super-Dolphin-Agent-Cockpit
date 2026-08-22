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

// EncodePortableValidationResult 返回 validate stdout 的 canonical bytes。
func EncodePortableValidationResult(result PortableValidationResult) ([]byte, error) {
	if err := result.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode portable validation result: %w", err)
	}
	if len(encoded) == 0 || len(encoded) > PortableManifestMaxResultBytes {
		return nil, errors.New("portable validation result exceeds 1 MiB")
	}
	return encoded, nil
}

// DecodePortableValidationResult 严格解码并校验 canonical validation result。
func DecodePortableValidationResult(data []byte) (PortableValidationResult, error) {
	if len(data) == 0 || len(data) > PortableManifestMaxResultBytes {
		return PortableValidationResult{}, errors.New("portable validation result size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkPortableJSONValue(decoder); err != nil {
		return PortableValidationResult{}, fmt.Errorf("portable validation result JSON is not strict: %w", err)
	}
	if err := ensurePortableManifestEOF(decoder); err != nil {
		return PortableValidationResult{}, err
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var result PortableValidationResult
	if err := decoder.Decode(&result); err != nil {
		return PortableValidationResult{}, err
	}
	if err := result.Validate(); err != nil {
		return PortableValidationResult{}, err
	}
	canonical, err := EncodePortableValidationResult(result)
	if err != nil || !bytes.Equal(canonical, data) {
		return PortableValidationResult{}, errors.New("portable validation result is not canonical")
	}
	return result, nil
}

// Validate 校验 validation result 的 contract、target、engine facts 和所有摘要。
func (result PortableValidationResult) Validate() error {
	if err := validatePortableValidationIdentity(result); err != nil {
		return err
	}
	if err := validatePortableValidationTarget(result); err != nil {
		return err
	}
	if err := validatePortableValidationDigests(result); err != nil {
		return err
	}
	return validatePortableEngineFacts(result.EngineFacts)
}

func validatePortableValidationIdentity(result PortableValidationResult) error {
	if result.SchemaVersion != PortableValidationResultSchema || result.Status != PortableValidationResultStatusValidated || result.ContractID != PortableManifestContractID || result.Authority != PortableValidationResultAuthority || result.ExecutionPoolContract != PortableManifestContractID {
		return errors.New("portable validation result identity is invalid")
	}
	return nil
}

func validatePortableValidationTarget(result PortableValidationResult) error {
	if err := validatePortableOperationID(result.OperationID); err != nil {
		return errors.New("portable validation result target identity is invalid")
	}
	if !validPortableToken(result.RepositoryID) || !validPortableObjectFormat(result.ObjectFormat) || !validPortableOIDForFormat(result.TargetCommitSHA, result.ObjectFormat) || !validPortableOIDForFormat(result.TargetTreeSHA, result.ObjectFormat) || !validPortableRepoRelativePath(result.ManifestPath) {
		return errors.New("portable validation result target identity is invalid")
	}
	return nil
}

func validatePortableValidationDigests(result PortableValidationResult) error {
	for _, digest := range []string{result.ManifestBlobSHA256, result.ManifestDigest, result.DescriptorSHA256, result.BindingSHA256} {
		if !validPortableDigest(digest) {
			return errors.New("portable validation result digest is invalid")
		}
	}
	return nil
}
