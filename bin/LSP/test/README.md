# LSP 跨平台真实仓库样板

本目录保存 cmd/mcp-lsp 当前注册语言的真实上游源码快照，供 Windows、Linux 和
macOS 共用。目录不绑定操作系统或 CPU 架构；平台差异只属于 E2E 结果和语言服务器
安装证据。

每个一级子目录对应一个可独立路由的语言或语言族。SOURCES.md 冻结上游 URL 和
commit。快照不保留嵌套 .git，因此主仓库可以直接审查和追踪全部测试输入。

语言 ID 归并规则：

- go 同时覆盖 go、gomod、gosum、gowork。
- javascript、typescript、javascriptreact、typescriptreact 各自保留样板。
- mql 覆盖 mql、mql4、mql5、mq4、mq5、mqh。
- proto 覆盖运行时 proto 以及安装器识别的 protobuf、proto3。
- c、cpp、objective-c、objective-cpp 分别保留，虽然它们共用 clangd。

测试必须显式传入该语言目录作为 work_dir，并选择目录内的自然源码文件。不得在
测试时修改上游快照；需要编辑验证时复制到构建缓存中的临时目录。
