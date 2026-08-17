package appupdaterecovery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)


const journalSchemaVersion = 2

const (
	discardIdentitySchemaVersion = 1
	discardIdentityFileName      = "discard-identity.json"
	discardIdentityFieldChain    = "release_transaction_discard_identity"
	discardRootKindFile          = "file"
	discardRootKindDirectory     = "directory"
)

type journalEntry struct {
	Sequence uint64  `json:"sequence"`
	Trigger  Trigger `json:"trigger"`
	State    State   `json:"state"`
	At       string  `json:"at"`
}

type journalPayload struct {
	SchemaVersion    int                   `json:"schema_version"`
	Identity         Identity              `json:"identity"`
	Paths            Paths                 `json:"paths"`
	Trust            TrustGeneration       `json:"trust"`
	Probation        ProbationRecord       `json:"probation"`
	RollbackRestart  RollbackRestartRecord `json:"rollback_restart"`
	TargetGeneration uint64                `json:"target_generation"`
	Entries          []journalEntry        `json:"entries"`
	CreatedAt        string                `json:"created_at"`
	UpdatedAt        string                `json:"updated_at"`
}

type journalEnvelope struct {
	Payload  []byte `json:"payload"`
	Checksum string `json:"checksum"`
}

type discardRootIdentity struct {
	VolumeID uint64 `json:"volume_id"`
	FileID   uint64 `json:"file_id"`
	Kind     string `json:"kind"`
}

type discardInstanceIdentity struct {
	SchemaVersion int                 `json:"schema_version"`
	TransactionID TransactionID       `json:"transaction_id"`
	Root          discardRootIdentity `json:"root"`
}

func newJournal(req CreateRequest, targetGeneration uint64, now time.Time) journalPayload {
	timestamp := now.UTC().Format(time.RFC3339Nano)
	return journalPayload{
		SchemaVersion:    journalSchemaVersion,
		Identity:         req.Identity,
		Paths:            req.Paths,
		Trust:            req.Trust,
		TargetGeneration: targetGeneration,
		Entries: []journalEntry{{
			Sequence: 1,
			State:    StatePrepared,
			At:       timestamp,
		}},
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
	}
}

func (journal journalPayload) transaction() Transaction {
	last := journal.Entries[len(journal.Entries)-1]
	return Transaction{
		Identity:         journal.Identity,
		Paths:            journal.Paths,
		State:            last.State,
		Trust:            journal.Trust,
		Probation:        journal.Probation,
		RollbackRestart:  journal.RollbackRestart,
		TargetGeneration: journal.TargetGeneration,
		Revision:         last.Sequence,
		CreatedAt:        journal.CreatedAt,
		UpdatedAt:        journal.UpdatedAt,
	}
}

func encodeJournal(journal journalPayload) ([]byte, error) {
	payload, err := json.Marshal(journal)
	if err != nil {
		return nil, fmt.Errorf("encode update transaction journal payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	envelope := journalEnvelope{Payload: payload, Checksum: hex.EncodeToString(sum[:])}
	raw, err := json.MarshalIndent(envelope, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode update transaction journal envelope: %w", err)
	}
	return append(raw, '\n'), nil
}

// decodeJournal 严格校验 envelope、checksum、字段和完整迁移历史。
func decodeJournal(raw []byte) (journalPayload, error) {
	var envelope journalEnvelope
	if err := decodeStrict(raw, &envelope); err != nil {
		return journalPayload{}, fmt.Errorf("decode update transaction journal envelope: %w", err)
	}
	if len(envelope.Payload) == 0 || envelope.Checksum == "" {
		return journalPayload{}, errors.New("update transaction journal payload and checksum are required")
	}
	sum := sha256.Sum256(envelope.Payload)
	if hex.EncodeToString(sum[:]) != envelope.Checksum {
		return journalPayload{}, errors.New("update transaction journal checksum mismatch")
	}
	if err := validateRequiredJSONFields(envelope.Payload, reflect.TypeFor[journalPayload]()); err != nil {
		return journalPayload{}, err
	}
	var journal journalPayload
	if err := decodeStrict(envelope.Payload, &journal); err != nil {
		return journalPayload{}, fmt.Errorf("decode update transaction journal payload: %w", err)
	}
	if err := validateJournal(journal); err != nil {
		return journalPayload{}, err
	}
	return journal, nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func encodeDiscardInstanceIdentity(identity discardInstanceIdentity) ([]byte, error) {
	if err := validateDiscardInstanceIdentity(identity); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup discard identity: %w", err)
	}
	return append(raw, '\n'), nil
}

func decodeDiscardInstanceIdentity(raw []byte) (discardInstanceIdentity, error) {
	producer := reflect.TypeFor[discardInstanceIdentity]()
	if err := validateRequiredJSONFieldsForChain(raw, producer, discardIdentityFieldChain); err != nil {
		return discardInstanceIdentity{}, err
	}
	var identity discardInstanceIdentity
	if err := decodeStrict(raw, &identity); err != nil {
		return discardInstanceIdentity{}, fmt.Errorf("decode backup discard identity: %w", err)
	}
	if err := validateDiscardInstanceIdentity(identity); err != nil {
		return discardInstanceIdentity{}, err
	}
	return identity, nil
}

func validateDiscardInstanceIdentity(identity discardInstanceIdentity) error {
	if identity.SchemaVersion != discardIdentitySchemaVersion {
		return fmt.Errorf("backup discard identity schema = %d, want %d", identity.SchemaVersion, discardIdentitySchemaVersion)
	}
	if err := validateTransactionID(identity.TransactionID); err != nil {
		return fmt.Errorf("validate backup discard transaction identity: %w", err)
	}
	if identity.Root.Kind != discardRootKindFile && identity.Root.Kind != discardRootKindDirectory {
		return fmt.Errorf("backup discard root kind = %q", identity.Root.Kind)
	}
	return nil
}

// validateJournal 验证 journal schema、identity、trust 和时间边界。
func validateJournal(journal journalPayload) error {
	if journal.SchemaVersion != journalSchemaVersion {
		return fmt.Errorf("update transaction journal schema = %d, want %d", journal.SchemaVersion, journalSchemaVersion)
	}
	if err := validateCreateRequest(CreateRequest{
		Identity: journal.Identity,
		Paths:    journal.Paths,
		Trust: TrustGeneration{
			PreviousGeneration: journal.Trust.PreviousGeneration,
			Generation:         journal.Trust.Generation, PackageSigner: journal.Trust.PackageSigner, State: TrustPending,
		},
	}); err != nil {
		return fmt.Errorf("validate update transaction journal identity: %w", err)
	}
	if err := validateEntryHistory(journal.Entries); err != nil {
		return err
	}
	if journal.TargetGeneration == 0 {
		return errors.New("update transaction target generation must be positive")
	}
	last := journal.Entries[len(journal.Entries)-1]
	if journal.Trust.State != trustStateFor(last.State) {
		return fmt.Errorf("trust state = %q, want %q for transaction state %q", journal.Trust.State, trustStateFor(last.State), last.State)
	}
	if err := validateProbationRecord(journal); err != nil {
		return err
	}
	if err := validateRollbackRestartRecord(journal.transaction()); err != nil {
		return err
	}
	if journal.CreatedAt != journal.Entries[0].At || journal.UpdatedAt != last.At {
		return errors.New("update transaction journal timestamps do not match transition history")
	}
	return nil
}

// validateEntryHistory 通过权威 stateless 状态机重放全部 journal entry。
func validateEntryHistory(entries []journalEntry) error {
	if len(entries) == 0 {
		return errors.New("update transaction journal has no entries")
	}
	if entries[0].Sequence != 1 || entries[0].Trigger != "" || entries[0].State != StatePrepared {
		return errors.New("update transaction journal initial entry is invalid")
	}
	if _, err := time.Parse(time.RFC3339Nano, entries[0].At); err != nil {
		return fmt.Errorf("update transaction journal entry 0 timestamp: %w", err)
	}
	current := StatePrepared
	for index, entry := range entries {
		if index == 0 {
			continue
		}
		next, err := validateJournalEntry(index, entry, current)
		if err != nil {
			return err
		}
		current = next
	}
	return nil
}

// validateJournalEntry 校验单条序号、时间和状态机迁移结果。
func validateJournalEntry(index int, entry journalEntry, current State) (State, error) {
	if entry.Sequence != uint64(index+1) || !isKnownState(entry.State) {
		return "", fmt.Errorf("update transaction journal entry %d sequence or state is invalid", index)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.At); err != nil {
		return "", fmt.Errorf("update transaction journal entry %d timestamp: %w", index, err)
	}
	next, err := nextState(current, entry.Trigger)
	if err != nil {
		return "", fmt.Errorf("replay update transaction journal entry %d: %w", index, err)
	}
	if next != entry.State {
		return "", fmt.Errorf("update transaction journal entry %d state = %q, want %q", index, entry.State, next)
	}
	return next, nil
}

// atomicWrite 在同目录写临时文件、fsync、rename 并 fsync 目录。
func atomicWrite(path string, raw []byte) (err error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".journal-*.tmp")
	if err != nil {
		return fmt.Errorf("create update transaction journal temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove update transaction journal temp file: %w", removeErr))
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return closeJournalTemp(temp, fmt.Errorf("chmod update transaction journal temp file: %w", err))
	}
	if _, err := temp.Write(raw); err != nil {
		return closeJournalTemp(temp, fmt.Errorf("write update transaction journal temp file: %w", err))
	}
	if err := temp.Sync(); err != nil {
		return closeJournalTemp(temp, fmt.Errorf("fsync update transaction journal temp file: %w", err))
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close update transaction journal temp file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace update transaction journal: %w", err)
	}
	return securefs.SyncDirectory(dir)
}

func closeJournalTemp(temp *os.File, cause error) error {
	closeErr := temp.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close update transaction journal temp file: %w", closeErr)
	}
	return errors.Join(cause, closeErr)
}

