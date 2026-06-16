# 修改记录 06-13 datasource 正文解析落库

## 需求

本次围绕 `internal/module/datasource` 扩展上传链路：

- 上传 `.txt` 和 `.pdf` 后解析为文字。
- 将解析后的文字写入数据库存储。
- 数据库表不存在时懒创建，不要求启动阶段预先迁移。
- Prompt datasource 动态段优先使用已落库正文，让模型能直接读取上传文件内容，而不是只看到文件名。

## 行为变化

### 上传流程

`datasource/upload` 现在仍然保持原有 RPC 请求和响应结构：

```json
{
  "sourcePath": "D:\\Downloads\\notes.txt"
}
```

成功响应仍然是：

```json
{
  "name": "notes.txt",
  "extension": ".txt",
  "size": 128,
  "storedPath": "D:\\Super-Dolphin\\.agent\\datasources\\uploads\\notes.txt"
}
```

内部流程变为：

1. 校验 `sourcePath` 必填、绝对路径、普通文件。
2. 校验扩展名只允许 `.txt` 和 `.pdf`。
3. 按扩展名提取正文。
4. 复制原始文件到 `.agent/datasources/uploads`。
5. 将正文 upsert 到 `public.datasource_documents`。

如果正文提取为空，直接返回错误，不写数据库记录。

### 删除流程

`datasource/delete` 保持原有请求和响应结构：

```json
{
  "fileName": "notes.txt"
}
```

删除文件成功后，会同步删除同一 `workspace_root + name` 的正文记录。

### Prompt 注入

Prompt provider 现在优先读取数据库正文：

```md
## datasource

Uploaded datasource file contents available in this workspace.

### notes.txt
first line
second line
```

如果数据库没有正文记录，但上传目录里有文件，则保留旧行为，只注入文件名列表：

```md
## datasource

Uploaded datasource files available in this workspace. Do not infer file contents from names alone.
- manual.pdf
- notes.txt
```

## 数据库设计

新增 `internal/module/datasource/store.go`，通过 `DatasourceDocumentStore` 负责文档正文存储。

表名：

```text
public.datasource_documents
```

懒创建 SQL：

```sql
CREATE TABLE IF NOT EXISTS public.datasource_documents (
  workspace_root text NOT NULL,
  name text NOT NULL,
  extension text NOT NULL,
  size_bytes bigint NOT NULL,
  stored_path text NOT NULL,
  content text NOT NULL,
  created_at timestamp with time zone DEFAULT now() NOT NULL,
  updated_at timestamp with time zone DEFAULT now() NOT NULL,
  PRIMARY KEY (workspace_root, name),
  CHECK (workspace_root <> ''),
  CHECK (name <> ''),
  CHECK (extension <> ''),
  CHECK (stored_path <> ''),
  CHECK (content <> '')
);
```

设计取舍：

- 使用 `workspace_root + name` 作为主键，避免不同项目上传同名文件时互相覆盖。
- 使用 upsert 语义，同一项目同名文件重新上传时正文跟随新文件更新。
- 懒创建只在第一次 store 操作成功后标记为已创建；建表失败会直接返回错误，不吞错。
- 该表目前由 datasource 模块手写 SQL 管理，没有进入 sqlc 静态查询链路。

## PDF 解析说明

新增 `internal/module/datasource/pdf_extract.go`。

当前实现是轻量文本提取器，不引入第三方依赖：

- 扫描 PDF `stream ... endstream` 内容。
- 支持未压缩 stream。
- 支持 `/FlateDecode` zlib 压缩 stream。
- 支持 literal string：`(text)`。
- 支持 hex string：`<48656c6c6f>`。
- 支持常见 PDF escape，例如 `\n`、`\r`、`\t`、`\(`、`\)`、`\\` 和 octal escape。
- 支持 UTF-16BE BOM 文本。

限制：

- 不做 OCR，扫描件不会产生正文。
- 不完整实现 PDF spec，只覆盖常见可见文本流。
- 如果无法提取文字，会返回 `datasource: pdf text content not found` 并映射为 RPC invalid params。

## 主要代码变更

### `internal/module/datasource/service.go`

- `Service` 新增 `ListDocuments(ctx, workspaceRoot)`，供 prompt provider 获取已落库正文。
- `NewService` 改为调用 `NewServiceWithStore(nil)`，保留测试和手工构造兼容。
- 新增 `NewServiceWithStore(store)`，用于 fx 注入数据库 store。
- 上传流程拆分为：
  - `prepareUploadSource`
  - `extractDatasourceText`
  - `copyUploadIntoWorkspace`
  - `persistUploadedDocument`
- 删除流程在删除文件后同步调用 `DeleteDocument`。

### `internal/module/datasource/store.go`

- 新增 `DatasourceDocument` DTO。
- 新增 `UpsertDatasourceDocumentParams`。
- 新增 `DatasourceDocumentStore` 接口。
- 新增 `documentStore`，基于 `pgxpool.Pool` 和 `platformdb.Queryable` 执行手写 SQL。
- `ensureTable` 使用 mutex 保护懒创建状态。

### `internal/module/datasource/extract.go`

- 新增 `extractDatasourceText`，按扩展名分发 TXT/PDF 提取。
- 新增 `extractTextFile`。
- 对空正文 fail-fast，返回 `errDatasourceContentEmpty`。

### `internal/module/datasource/pdf_extract.go`

- 新增轻量 PDF stream 文本提取。
- 新增 `errPDFTextNotFound`，用于 RPC 参数错误映射。

### `internal/module/datasource/prompt_provider.go`

- `Resolve` 先调用 `ListDocuments`。
- 有正文记录时渲染正文。
- 没有正文记录时回退到原文件名列表逻辑。
- 渲染时不泄露绝对工作目录路径。

### `internal/module/datasource/module.go`

- fx 新增 `NewDocumentStore`。
- fx 改为提供 `NewServiceWithStore`，让 datasource service 自动拿到可选数据库存储。

### `internal/module/datasource/rpc.go`

- 将 `errDatasourceContentEmpty` 和 `errPDFTextNotFound` 映射为 invalid params。

## 回归测试

新增或调整的重点测试：

- `internal/module/datasource/service_test.go`
  - `TestUploadFilePersistsExtractedTextContent`
  - `TestUploadFilePersistsExtractedPDFTextContent`
  - 调整 `TestUploadFileCopiesPDFAndTXTToWorkingDirUploadDir`，使用带可提取正文的最小 PDF。
- `internal/module/datasource/prompt_provider_test.go`
  - `TestPromptProviderRendersPersistedDatasourceText`
- `internal/module/datasource/store_test.go`
  - `TestDocumentStoreUpsertLazilyCreatesTable`

## 已执行验证

datasource 包全量测试：

```powershell
$env:GOCACHE='D:\Super-Dolphin\.build-cache\go-build'
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/datasource -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/datasource	1.501s
```

WebSocket RPC 路径测试：

```powershell
$env:GOCACHE='D:\Super-Dolphin\.build-cache\go-build'
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/ws_test -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/ws_test	1.455s
```

Prompt datasource 相关测试：

```powershell
$env:GOCACHE='D:\Super-Dolphin\.build-cache\go-build'
& 'C:\Program Files\Go\bin\go.exe' test ./internal/module/prompt -run Datasource -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/prompt	0.640s
```

App fx graph 闭合测试：

```powershell
$env:GOCACHE='D:\Super-Dolphin\.build-cache\go-build'
& 'C:\Program Files\Go\bin\go.exe' test ./internal/app -run TestAppModuleGraphIsClosed -count=1
```

结果：

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/app	0.759s
```

diff 空白检查：

```powershell
git diff --check
```

结果：exit 0，仅输出 Windows 工作区的 LF/CRLF 提示。

## 验证受限说明

按仓库规则尝试运行单文件守卫：

```powershell
$env:REAL_GO_BIN='C:\Program Files\Go\bin\go.exe'
$env:GOCACHE='D:\Super-Dolphin\.build-cache\go-build'
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal\module\datasource\service.go
```

最终 datasource 自身复杂度违规已经清除，但 guard 仍被仓库既有 `internal/provider/codexapp` 包体积规则挡住：

```text
生产文件违规 (2):
  • 包文件数 internal/provider/codexapp: 31 个 > 上限 30
  • 包行数 internal/provider/codexapp: 10010 行 > 上限 10000
```

尝试运行 `go test ./internal/app -count=1` 时，当前 Windows 沙箱无法创建 Bash 实例，脚本类测试失败：

```text
Bash/Service/CreateInstance/E_ACCESSDENIED
```

这不是 datasource fx 注入错误；`TestAppModuleGraphIsClosed` 已单独通过。

尝试运行：

```powershell
make sqlc-verify
```

当前 shell 中没有 `make` 命令，无法执行：

```text
make : The term 'make' is not recognized as the name of a cmdlet, function, script file, or operable program.
```

## 备注

- 本次没有执行 git stage 或 commit。
- 当前 datasource 表采用模块内懒创建，未新增 migrations 文件。
- 如果后续希望让该表进入正式 schema 管理，需要补 migration、sqlc schema 输入和 store 查询生成链路。
