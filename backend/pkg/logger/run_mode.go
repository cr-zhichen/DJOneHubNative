package logger

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
)

var (
	goRunOnce sync.Once
	goRunMode bool
)

// IsGoRun 返回当前进程是否大概率由 `go run` 启动。
// 也支持通过 VOHIVE_FORCE_GO_RUN_LOG=true/false 手动覆盖判定结果。
func IsGoRun() bool {
	goRunOnce.Do(func() {
		goRunMode = detectGoRunMode()
	})
	return goRunMode
}

func detectGoRunMode() bool {
	if raw := strings.TrimSpace(os.Getenv("VOHIVE_FORCE_GO_RUN_LOG")); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			return v
		}
	}

	if exe, err := os.Executable(); err == nil {
		path := filepath.ToSlash(strings.ToLower(strings.TrimSpace(exe)))
		// `go run` 的临时二进制通常位于 .../go-build... 路径下。
		if strings.Contains(path, "/go-build") {
			return true
		}
	}

	if bi, ok := debug.ReadBuildInfo(); ok && bi != nil {
		// `go run` 默认以 command-line-arguments 作为主模块路径。
		if strings.TrimSpace(bi.Path) == "command-line-arguments" {
			return true
		}
	}

	return false
}
