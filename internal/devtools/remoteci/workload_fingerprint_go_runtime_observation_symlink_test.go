package remoteci

import (
	"crypto/sha1"
	"fmt"
)

func testExactGoTestDigestReplaceSymlink(snapshot *remoteGitTreeSnapshot, filePath, target string) {
	sum := sha1.Sum([]byte(target))
	entry := remoteGitTreeEntry{mode: "120000", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
	snapshot.byPath[filePath] = entry
	snapshot.rememberRemoteGitBlob(entry.objectID, []byte(target))
	for index, candidate := range snapshot.entries {
		if candidate.path == filePath {
			snapshot.entries[index] = entry
			delete(snapshot.goSources, filePath)
			return
		}
	}
	snapshot.entries = append(snapshot.entries, entry)
}
