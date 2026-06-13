package store

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	"github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	"github.com/anthropic-ai/super-agent-v3/internal/store/cwdlock"
	datasourcestore "github.com/anthropic-ai/super-agent-v3/internal/store/datasource"
	"github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	"github.com/anthropic-ai/super-agent-v3/internal/store/feedback"
	"github.com/anthropic-ai/super-agent-v3/internal/store/hookstore"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
	"github.com/anthropic-ai/super-agent-v3/internal/store/interaction"
	mcpserverstore "github.com/anthropic-ai/super-agent-v3/internal/store/mcpserver"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/store/routingtest"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	"github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/store/topologyapproval"
	turndedupestore "github.com/anthropic-ai/super-agent-v3/internal/store/turndedupe"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Module is the explicit store root assembler exception: this file may import
// store subpackages to wire the shared sqlc queries provider, but the pattern
// must not spread to other root packages and this file must stay free of
// business logic.
var Module = fx.Module("store",
	fx.Provide(func(pool *pgxpool.Pool) *sqlc.Queries { return sqlc.New(pool) }),
	fx.Provide(func(q *sqlc.Queries) sqlc.Querier { return q }),
	agentstatus.Module,
	ailog.Module,
	auditlog.Module,
	binding.Module,
	buslog.Module,
	commandcard.Module,
	cronstore.Module,
	cwdlock.Module,
	datasourcestore.Module,
	dbquery.Module,
	feedback.Module,
	hookstore.Module,
	insightstore.Module,
	interaction.Module,
	mcpserverstore.Module,
	promptstore.Module,
	routingtest.Module,
	sharedfile.Module,
	systemlog.Module,
	thread.Module,
	topologyapproval.Module,
	turndedupestore.Module,
	uipreference.Module,
)
