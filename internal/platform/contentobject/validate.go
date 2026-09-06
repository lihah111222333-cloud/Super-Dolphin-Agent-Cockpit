package contentobject

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// validate 校验object put身份、密文size/hash与self-digest。
func (r PutRequest) validate() error {
	if r.SchemaVersion != 1 || r.ObjectVersion <= 0 || r.CiphertextSize <= 0 || r.CiphertextSize > MaxCiphertextBytes {
		return ErrInvalidObjectRequest
	}
	if int64(len(r.Ciphertext)) != r.CiphertextSize || r.CiphertextHash != hashCiphertext(r.Ciphertext) {
		return ErrInvalidObjectRequest
	}
	if err := validateObjectIdentity(r.OperationID, r.RequestDigest, r.ObjectID); err != nil {
		return err
	}
	want, err := ComputePutRequestDigest(r)
	if err != nil || want != r.RequestDigest {
		return ErrInvalidObjectRequest
	}
	return validateObjectDigest("ciphertext_hash", r.CiphertextHash)
}

func (r QueryRequest) validate() error {
	if r.SchemaVersion != 1 || r.ObjectVersion <= 0 {
		return ErrInvalidObjectRequest
	}
	return validateObjectIdentity(r.OperationID, r.RequestDigest, r.ObjectID)
}

// validate 校验receipt完整字段、UTC时间与self-digest。
func (r PutReceipt) validate() error {
	if err := r.validateHeader(); err != nil {
		return err
	}
	if err := r.validateIdentityAndDigests(); err != nil {
		return err
	}
	want, err := computeReceiptDigest(r)
	if err != nil || want != r.ReceiptDigest {
		return ErrObjectCorrupt
	}
	return nil
}

// Validate 供CONTENT owner在写metadata前复验object backend receipt。
func (r PutReceipt) Validate() error { return r.validate() }

// validateHeader 校验receipt版本、对象版本、密文size与UTC时间。
func (r PutReceipt) validateHeader() error {
	if r.SchemaVersion != 1 || r.ObjectVersion <= 0 {
		return ErrObjectCorrupt
	}
	if r.CiphertextSize <= 0 || r.CiphertextSize > MaxCiphertextBytes {
		return ErrObjectCorrupt
	}
	if r.RecordedAt.IsZero() || r.RecordedAt.Location() != timeUTC() {
		return ErrObjectCorrupt
	}
	return nil
}

func (r PutReceipt) validateIdentityAndDigests() error {
	for name, value := range map[string]string{
		"store_id": r.StoreID, "operation_id": r.OperationID, "object_id": r.ObjectID,
		"object_ref": r.ObjectRef, "receipt_ref": r.ReceiptRef,
	} {
		if !validObjectText(value, 512) {
			return fmt.Errorf("%w: %s is invalid", ErrObjectCorrupt, name)
		}
	}
	for name, value := range map[string]string{
		"request_digest": r.RequestDigest, "ciphertext_hash": r.CiphertextHash, "receipt_digest": r.ReceiptDigest,
	} {
		if err := validateObjectDigest(name, value); err != nil {
			return ErrObjectCorrupt
		}
	}
	return nil
}

func validateObjectIdentity(operationID, requestDigest, objectID string) error {
	if !validObjectText(operationID, 128) || !validObjectText(objectID, 128) {
		return ErrInvalidObjectRequest
	}
	return validateObjectDigest("request_digest", requestDigest)
}

func validateObjectDigest(name, value string) error {
	if len(value) != 64 || value != strings.ToLower(value) {
		return fmt.Errorf("%w: %s must be lowercase 256-bit digest", ErrInvalidObjectRequest, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s is not hexadecimal", ErrInvalidObjectRequest, name)
	}
	return nil
}

func validObjectText(value string, limit int) bool {
	return value != "" && len(value) <= limit && utf8.ValidString(value) &&
		strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

func timeUTC() *time.Location { return time.UTC }
