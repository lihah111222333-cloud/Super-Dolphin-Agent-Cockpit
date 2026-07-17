//go:build !unix

package schema

func runBlockingFilesystemWorkerFixture() bool {
	return false
}
