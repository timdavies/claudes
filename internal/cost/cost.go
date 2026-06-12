// Package cost reports per-session cost by shelling out to ccusage
// (https://github.com/ryoppippi/ccusage), which reads the same transcript JSONL
// Claude Code writes and applies maintained per-model pricing. Its per-session
// `totalCost` matches Claude Code's own billing to the cent, so claudes doesn't
// carry its own pricing table to drift out of date.
//
// ccusage keys each session by `period`, which is the Claude Code session UUID
// — the same value claudes stamps as CLAUDES_SESSION_ID at spawn — so mapping a
// cost back to a tmux session is a direct lookup.
package cost

import (
	"encoding/json"
	"os/exec"
)

// ccusageOutput is the slice of `ccusage session --json` we consume.
type ccusageOutput struct {
	Session []struct {
		Period    string  `json:"period"` // Claude Code session UUID
		TotalCost float64 `json:"totalCost"`
	} `json:"session"`
}

// SessionCosts returns a map of Claude Code session UUID → estimated total cost
// in USD. It returns an empty map (not an error) when ccusage isn't installed,
// so cost tracking degrades to "no cost shown" rather than breaking the daemon
// tick.
func SessionCosts() (map[string]float64, error) {
	out, err := runCCUsage()
	if err != nil {
		return map[string]float64{}, err
	}
	return parseCosts(out)
}

func parseCosts(out []byte) (map[string]float64, error) {
	var parsed ccusageOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		return map[string]float64{}, err
	}
	costs := make(map[string]float64, len(parsed.Session))
	for _, s := range parsed.Session {
		costs[s.Period] = s.TotalCost
	}
	return costs, nil
}

// runCCUsage invokes ccusage, preferring one on PATH and falling back to npx so
// it works whether or not the user has installed it globally. Returns a nil
// slice + error when neither is available.
func runCCUsage() ([]byte, error) {
	if path, err := exec.LookPath("ccusage"); err == nil {
		return exec.Command(path, "session", "--json").Output()
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		return nil, err
	}
	return exec.Command(npx, "-y", "ccusage", "session", "--json").Output()
}
