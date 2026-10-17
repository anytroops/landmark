// Package runinfo records where a benchmark or build ran, so every committed
// result traces to a machine and a commit.
package runinfo

import (
	"fmt"
	"os/exec"
	"regexp"
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
	power, battery := parsePower(sh("pmset", "-g", "batt"))
	return map[string]string{
		"os":     runtime.GOOS + "/" + runtime.GOARCH,
		"cpu":    sh("sysctl", "-n", "machdep.cpu.brand_string"),
		"cores":  fmt.Sprint(runtime.NumCPU()),
		"go":     runtime.Version(),
		"commit": sh("git", "-C", root, "rev-parse", "--short", "HEAD"),
		"dirty":  fmt.Sprint(dirty != ""),
		// macOS throttles hard on battery; timings taken there are not comparable.
		"power": power,
		// Charging shares the adapter with the CPU, so record it too.
		"battery": battery,
	}
}

var batteryRe = regexp.MustCompile(`(\d+)%; ([^;]+);`)

// parsePower reads `pmset -g batt` output: the power source ("ac",
// "battery", or "unknown") and the battery's charge and state, such as
// "100%; charged" ("none" without a battery).
func parsePower(out string) (power, battery string) {
	switch {
	case strings.Contains(out, "AC Power"):
		power = "ac"
	case strings.Contains(out, "Battery Power"):
		power = "battery"
	default:
		power = "unknown"
	}
	battery = "none"
	if m := batteryRe.FindStringSubmatch(out); m != nil {
		battery = m[1] + "%; " + strings.TrimSpace(m[2])
	}
	return power, battery
}
