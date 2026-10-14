// Package runinfo records where a benchmark or build ran, so every committed
// result traces to a machine and a commit.
package runinfo

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Machine describes the host and the git state of the repo at root. "dirty"
// is true when tracked code or inputs differ from the commit; result folders
// are excluded because pipelines rewrite them.
func Machine(root string) map[string]string {
	sh := func(name string, args ...string) string {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			return "unknown"
		}
		return strings.TrimSpace(string(out))
	}
	dirty := sh("git", "-C", root, "status", "--porcelain", "--untracked-files=no", "--",
		".", ":(exclude)eval/results", ":(exclude)bench/results")
	return map[string]string{
		"os":     runtime.GOOS + "/" + runtime.GOARCH,
		"cpu":    sh("sysctl", "-n", "machdep.cpu.brand_string"),
		"cores":  fmt.Sprint(runtime.NumCPU()),
		"go":     runtime.Version(),
		"commit": sh("git", "-C", root, "rev-parse", "--short", "HEAD"),
		"dirty":  fmt.Sprint(dirty != ""),
	}
}
