# mcp_server 与 datasource 接口使用说明

本文说明 `internal/module/mcp_server` 和 `internal/module/datasource` 当前暴露的请求方式、请求参数、成功响应、错误语义和落盘位置。

## 调用方式

这两组接口不是 REST 接口，而是桌面端后端注册的 JSON-RPC 方法。

后端 RPC 平台会把方法注册到 `/ws` WebSocket JSON-RPC 通道；前端代码中也可以通过通用封装调用：

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

所有 params 都必须是 JSON 对象，不使用数组参数。当前方法没有线程作用域要求，不需要传 `threadId`。

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
| `transport` | string | 是 | 目前只支持 `"http"` |
| `url` | string | 是 | 必须是合法 `http://` 或 `https://` URL，且必须包含 host |
| `headers` | object | 否 | 请求头 key/value；key 和 value 都不能为空 |

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

用途：把本地 `.txt` 或 `.pdf` 文件复制到当前工作目录的数据源上传目录。

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

- 只允许上传 `.txt` 和 `.pdf`，扩展名会按小写判断。
- 文件会复制到 `<当前工作目录>/.agent/datasources/uploads/<原文件名>`。
- 如果上传目录不存在，会自动创建。
- 如果同名目标文件已经存在，会返回冲突错误，不会覆盖。
- 复制失败时会清理未完成的目标文件。

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

### `datasource/delete`

用途：删除当前工作目录数据源上传目录中的指定文件。

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

## 错误响应

RPC 错误会按 JSON-RPC error 返回。常见 code：

| code | 含义 | 典型场景 |
| --- | --- | --- |
| `-31007` | invalid params | 缺少必填字段、路径不是绝对路径、扩展名不支持、URL 非法、文件名不安全 |
| `-31003` | conflict | MCP server 已存在、上传目标文件已存在 |
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

## Prompt 注入行为

`datasource` 还注册了一个动态 prompt section provider。

当上传目录中存在数据源文件时，会向 prompt 中注入类似内容：

```md
## datasource

Uploaded datasource files available in this workspace. Do not infer file contents from names alone.
- manual.pdf
- notes.txt
```

当没有上传文件时，不注入 datasource 段落。

## 代码位置

- `internal/module/mcp_server/rpc.go`：注册 `mcpServer/add`、`mcpServer/list`。
- `internal/module/mcp_server/service.go`：配置读写、校验和落盘逻辑。
- `internal/module/datasource/rpc.go`：注册 `datasource/upload`、`datasource/list`、`datasource/delete`。
- `internal/module/datasource/service.go`：上传、列表、删除和文件名校验逻辑。
- `internal/module/datasource/prompt_provider.go`：datasource prompt 动态区渲染。
