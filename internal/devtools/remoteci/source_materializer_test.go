package remoteci

import (
	"context"
	"testing"
)

type sourceImporterStub struct {
	bundle string
	object string
	tree   string
	repo   string
	err    error
}

func (stub *sourceImporterStub) ImportAndVerify(_ context.Context, bundlePath string, expectedObject string, expectedTree string) (string, error) {
	stub.bundle = bundlePath
	stub.object = expectedObject
	stub.tree = expectedTree
	return stub.repo, stub.err
}

func TestImportSourceDelegatesGitTruthToSourceExportOwner(t *testing.T) {
	importer := &sourceImporterStub{repo: "/tmp/verified.git"}
	repo, err := importSource(context.Background(), importer, "/tmp/source.bundle", "commit-sha", "tree-sha")
	if err != nil {
		t.Fatalf("importSource() error = %v", err)
	}
	if repo != importer.repo || importer.bundle != "/tmp/source.bundle" || importer.object != "commit-sha" || importer.tree != "tree-sha" {
		t.Fatalf("import call = %#v, repo = %q", importer, repo)
	}
}

func TestImportSourceFailsClosedOnMissingInputsAndEmptyRepository(t *testing.T) {
	for _, test := range []struct {
		name   string
		bundle string
		object string
		tree   string
		repo   string
	}{
		{name: "bundle", object: "object", tree: "tree", repo: "/tmp/repo"},
		{name: "object", bundle: "/tmp/bundle", tree: "tree", repo: "/tmp/repo"},
		{name: "tree", bundle: "/tmp/bundle", object: "object", repo: "/tmp/repo"},
		{name: "result", bundle: "/tmp/bundle", object: "object", tree: "tree"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := importSource(context.Background(), &sourceImporterStub{repo: test.repo}, test.bundle, test.object, test.tree); err == nil {
				t.Fatal("importSource() accepted incomplete source verification")
			}
		})
	}
}
