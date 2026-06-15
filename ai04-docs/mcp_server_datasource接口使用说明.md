# mcp_server 与 datasource 接口使用说明

本文说明 `internal/module/mcp_server` 和 `internal/module/datasource` 当前暴露的 JSON-RPC 调用方式、参数、响应、落盘位置、数据库存储和 prompt 注入行为。

## 调用方式

这两组接口不是 REST 接口，而是桌面端后端注册的 JSON-RPC 方法。

后端 RPC 平台会把方法注册到 WebSocket JSON-RPC 通道；前端代码中可通过通用封装调用：

```js
import { callBackend } from './shared/api/backendApi.js';

const result = await callBackend('datasource/list', {});
```

原始 JSON-RPC 请求形态如下：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "datasource/list",
  "params": {}
}
```

所有 `params` 都必须是 JSON object，不使用数组参数。当前方法没有线程作用域要求，不需要传 `threadId`。

## mcp_server

### `mcpServer/add`

用途：把 MCP HTTP server 配置追加写入当前工作目录下的项目配置文件。

请求：

```json
{
  "mcpServers": {
    "my-search": {
      "transport": "http",
      "url": "https://your-domain.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `mcpServers` | object | 是 | key 是 server 名称，value 是 server 配置 |
| `transport` | string | 是 | 当前只支持 `"http"` |
| `url` | string | 是 | 必须是合法 `http://` 或 `https://` URL，且必须包含 host |
| `headers` | object | 否 | 请求头 key/value，key 和 value 都不能为 empty string |

成功响应：

```json
{
  "configPath": "D:\\Super-Dolphin\\.agent\\mcp_server\\config.json",
  "serverNames": ["my-search"]
}
```

行为说明：

- 写入路径固定为 `<当前工作目录>/.agent/mcp_server/config.json`。
- 配置文件不存在时会自动创建。
- 会保留已有 `mcpServers` 并追加新 server。
- 如果新增 server 名称已经存在，会返回冲突错误。
- server 名称不能为空，也不能依赖前后空格自动修正。

### `mcpServer/list`

用途：读取当前项目的 MCP server 配置。

请求：

```json
{}
```

成功响应：

```json
{
  "configPath": "D:\\Super-Dolphin\\.agent\\mcp_server\\config.json",
  "mcpServers": {
    "my-search": {
      "transport": "http",
      "url": "https://your-domain.com/mcp",
      "headers": {
        "Authorization": "Bearer YOUR_API_KEY"
      }
    }
  }
}
```

行为说明：

- 会从当前工作目录向上查找已存在的 `.agent/mcp_server/config.json`。
- 如果找不到配置文件，返回当前工作目录对应的 `configPath` 和空 `mcpServers`。
- 如果配置文件存在但 JSON 非法、缺少 `mcpServers`、或已有 server 配置非法，会返回参数错误。

## datasource

### `datasource/upload`

用途：把本地 `.txt` 或 `.pdf` 文件复制到当前工作目录的数据源上传目录，解析正文，并写入数据库中的 datasource 文档表。

请求：

```json
{
  "sourcePath": "D:\\Downloads\\notes.txt"
}
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `sourcePath` | string | 是 | 必须是本机绝对路径，目标必须是普通文件 |

成功响应：

```json
{
  "name": "notes.txt",
  "extension": ".txt",
  "size": 128,
  "storedPath": "D:\\Super-Dolphin\\.agent\\datasources\\uploads\\notes.txt"
}
```

行为说明：

- 只允许上传 `.txt` 和 `.pdf`，扩展名按小写判断。
- 文件会复制到 `<当前工作目录>/.agent/datasources/uploads/<原文件名>`。
- 如果上传目录不存在，会自动创建。
- 如果同名目标文件已经存在，会用新文件覆盖旧文件。
- `.txt` 会直接读取文本内容。
- `.pdf` 会从 PDF content stream 中提取可见文本；支持普通 literal string、hex string 和常见 `FlateDecode` 压缩流。
- 如果 PDF 是纯扫描件或无法提取正文，会返回参数错误，不会写入 datasource 文档记录。
- 解析出的正文会写入 `public.datasource_documents`；表不存在时会在首次写入前懒创建。

### `datasource/list`

用途：列出当前工作目录数据源上传目录中的文件名。

请求：

```json
{}
```

成功响应：

```json
{
  "fileNames": ["manual.pdf", "notes.txt"]
}
```

行为说明：

- 读取路径为 `<当前工作目录>/.agent/datasources/uploads`。
- 只返回普通文件名，不返回子目录。
- 文件名按字典序排序。
- 上传目录不存在时会创建目录，并返回空列表。
- 该接口仍然只返回文件名；正文通过 prompt 动态段消费，不在此接口暴露。

### `datasource/delete`

用途：删除当前工作目录数据源上传目录中的指定文件，并删除对应数据库正文记录。

请求：

```json
{
  "fileName": "notes.txt"
}
```

字段说明：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `fileName` | string | 是 | 只能是普通文件名，不能是 `.` 或 `..`，不能包含路径分隔符或 Windows 盘符 |

成功响应：

```json
{
  "name": "notes.txt",
  "deleted": true
}
```

行为说明：

- 删除路径固定为 `<当前工作目录>/.agent/datasources/uploads/<fileName>`。
- 目标不存在时返回 not found。
- 目标存在但不是普通文件时返回参数错误。
- 不支持通过相对路径或子路径删除 uploads 目录外的文件。
- 如果 datasource 文档表不存在，删除数据库记录前会懒创建表；这是为了让删除路径在空库上也保持一致语义。

## 数据库存储

datasource 正文存储在 `public.datasource_documents`，由 datasource store 在首次 upsert/list/delete 时懒创建。

表结构：

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

字段语义：

| 字段 | 说明 |
| --- | --- |
| `workspace_root` | 上传发生时的当前工作目录，用于隔离不同项目 |
| `name` | 原始文件名，也是同一工作目录下 upsert 的唯一键之一 |
| `extension` | 小写扩展名，如 `.txt`、`.pdf` |
| `size_bytes` | 源文件大小 |
| `stored_path` | 复制后的本地文件路径 |
| `content` | 解析后的文本正文 |
| `created_at` / `updated_at` | 数据库维护的创建和更新时间 |

## Prompt 注入行为

`datasource` 注册了动态 prompt section provider，section 名称为 `datasource`。

当数据库中存在当前工作目录的 datasource 文档正文时，prompt 中优先注入正文：

```md
## datasource

Uploaded datasource file contents available in this workspace.

### notes.txt
first line
second line
```

如果数据库中没有正文记录，但上传目录里有文件，则保留旧的文件名提示：

```md
## datasource

Uploaded datasource files available in this workspace. Do not infer file contents from names alone.
- manual.pdf
- notes.txt
```

没有上传文件且没有正文记录时，不注入 datasource 段落。

## 错误响应

RPC 错误会按 JSON-RPC error 返回。常见 code：

| code | 含义 | 典型场景 |
| --- | --- | --- |
| `-31007` | invalid params | 缺少必填字段、路径不是绝对路径、扩展名不支持、文件名不安全、PDF 无可提取正文 |
| `-31003` | conflict | MCP server 已存在 |
| `-31001` | not found | 删除不存在的数据源文件 |
| `-31002` | invalid state | 服务未正确装配 |

示例错误：

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -31007,
    "message": "datasource: sourcePath is required"
  }
}
```

## 代码位置

- `internal/module/mcp_server/rpc.go`：注册 `mcpServer/add`、`mcpServer/list`。
- `internal/module/mcp_server/service.go`：MCP 配置读写、校验和落盘逻辑。
- `internal/module/datasource/rpc.go`：注册 `datasource/upload`、`datasource/list`、`datasource/delete`。
- `internal/module/datasource/service.go`：上传、列表、删除、解析和存储编排。
- `internal/module/datasource/store.go`：datasource 文档表懒创建与数据库读写。
- `internal/module/datasource/extract.go`：按文件类型分发正文提取。
- `internal/module/datasource/pdf_extract.go`：轻量 PDF 文本提取。
- `internal/module/datasource/prompt_provider.go`：datasource prompt 动态区渲染。
