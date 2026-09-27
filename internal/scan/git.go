package scan

import (
	"context"
	"os/exec"
	"time"
)

// runGit runs git in dir and returns its standard output. A variable so tests
// can make git fail without uninstalling it.
var runGit = func(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
}
