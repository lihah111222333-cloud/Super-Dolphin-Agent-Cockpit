-- 0037_thread_pending_launch.sql — defer Claude CLI launch until first turn.
--
-- 用途: 记录 thread 是否处于"仅建行未启动 CLI"状态。用户点击"启动 Agent"
--   时先写一条 pending_launch=true 的 agent_threads 行，直到首轮 turn/start
--   带上真实用户输入，后端才跑 router 分类并 fork Claude CLI，然后在
--   UpdateAgentThreadLaunchResult 里清掉此 flag 并写入 agent_key /
--   prompt_version_id。
-- Go 代码: internal/store/thread/{contract.go,store.go},
--   internal/module/thread/{lifecycle.go,spawn.go,router_resolve.go},
--   internal/module/turn/rpc_helpers.go。

ALTER TABLE public.agent_threads
    ADD COLUMN IF NOT EXISTS pending_launch BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_agent_threads_pending_launch
    ON public.agent_threads (pending_launch)
    WHERE pending_launch = true;
