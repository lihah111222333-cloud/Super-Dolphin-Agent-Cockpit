package operatorprojection

import (
	"context"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentcrypto"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/contentobject"
	"testing"
)

type contractSealer struct{ err error }

func (s contractSealer) Validate() error { return s.err }
func (s contractSealer) CreateAndSeal(context.Context, contentcrypto.SealRequest) (contentcrypto.SealedPayload, error) {
	return contentcrypto.SealedPayload{}, s.err
}

type contractStore struct{ err error }

func (s contractStore) Validate() error { return s.err }
func (s contractStore) Put(context.Context, contentobject.PutRequest) (contentobject.PutReceipt, error) {
	return contentobject.PutReceipt{}, s.err
}
func (s contractStore) QueryPut(context.Context, contentobject.QueryRequest) (contentobject.PutReceipt, error) {
	return contentobject.PutReceipt{}, s.err
}

// TestOperatorProjectionPipelineContract verifies fail-fast dependency contracts.
func TestOperatorProjectionPipelineContract(t *testing.T) {
	db, _ := projectionFixture(t)
	if _, err := NewPipeline(db, contentcrypto.UnavailableSealer{}, contractStore{}); err == nil {
		t.Fatal("expected unavailable sealer rejection")
	}
	if _, err := NewPipeline(db, contractSealer{}, contentobject.UnavailableStore{}); err == nil {
		t.Fatal("expected unavailable store rejection")
	}
}
