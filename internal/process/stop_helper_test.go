package process

import "runtime"

func isWindows() bool { return runtime.GOOS == "windows" }
