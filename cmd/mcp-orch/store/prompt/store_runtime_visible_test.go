package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestListRuntimeVisibleRequiresCWD(t *testing.T) {
	t.Parallel()

	store := NewStore(newPromptTestDB(t))
	_, err := store.List(context.Background(), ListFilter{RuntimeVisible: true, Limit: 10})
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("List() error = %v, want cwd required", err)
	}
}

func TestListRuntimeVisibleUsesScopedEnabledQuery(t *testing.T) {
	t.Parallel()

	db := newPromptTestDB(t)
	store := NewStore(db)
	insertPromptTemplate(t, db, "repo-a/prompt", "Repo Prompt", true, []string{"scope.cwd:/repo/a"})
	insertPromptTemplate(t, db, "repo-b/prompt", "Repo Prompt", true, []string{"scope.cwd:/repo/b"})
	insertPromptTemplate(t, db, "repo-a/disabled", "Repo Disabled", false, []string{"scope.cwd:/repo/a"})

	got, err := store.List(context.Background(), ListFilter{
		Keyword:        "Repo",
		CWD:            " /repo/a ",
		RuntimeVisible: true,
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 1 || got[0].PromptKey != "repo-a/prompt" {
		t.Fatalf("List() = %+v, want only enabled /repo/a scoped prompt", got)
	}
}
