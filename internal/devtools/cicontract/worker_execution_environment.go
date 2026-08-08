package cicontract

// WorkerExecutionEnvironmentSchemaVersion 标识 worker executor 的稳定语义环境材料。
// 路径、资源、job 和 agent 身份不属于该材料；它们只进入本次执行 provenance。
const WorkerExecutionEnvironmentSchemaVersion = "worker-executor-semantic-env/v1"

// WorkerExecutionProvenanceID 标识实际 worker executor 运行时边界。
// 它是回执 provenance 的稳定 owner，不把 job 或 agent 身份带入 PASS identity。
const WorkerExecutionProvenanceID = "aliyun-eci-worker-executor/v1"

// CanonicalWorkerExecutionEnvironment 返回 normal/e2e/race workload 共用的、
// 会影响结果的显式环境。调用方获得新 slice，可安全追加 invocation-only 变量。
// 缓存、临时目录、工作目录、job、agent 与资源变量故意不在这里。
func CanonicalWorkerExecutionEnvironment() []string {
	return []string{
		"CGO_ENABLED=1",
		"CI=true",
		"GIT_AUTHOR_DATE=946684800 +0000",
		"GIT_AUTHOR_EMAIL=gate-executor@super-dolphin.invalid",
		"GIT_AUTHOR_NAME=Super Dolphin Gate Executor",
		"GIT_COMMITTER_DATE=946684800 +0000",
		"GIT_COMMITTER_EMAIL=gate-executor@super-dolphin.invalid",
		"GIT_COMMITTER_NAME=Super Dolphin Gate Executor",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_TERMINAL_PROMPT=0",
		"GOARCH=amd64",
		"GOENV=off",
		"GOOS=linux",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"LD_LIBRARY_PATH=/usr/lib/x86_64-linux-gnu:/lib/x86_64-linux-gnu:/usr/lib/aarch64-linux-gnu:/lib/aarch64-linux-gnu:/usr/lib:/lib",
		"NPM_CONFIG_AUDIT=false",
		"NPM_CONFIG_FUND=false",
		"NPM_CONFIG_UPDATE_NOTIFIER=false",
		"npm_config_offline=true",
		"npm_config_userconfig=/dev/null",
		"FONTCONFIG_FILE=fonts.conf",
		"FONTCONFIG_PATH=/etc/fonts",
		"GSETTINGS_SCHEMA_DIR=/usr/share/glib-2.0/schemas",
		"SUPER_DOLPHIN_GATE_GIT=/usr/bin/git",
		"SUPER_DOLPHIN_GATE_NODE=/usr/local/bin/node",
		"SUPER_DOLPHIN_TEST_BACKEND=remote-worker",
		"SUPER_DOLPHIN_GATE_XVFB_RUN=/usr/bin/xvfb-run",
		"XDG_DATA_DIRS=/usr/local/share:/usr/share",
		"TZ=UTC",
	}
}
