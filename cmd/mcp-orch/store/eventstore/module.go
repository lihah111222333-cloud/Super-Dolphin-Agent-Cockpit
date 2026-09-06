package eventstore

import (
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"go.uber.org/fx"
)

// Module 注册 canonical event owner、只读身份验证器和 FenceVerifier 窄接口。
var Module = fx.Module("store.eventstore",
	fx.Provide(
		ProvideIdentityReader,
		NewIdentityFenceVerifier,
		ProvideFenceVerifier,
		NewStore,
	),
)

// ProvideIdentityReader 将 identity owner 收窄为只读身份验证端口。
func ProvideIdentityReader(store *identity.Store) IdentityReader { return store }

// ProvideFenceVerifier 把具体身份验证器收窄为 event owner 的只读端口。
func ProvideFenceVerifier(verifier *IdentityFenceVerifier) FenceVerifier { return verifier }
