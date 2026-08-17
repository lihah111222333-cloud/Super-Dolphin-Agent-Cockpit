//go:build windows

package installer

// RemoveWindowsInstallerTreeChecked 在 Windows 上递归删除目标前，重验目标位于
// 受信根内且整棵树不含 symlink、junction 或其他 reparse point。调用方仍须先
// 对受信根本身施加自己的产品/前缀权限边界；本函数不会放宽可删除范围。
func RemoveWindowsInstallerTreeChecked(root, target string) error {
	return removeWindowsInstallerAllChecked(root, target)
}
