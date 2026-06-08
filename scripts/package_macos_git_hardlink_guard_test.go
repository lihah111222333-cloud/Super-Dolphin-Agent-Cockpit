package main

import "testing"

func TestPackageMacOSScriptPreservesGitCoreHardlinks(t *testing.T) {
	script := readScript(t, "package_macos.sh")
	copyBody := functionBody(t, script, "copy_packaged_git")
	writeBody := functionBody(t, script, "write_git_core_hardlink_manifest")
	restoreBody := functionBody(t, script, "restore_git_core_hardlinks")

	assertScriptContains(t, copyBody, "rsync -aH --delete \"$git_exec_path\"/ \"$resources/libexec/git-core\"/")
	assertScriptContains(t, copyBody, "rsync -aH --delete \"$git_share\"/ \"$resources/share/git-core\"/")
	assertScriptContains(t, copyBody, "write_git_core_hardlink_manifest \"$git_exec_path\" \"$resources\"")
	assertScriptContains(t, writeBody, "find \"$git_exec_path\" -type f -links +1 -print0")
	assertScriptContains(t, writeBody, "stat -f '%i' \"$src\"")
	assertScriptContains(t, restoreBody, "[[ -s \"$manifest\" ]] || return 0")
	assertScriptContains(t, restoreBody, "ln \"$canonical\" \"$path\"")
	assertScriptOrder(t, copyBody, "rsync -aH --delete \"$git_exec_path\"/", "write_git_core_hardlink_manifest \"$git_exec_path\" \"$resources\"")
	assertScriptOrder(t, script, "sign_macho_tree \"$codesign_identity\"", "restore_git_core_hardlinks \"$resources\"")
	assertScriptOrder(t, script, "restore_git_core_hardlinks \"$resources\"", "verify_postgres_runtime \"$resources/postgres/$platform\"")
}
