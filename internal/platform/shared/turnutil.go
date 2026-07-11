package shared

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"

// IsRemoteTurnInput 判断输入是否来自远程 turn，保持 shared 包旧入口兼容。
func IsRemoteTurnInput(value string) bool { return util.IsRemoteTurnInput(value) }
