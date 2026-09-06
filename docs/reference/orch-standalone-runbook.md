# 独立 orch 本机 operator 运行手册

## 适用范围与边界

正式入口是 `mcp-orch serve --config <absolute> --ready-file <absolute>`，不是裸 `mcp-orch`。
裸命令仍走 V3 parent/sidecar 启动链；显式 `serve` 不需要 V3 parent、资源发现环境或仓库作为运行 cwd。
本手册描述 `standalone_http_local_v1` 本机 operator profile，不是 strict `unified_v2` 的完成声明。
`wait_agents`、Room、RecoveryCustody 接线与完整故障恢复仍须独立验收；这些缺口不等于现有 `serve` 无法启动，也不得通过降低目标消除。
ready 只证明本次启动链已发布监听；CLI 版本、无模型 probe、HTTP 工具发现均不能证明真实模型任务或全量升级通过。

## 1. 构建与部署

以下命令适用于 macOS/Linux。将所有 `/ABS/...`、`<...>` 占位值替换为操作者确认的实际值；它们不是默认配置。
构建要求仓库及满足 `go.mod` 的 Go 工具链；部署目录须预先创建并由操作者管理：

```sh
cd /ABS/REPO
go build -o /ABS/RELEASE/mcp-orch ./cmd/mcp-orch
```

可部署该二进制到仓库之外；目标平台/架构必须匹配构建产物。运行仍需已安装的官方 provider CLI、其依赖及明确的登录/网络条件。
不需要启动 V3 桌面主进程，不应为修复启动而补造 `GO_AGENT_*`、`SUPER_DOLPHIN_*` 或 V3 resource 文件。

## 2. 固定 provider executable 来源

配置校验用 `Lstat` 拒绝 executable 本身是 symlink；Homebrew 的入口可能是链接，不能直接填入。
对操作者选定的安装入口执行下面的显式解析，记录真实路径、SHA-256 和版本（只运行所选 CLI 的 `--version`）：

```sh
python3 - /ABS/INSTALLED/codex /ABS/INSTALLED/claude <<'PY'
import hashlib, os, pathlib, subprocess, sys
for value in sys.argv[1:]:
    p = pathlib.Path(value)
    if not p.is_absolute():
        raise SystemExit('provider input must be absolute')
    p = p.resolve(strict=True)
    if not p.is_file() or not os.access(p, os.X_OK):
        raise SystemExit('provider must resolve to an executable regular file')
    print('executable:', p)
    with p.open('rb') as f:
        print('sha256:', hashlib.file_digest(f, 'sha256').hexdigest())
    subprocess.run([str(p), '--version'], check=True)
PY
```

该示例使用 Python 3.11+。将解析后的真实文件路径写入 JSON；不要关闭 nonsymlink 校验，也不要复制 CLI 二进制绕开来源。
升级/重装 CLI 后重新解析、核对安装来源、版本与 hash，再显式更新配置并重新验证；旧真实路径可能已不存在。
`ProbeConfiguredProviders` 在 ready 前验证全部配置 provider 的机器协议：Codex app-server initialize、Claude stream-json system/init。
probe 不发真实模型任务，不证明所选模型可用、账户有额度或 coding 能力已验收；任一失败都阻断启动，不切换备用 provider。

## 3. 目录、凭据与完整配置

预先建立 workspace、state、ready 父目录、credential 父目录和显式 provider home；不要把已有私人目录当作待初始化目录。
Unix validator 的私有权限条件是 group/world 位全部为零；使用 **目录 0700、文件 0600** 是可操作的标准设置，而非声称 validator 只允许这两个精确模式。
`state_dir`、ready 父目录必须私有；credential 必须私有普通文件且不能是 symlink；已有 SQLite 主库/WAL/SHM 也接受私有性和身份校验。
workspace/provider home 必须是已存在的绝对目录（配置校验并不要求其恰好为 0700）；建议新建专用目录为 0700。
配置建议存为 0600；`LoadConfig` 本身检查普通文件、大小和严格 JSON，而非配置文件的 owner-only 权限。
Windows 使用 securefs 的私有 ACL 校验，不能把 Unix chmod 示例当作 Windows ACL 验收。

在已存在、受信任的父目录下新建本实例的私有目录，路径存在即失败，不自动修改现有目录权限：

```sh
python3 - /ABS/PRIVATE/orch-state /ABS/PRIVATE/orch-ready /ABS/PRIVATE/orch-credentials <<'PY'
import os, pathlib, sys
os.umask(0o077)
for value in sys.argv[1:]:
    p = pathlib.Path(value)
    if not p.is_absolute():
        raise SystemExit('directory must be absolute')
    p.mkdir(mode=0o700, parents=False, exist_ok=False)
PY
```

只在指定新文件生成随机 bearer，已有文件（含链接）直接失败；不打印 secret，不把 secret 放命令行：

```sh
python3 - /ABS/PRIVATE/orch-credentials/bearer.txt <<'PY'
import os, pathlib, secrets, stat, sys
p = pathlib.Path(sys.argv[1])
if not p.is_absolute() or not p.parent.is_dir():
    raise SystemExit('credential requires an existing absolute parent')
if stat.S_IMODE(p.parent.stat().st_mode) & 0o077:
    raise SystemExit('credential parent must be private')
os.umask(0o077)
fd = os.open(p, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w', encoding='ascii') as f:
    f.write(secrets.token_urlsafe(48) + '\n')
    f.flush()
    os.fsync(f.fileno())
PY
```

将下列完整 JSON 存到操作者指定的新配置文件（不覆盖已有配置）；每个模型必须明确选择并与账户/provider 核验：

```json
{
  "schema_version": 1,
  "workspace_root": "/ABS/WORKSPACE",
  "state_dir": "/ABS/PRIVATE/orch-state",
  "listen": "127.0.0.1:0",
  "credential_file": "/ABS/PRIVATE/orch-credentials/bearer.txt",
  "principal_ref": "operator/<EXPLICIT_OPERATOR_ID>",
  "scope_ref": "workspace/<EXPLICIT_SCOPE_ID>",
  "providers": [
    {"name": "codex", "executable": "/ABS/RESOLVED/CODEX_EXECUTABLE", "home": "/ABS/SELECTED/CODEX_HOME", "models": ["<EXPLICIT_CODEX_MODEL>"], "sandbox": "workspace-write"},
    {"name": "claude", "executable": "/ABS/RESOLVED/CLAUDE_EXECUTABLE", "home": "/ABS/SELECTED/CLAUDE_HOME", "models": ["<EXPLICIT_CLAUDE_MODEL>"]}
  ]
}
```

`127.0.0.1:0` 显式请求内核选择 loopback 端口；也可显式指定未占用端口，不能使用公网地址或 `localhost` 名称。
Codex 必须显式声明 sandbox；coding 示例选 `workspace-write`，不是全权默认。Claude 不接受 Codex sandbox 字段。
`principal_ref`/`scope_ref` 是启动时绑定的 operator 身份引用，不是 child grant，也不是从 token、路径或客户端 metadata 推导的授权。
provider home 由操作者指定，分别传作 `CODEX_HOME`/`CLAUDE_CONFIG_DIR`，可能含用户登录状态；空目录不等于已登录。
子进程剥离父 `GIT_DIR/GIT_WORK_TREE/GIT_INDEX_FILE` 等仓库局部上下文及 indexed Git config；认证/作者环境保留，仓库按任务 cwd 解析。
本手册不扫描/迁移个人 state，不修改 `~/.codex/config.toml`、`~/.claude/settings.json` 或账户凭据；真实 CLI 自身可能在所选 home 写运行状态。

## 4. 启动、发现、请求与停止

```sh
cd /ABS/NON_REPO_CWD
/ABS/RELEASE/mcp-orch serve --config /ABS/PRIVATE/serve.json --ready-file /ABS/PRIVATE/orch-ready/ready.json &
orch_pid=$!
# 操作者确认 ready 存在且该进程仍存活后，才连接 ready.endpoint。
# 完成操作后，在同一 shell 中正常停止并检查退出结果：
kill -TERM "$orch_pid"
wait "$orch_pid"
```

ready 包含 `endpoint`、`instance_id`，不含 token，不能授权；不要把发现文件当作 bearer 或活跃 owner 的最终证据。
正式 serve 持实例锁后可接管已确认陈旧的 ready；拒绝活跃发布者，不应手工删除活跃 ready/锁文件或替换 state 目录。
SIGTERM 触发统一停止流程（10 秒停止 context）、关闭监听、清理本实例 ready、释放实例锁；仍需检查退出状态及后代进程回收。
同一物理 state 的第二 owner 被拒绝；不同私有 state 可并行，但须使用各自 ready 路径及不冲突的监听端口，状态不会自动合并。

HTTP/MCP 客户端必须从私有文件读取 bearer，在内存中设置 Authorization，禁止记录 token。
当前仓库 fixture 使用协议 `2025-11-25`：先 `initialize`，保存返回 `Mcp-Session-Id`，再发无 id 的 `notifications/initialized`，之后才 `tools/list`/`tools/call`。
请求使用 JSON-RPC 2.0、`Content-Type: application/json`、`Accept: application/json, text/event-stream`；后续请求带 session 和 `MCP-Protocol-Version`。
参考 `cmd/mcp-orch/standalone_daemon_e2e_test.go` 的 `initializeServeE2E`、`requestServeE2E`、`assertServeListAgents`；不要用省略握手的临时 curl 冒充客户端验收。

## 5. 验证层级与故障定位

仓库内使用正式测试包装器及精确 selector（不是 raw go test）：

```sh
scripts/test_with_guard.sh --host-test medium -tags=e2e ./cmd/mcp-orch -run '^TestMCPOrchServeBlackBoxSingleInstance$' -timeout=600s -count=1
scripts/test_with_guard.sh --host-test medium -tags=e2e ./cmd/mcp-orch -run '^TestStandaloneCodexClientLaunchesManagedCodexThroughMCP$' -timeout=600s -count=1
scripts/test_with_guard.sh --host-test medium -tags=e2e ./cmd/mcp-orch -run '^TestStandaloneBinaryLaunchesManagedClaudeThroughMCP$' -timeout=600s -count=1
```

第一条是受控 fixture 黑盒，不证明真实模型；后两条会真实调用模型并可能计费，运行前显式设置对应 `ORCH_E2E_CODEX_BIN/HOME/MODEL` 或 `ORCH_E2E_CLAUDE_BIN/HOME/MODEL` 三个环境变量（分别展开为三个完整变量名）。
`BIN` 必须是真实绝对可执行路径，`HOME` 为操作者批准的绝对目录；不自动发现账户。真实 E2E 的 PASS 仅覆盖所选测试、CLI、模型和当次环境，不覆盖全部故障恢复/strict profile。

| 故障分类 | 当前稳定错误片段与源码 | 操作者动作 |
| --- | --- | --- |
| 错误入口 | `unknown orch command; expected serve`，`serve.go:runCommand` | 使用显式 serve；裸命令是另一入口，不是独立服务 |
| 缺失/相对参数 | `orch serve config and ready-file must both be absolute paths`，`serve.go:parseServeOptions` | 两参数均显式绝对路径；不存在配置的读取错误前缀为 `open orch config` |
| executable 链接/非普通文件 | `orch provider executable must identify a regular file`，`standalone/config.go:validateProviderExecutable` | 显式 resolve 并核验真实安装文件，不放宽检查 |
| 非私有路径 | `private path grants group/world access`，`securefs.CheckPrivateOwnerOnly`；凭据包装前缀 `orch credential permissions` | 核对指定路径/权限，先确认归属，不批量 chmod 个人目录 |
| 实例占用 | `orch instance already running`，`standalone/instance.go:ErrAlreadyRunning` | 找到该 state 的活跃 owner，正常停机或选择独立 state；不删除锁绕过 |
| provider 握手 | `orch provider conformance probe failed`，`standalone/provider_probe.go:ErrProviderProbe` | 核对显式 executable/home 与协议；修正原因后重新启动，不降级 |
