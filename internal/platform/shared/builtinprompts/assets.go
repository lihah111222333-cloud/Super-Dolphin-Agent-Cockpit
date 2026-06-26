package builtinprompts

import "embed"

// embeddedAssets 包含内置 prompt manifest、模板配置和 section 正文。
//
//go:embed assets/**
var embeddedAssets embed.FS
