package multilsp

import "context"

func (m *manager) resolvePythonFormatterProductRoot(ctx context.Context) (string, error) {
	return resolvePythonFormatterProductRootPlatform(m, ctx)
}
