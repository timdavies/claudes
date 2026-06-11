// Package cost estimates how much a Claude Code session has cost by summing the
// token usage recorded in its transcript JSONL and multiplying by per-model
// pricing. Claude Code doesn't persist a dollar figure in the transcript (only
// token counts), so the number here is an estimate — accurate as long as the
// pricing table below tracks current public rates.
//
// The transcript for a session lives at a deterministic path:
//
//	~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl
//
// where <encoded-cwd> is the working directory with every non-alphanumeric rune
// replaced by '-'. claudes assigns the session UUID at spawn (claude
// --session-id), so it always knows exactly which file to read — no guessing by
// recency, no SessionStart hook required.
package cost

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// rate is per-million-token pricing in USD for one model tier.
type rate struct {
	in, out, cacheWrite, cacheRead float64
}

// pricing maps a model tier (matched as a substring of the model id) to its
// rate. Public list prices as of 2026; update here when they change. Order
// matters only in that the first substring match wins, so keep tiers distinct.
var pricing = map[string]rate{
	"opus":   {in: 15, out: 75, cacheWrite: 18.75, cacheRead: 1.50},
	"sonnet": {in: 3, out: 15, cacheWrite: 3.75, cacheRead: 0.30},
	"haiku":  {in: 0.80, out: 4, cacheWrite: 1.00, cacheRead: 0.08},
}

// rateFor returns the rate for a model id, falling back to opus (the default
// model) when the id is empty or unrecognized.
func rateFor(model string) rate {
	for tier, r := range pricing {
		if strings.Contains(model, tier) {
			return r
		}
	}
	return pricing["opus"]
}

// transcriptLine is the slice of a transcript record we care about: the usage
// block on assistant messages.
type transcriptLine struct {
	Message struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens         int `json:"input_tokens"`
			OutputTokens        int `json:"output_tokens"`
			CacheCreationTokens int `json:"cache_creation_input_tokens"`
			CacheReadTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// EncodeDir maps a working directory to the project-dir name Claude Code uses
// under ~/.claude/projects: every rune that isn't a letter or digit becomes
// '-'. Consecutive separators are preserved (not collapsed), matching observed
// behavior (e.g. "/x/.claude" -> "-x--claude").
func EncodeDir(dir string) string {
	var b strings.Builder
	b.Grow(len(dir))
	for _, r := range dir {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// TranscriptPath returns the JSONL path for a session, or "" if either input is
// empty or the home dir can't be resolved.
func TranscriptPath(dir, sessionID string) string {
	if dir == "" || sessionID == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects", EncodeDir(dir), sessionID+".jsonl")
}

// SessionUSD parses the transcript and returns the estimated total cost in USD.
// A missing transcript (session hasn't written one yet) returns (0, nil) — not
// an error — so callers can treat "no cost yet" the same as "$0.00".
func SessionUSD(dir, sessionID string) (float64, error) {
	path := TranscriptPath(dir, sessionID)
	if path == "" {
		return 0, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()
	return usdFromReader(f)
}

// usdFromReader sums cost over the JSONL records in r. Split out from SessionUSD
// so it can be tested without touching the filesystem layout.
func usdFromReader(r io.Reader) (float64, error) {
	var total float64
	sc := bufio.NewScanner(r)
	// Transcript lines can be long (full tool results); raise the limit well
	// past bufio's 64KB default so we don't silently drop usage on big turns.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var line transcriptLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		u := line.Message.Usage
		if u == nil {
			continue
		}
		r := rateFor(line.Message.Model)
		total += float64(u.InputTokens)*r.in +
			float64(u.OutputTokens)*r.out +
			float64(u.CacheCreationTokens)*r.cacheWrite +
			float64(u.CacheReadTokens)*r.cacheRead
	}
	return total / 1e6, sc.Err()
}
