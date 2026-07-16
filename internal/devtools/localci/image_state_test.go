//go:build unix

package localci

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

const (
	imageStateDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	imageStateCommit = "1111111111111111111111111111111111111111"
	imageStateNext   = "2222222222222222222222222222222222222222"
	imageStateTree   = "3333333333333333333333333333333333333333"
	imageStateTree2  = "4444444444444444444444444444444444444444"
)

func TestAcceptedImageStateEmptyBootstrapAndLoad(t *testing.T) {
	state, root, fixture := newAcceptedImageStateFixture(t)
	if _, err := state.Load(context.Background()); !errors.Is(err, ErrAcceptedImageStateNotFound) {
		t.Fatalf("Load(empty) error = %v, want ErrAcceptedImageStateNotFound", err)
	}
	record := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := state.Bootstrap(context.Background(), record); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	loaded, err := state.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, record) {
		t.Fatalf("Load() = %#v, want %#v", loaded, record)
	}
	assertPrivateRegularFile(t, filepath.Join(root, acceptedImageStateName))
	if err := state.Bootstrap(context.Background(), record); !errors.Is(err, ErrAcceptedImageStateExists) {
		t.Fatalf("second Bootstrap() error = %v, want ErrAcceptedImageStateExists", err)
	}
}

func TestAcceptedImageStateBootstrapRejectsInvalidStateAndSignature(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*gate.AcceptedImageRecord)
	}{
		{name: "generation", mutate: func(record *gate.AcceptedImageRecord) { record.Generation = 2 }},
		{name: "predecessor", mutate: func(record *gate.AcceptedImageRecord) { record.PreviousRecordDigest = imageStateDigest }},
		{name: "wrong signer", mutate: func(record *gate.AcceptedImageRecord) { record.Signer.KeyID = "unknown-key" }},
		{name: "wrong signature", mutate: func(record *gate.AcceptedImageRecord) {
			record.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, _, fixture := newAcceptedImageStateFixture(t)
			record := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
			tt.mutate(&record)
			if err := state.Bootstrap(context.Background(), record); err == nil {
				t.Fatal("Bootstrap() accepted invalid record")
			}
			if _, err := state.Load(context.Background()); !errors.Is(err, ErrAcceptedImageStateNotFound) {
				t.Fatalf("Load() after rejected bootstrap error = %v", err)
			}
		})
	}
}

func TestAcceptedImageStateLoadRejectsTamperAndMalformedJSON(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, data []byte, record gate.AcceptedImageRecord) []byte
	}{
		{name: "signed field tamper", mutate: tamperAcceptedImageRepo},
		{name: "unknown field", mutate: addAcceptedImageUnknownField},
		{name: "trailing json", mutate: func(_ *testing.T, data []byte, _ gate.AcceptedImageRecord) []byte {
			return append(data, []byte("{}\n")...)
		}},
		{name: "partial write", mutate: func(_ *testing.T, data []byte, _ gate.AcceptedImageRecord) []byte { return data[:len(data)/2] }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, root, fixture := newAcceptedImageStateFixture(t)
			record := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
			if err := state.Bootstrap(context.Background(), record); err != nil {
				t.Fatalf("Bootstrap() error = %v", err)
			}
			path := filepath.Join(root, acceptedImageStateName)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			if err := os.WriteFile(path, tt.mutate(t, data, record), privateSchedulerFileMode); err != nil {
				t.Fatalf("WriteFile(tamper) error = %v", err)
			}
			if _, err := state.Load(context.Background()); err == nil {
				t.Fatal("Load() accepted tampered state")
			}
		})
	}
}

func TestAcceptedImageStatePromoteCAS(t *testing.T) {
	state, _, fixture := newAcceptedImageStateFixture(t)
	current := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := state.Bootstrap(context.Background(), current); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	promotion := fixture.promotion(t, current, imageStateNext, imageStateTree2)
	if err := state.PromoteCAS(context.Background(), promotion); err != nil {
		t.Fatalf("PromoteCAS() error = %v", err)
	}
	loaded, err := state.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Generation != 2 || loaded.TrustedCommit != imageStateNext || loaded.PreviousRecordDigest != promotion.ExpectedRecordDigest {
		t.Fatalf("promoted record = %#v", loaded)
	}
	if err := state.PromoteCAS(context.Background(), promotion); !errors.Is(err, ErrAcceptedImageCASConflict) {
		t.Fatalf("stale PromoteCAS() error = %v, want ErrAcceptedImageCASConflict", err)
	}
}

func TestAcceptedImageStateRejectsInvalidPromotions(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*gate.PromotionRecord)
		wantErr error
	}{
		{name: "generation skip", mutate: func(p *gate.PromotionRecord) { p.Next.Generation = 3 }},
		{name: "trusted ref change", mutate: func(p *gate.PromotionRecord) { p.Next.TrustedRef = "refs/heads/other" }},
		{name: "repo change", mutate: func(p *gate.PromotionRecord) { p.Next.RepoID = "other-repo" }},
		{name: "rollback", mutate: func(p *gate.PromotionRecord) { p.Next.TrustedCommit = strings.Repeat("0", 39) + "1" }, wantErr: ErrAcceptedImageRollback},
		{name: "cas digest", mutate: func(p *gate.PromotionRecord) { p.ExpectedRecordDigest = imageStateDigest }},
		{name: "cas generation", mutate: func(p *gate.PromotionRecord) { p.ExpectedGeneration = 9 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, _, fixture := newAcceptedImageStateFixture(t)
			current := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
			if err := state.Bootstrap(context.Background(), current); err != nil {
				t.Fatalf("Bootstrap() error = %v", err)
			}
			promotion := fixture.promotion(t, current, imageStateNext, imageStateTree2)
			tt.mutate(&promotion)
			fixture.sign(t, &promotion.Next)
			err := state.PromoteCAS(context.Background(), promotion)
			if err == nil {
				t.Fatal("PromoteCAS() accepted invalid transition")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("PromoteCAS() error = %v, want %v", err, tt.wantErr)
			}
			loaded, loadErr := state.Load(context.Background())
			if loadErr != nil || loaded.Generation != 1 {
				t.Fatalf("state changed after rejected promotion: record=%#v error=%v", loaded, loadErr)
			}
		})
	}
}

func TestAcceptedImageStateConcurrentDoublePromoteOnlyOneSucceeds(t *testing.T) {
	first, root, fixture := newAcceptedImageStateFixture(t)
	second, err := NewAcceptedImageState(root, fixture.verifier, fixture.ancestry)
	if err != nil {
		t.Fatalf("NewAcceptedImageState(second) error = %v", err)
	}
	current := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := first.Bootstrap(context.Background(), current); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	promotion := fixture.promotion(t, current, imageStateNext, imageStateTree2)
	start := make(chan struct{})
	errorsByWorker := make(chan error, 2)
	var workers errgroup.Group
	for _, state := range []*AcceptedImageState{first, second} {
		candidate := state
		workers.Go(func() error {
			<-start
			errorsByWorker <- candidate.PromoteCAS(context.Background(), promotion)
			return nil
		})
	}
	close(start)
	if err := workers.Wait(); err != nil {
		t.Fatalf("promotion workers error = %v", err)
	}
	close(errorsByWorker)
	assertConcurrentPromotionResults(t, errorsByWorker)
}

func assertConcurrentPromotionResults(t *testing.T, errorsByWorker <-chan error) {
	t.Helper()
	successes, conflicts := 0, 0
	for err := range errorsByWorker {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAcceptedImageCASConflict):
			conflicts++
		default:
			t.Fatalf("PromoteCAS() unexpected error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestAcceptedImageStateIgnoresCrashTempResidue(t *testing.T) {
	state, root, fixture := newAcceptedImageStateFixture(t)
	residue := filepath.Join(root, ".accepted-image-crash.tmp")
	if err := os.WriteFile(residue, []byte("partial"), privateSchedulerFileMode); err != nil {
		t.Fatalf("WriteFile(residue) error = %v", err)
	}
	record := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := state.Bootstrap(context.Background(), record); err != nil {
		t.Fatalf("Bootstrap() with residue error = %v", err)
	}
	promotion := fixture.promotion(t, record, imageStateNext, imageStateTree2)
	if err := state.PromoteCAS(context.Background(), promotion); err != nil {
		t.Fatalf("PromoteCAS() with residue error = %v", err)
	}
	if _, err := os.Stat(residue); err != nil {
		t.Fatalf("authority modified unrelated crash residue: %v", err)
	}
}

func TestAcceptedImageStateRejectsRootSymlink(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "authority-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("Symlink(root) error = %v", err)
	}
	fixture := newAcceptedImageCryptoFixture(t)
	if _, err := NewAcceptedImageState(link, fixture.verifier, fixture.ancestry); err == nil {
		t.Fatal("NewAcceptedImageState() accepted root symlink")
	}
}

func TestAcceptedImageStateRejectsStateSymlink(t *testing.T) {
	state, root, _ := newAcceptedImageStateFixture(t)
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("{}\n"), privateSchedulerFileMode); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, acceptedImageStateName)); err != nil {
		t.Fatalf("Symlink(state) error = %v", err)
	}
	if _, err := state.Load(context.Background()); err == nil {
		t.Fatal("Load() accepted state symlink")
	}
}

func TestAcceptedImageStateRejectsLockSymlink(t *testing.T) {
	state, root, _ := newAcceptedImageStateFixture(t)
	target := filepath.Join(root, "target-lock")
	if err := os.WriteFile(target, nil, privateSchedulerFileMode); err != nil {
		t.Fatalf("WriteFile(lock target) error = %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, acceptedImageLockName)); err != nil {
		t.Fatalf("Symlink(lock) error = %v", err)
	}
	if _, err := state.Load(context.Background()); err == nil {
		t.Fatal("Load() accepted lock symlink")
	}
}

func TestAcceptedImageStateRejectsRootMode(t *testing.T) {
	root := acceptedImageCanonicalTempDir(t)
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	fixture := newAcceptedImageCryptoFixture(t)
	if _, err := NewAcceptedImageState(root, fixture.verifier, fixture.ancestry); err == nil {
		t.Fatal("NewAcceptedImageState() accepted shared root mode")
	}
}

func TestAcceptedImageStateRejectsStateModeAndOwner(t *testing.T) {
	state, root, fixture := newAcceptedImageStateFixture(t)
	record := fixture.signedRecord(t, 1, "", imageStateCommit, imageStateTree)
	if err := state.Bootstrap(context.Background(), record); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	path := filepath.Join(root, acceptedImageStateName)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod(state) error = %v", err)
	}
	if _, err := state.Load(context.Background()); err == nil {
		t.Fatal("Load() accepted shared state mode")
	}
	info, err := os.Lstat(root)
	if err != nil {
		t.Fatalf("Lstat(root) error = %v", err)
	}
	if err := validatePrivateOwnerAndMode(info, state.ownerUID+1, true); err == nil {
		t.Fatal("owner validation accepted a different owner UID")
	}
}

func TestAcceptedImageStateRejectsTypedNilAndCancelledContext(t *testing.T) {
	root := acceptedImageCanonicalTempDir(t)
	fixture := newAcceptedImageCryptoFixture(t)
	var nilVerifier *fixtureSignatureVerifier
	if _, err := NewAcceptedImageState(root, nilVerifier, fixture.ancestry); err == nil {
		t.Fatal("NewAcceptedImageState() accepted typed nil verifier")
	}
	var nilAncestry *fixtureAncestryVerifier
	if _, err := NewAcceptedImageState(root, fixture.verifier, nilAncestry); err == nil {
		t.Fatal("NewAcceptedImageState() accepted typed nil ancestry verifier")
	}
	state, err := NewAcceptedImageState(root, fixture.verifier, fixture.ancestry)
	if err != nil {
		t.Fatalf("NewAcceptedImageState() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := state.Load(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load(cancelled) error = %v, want context.Canceled", err)
	}
}

type acceptedImageCryptoFixture struct {
	verifier *fixtureSignatureVerifier
	ancestry *fixtureAncestryVerifier
	private  ed25519.PrivateKey
	signer   gate.SignerIdentity
}

func newAcceptedImageStateFixture(t *testing.T) (*AcceptedImageState, string, *acceptedImageCryptoFixture) {
	t.Helper()
	root := acceptedImageCanonicalTempDir(t)
	fixture := newAcceptedImageCryptoFixture(t)
	state, err := NewAcceptedImageState(root, fixture.verifier, fixture.ancestry)
	if err != nil {
		t.Fatalf("NewAcceptedImageState() error = %v", err)
	}
	return state, root, fixture
}

func acceptedImageCanonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp dir) error = %v", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(temp dir) error = %v", err)
	}
	return root
}

func newAcceptedImageCryptoFixture(t *testing.T) *acceptedImageCryptoFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer := gate.SignerIdentity{KeyID: "accepted-image-test", KeyEpoch: 1, Algorithm: gate.SignatureAlgorithmEd25519}
	key := fixtureSignerKey(signer)
	return &acceptedImageCryptoFixture{
		verifier: &fixtureSignatureVerifier{keys: map[string]ed25519.PublicKey{key: public}},
		ancestry: &fixtureAncestryVerifier{allowed: map[string]bool{imageStateCommit + "->" + imageStateNext: true}},
		private:  private,
		signer:   signer,
	}
}

func (f *acceptedImageCryptoFixture) signedRecord(
	t *testing.T,
	generation uint64,
	previous string,
	commit string,
	tree string,
) gate.AcceptedImageRecord {
	t.Helper()
	record := gate.AcceptedImageRecord{
		SchemaVersion:    gate.AcceptedImageRecordSchemaVersion,
		RepoID:           "repo-1",
		TrustedRef:       "refs/heads/main",
		TrustedCommit:    commit,
		SourceTree:       tree,
		PolicyDigest:     imageStateDigest,
		ImageInputDigest: imageStateDigest,
		Image: gate.ImageIdentity{
			Registry:               "registry.invalid/super-dolphin/gate",
			OCIIndexDigest:         imageStateDigest,
			PlatformManifestDigest: imageStateDigest,
			ConfigDigest:           imageStateDigest,
			RootFSDiffIDs:          []string{imageStateDigest},
			OS:                     "linux",
			Architecture:           "arm64",
		},
		Runner: gate.TrustedRunnerIdentity{
			BinaryDigest: imageStateDigest,
			Signer:       f.signer,
			PolicyDigest: imageStateDigest,
		},
		Generation:           generation,
		PreviousRecordDigest: previous,
		AcceptedAt:           time.Date(2026, 7, 16, 12, int(generation), 0, 0, time.UTC),
		Signer:               f.signer,
	}
	f.sign(t, &record)
	return record
}

func (f *acceptedImageCryptoFixture) promotion(
	t *testing.T,
	current gate.AcceptedImageRecord,
	nextCommit string,
	nextTree string,
) gate.PromotionRecord {
	t.Helper()
	digest, err := gate.AcceptedImageRecordDigest(current)
	if err != nil {
		t.Fatalf("AcceptedImageRecordDigest() error = %v", err)
	}
	next := f.signedRecord(t, current.Generation+1, digest, nextCommit, nextTree)
	return gate.PromotionRecord{
		SchemaVersion:        gate.PromotionRecordSchemaVersion,
		ExpectedRecordDigest: digest,
		ExpectedGeneration:   current.Generation,
		Next:                 next,
	}
}

func (f *acceptedImageCryptoFixture) sign(t *testing.T, record *gate.AcceptedImageRecord) {
	t.Helper()
	record.Signature = ""
	payload, err := gate.AcceptedImageSigningPayload(*record)
	if err != nil {
		t.Fatalf("AcceptedImageSigningPayload() error = %v", err)
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(f.private, payload))
}

type fixtureSignatureVerifier struct {
	keys map[string]ed25519.PublicKey
}

func (v *fixtureSignatureVerifier) VerifyAcceptedImage(
	ctx context.Context,
	signer gate.SignerIdentity,
	payload []byte,
	signature string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	public, ok := v.keys[fixtureSignerKey(signer)]
	if !ok {
		return errors.New("fixture signer is not trusted")
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("decode fixture signature: %w", err)
	}
	if !ed25519.Verify(public, payload, decoded) {
		return errors.New("fixture signature is invalid")
	}
	return nil
}

func fixtureSignerKey(signer gate.SignerIdentity) string {
	return fmt.Sprintf("%s/%d/%s", signer.KeyID, signer.KeyEpoch, signer.Algorithm)
}

type fixtureAncestryVerifier struct {
	allowed map[string]bool
}

func (v *fixtureAncestryVerifier) IsAncestor(
	ctx context.Context,
	_ string,
	_ string,
	ancestor string,
	descendant string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return ancestor == descendant || v.allowed[ancestor+"->"+descendant], nil
}

func tamperAcceptedImageRepo(t *testing.T, _ []byte, record gate.AcceptedImageRecord) []byte {
	t.Helper()
	record.RepoID = "tampered-repo"
	data, err := canonicalAcceptedImageBytes(record)
	if err != nil {
		t.Fatalf("canonicalAcceptedImageBytes(tampered) error = %v", err)
	}
	return data
}

func addAcceptedImageUnknownField(t *testing.T, data []byte, _ gate.AcceptedImageRecord) []byte {
	t.Helper()
	if len(data) < 2 || string(data[len(data)-2:]) != "}\n" {
		t.Fatalf("canonical data suffix = %q", data)
	}
	result := append([]byte(nil), data[:len(data)-2]...)
	return append(result, []byte(",\"unknown\":true}\n")...)
}

func assertPrivateRegularFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%s) error = %v", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != privateSchedulerFileMode {
		t.Fatalf("state mode = %v, want regular 0600", info.Mode())
	}
}
