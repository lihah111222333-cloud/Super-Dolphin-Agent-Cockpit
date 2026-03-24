package store

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	"github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/cwdlock"
	"github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	"github.com/anthropic-ai/super-agent-v3/internal/store/hookstore"
	"github.com/anthropic-ai/super-agent-v3/internal/store/interaction"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
	"github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/store/topologyapproval"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/anthropic-ai/super-agent-v3/internal/store/workspace"
	"github.com/jackc/pgx/v5/pgxpool"
)

var Module = fx.Module("store",
	fx.Provide(func(pool *pgxpool.Pool) *sqlc.Queries { return sqlc.New(pool) }),
	agentstatus.Module,
	ailog.Module,
	auditlog.Module,
	binding.Module,
	buslog.Module,
	cwdlock.Module,
	dbquery.Module,
	hookstore.Module,
	interaction.Module,
	systemlog.Module,
	tasktrace.Module,
	thread.Module,
	topologyapproval.Module,
	uipreference.Module,
	workspace.Module,
)
