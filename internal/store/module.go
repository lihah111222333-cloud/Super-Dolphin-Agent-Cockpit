package store

import (
	"database/sql"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/agentstatus"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/ailog"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/auditlog"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/buslog"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/commandcard"
	cronstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/cron"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/cwdlock"
	datasourcestore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/datasource"
	datasourcev2store "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/datasourcev2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/dbquery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/feedback"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/hookstore"
	insightstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/insight"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/interaction"
	mcpserverstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/mcpserver"
	promptstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/prompt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/routingtest"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sharedfile"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/skilltool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/systemlog"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/topologyapproval"
	turndedupestore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/turndedupe"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/uipreference"
)

// Module is the explicit store root assembler exception: this file may import
// store subpackages to wire the shared sqlc queries provider, but the pattern
// must not spread to other root packages and this file must stay free of
// business logic.
var Module = fx.Module("store",
	fx.Provide(func(db *sql.DB) *sqlc.Queries { return sqlc.New(db) }),
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
	datasourcev2store.Module,
	dbquery.Module,
	feedback.Module,
	hookstore.Module,
	insightstore.Module,
	interaction.Module,
	mcpserverstore.Module,
	promptstore.Module,
	routingtest.Module,
	sharedfile.Module,
	skilltool.Module,
	systemlog.Module,
	thread.Module,
	topologyapproval.Module,
	turndedupestore.Module,
	uipreference.Module,
)
