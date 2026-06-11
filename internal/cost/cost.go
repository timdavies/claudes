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

// rate is per-million-token base pricing in USD for one model tier. The cache
// rates are universal multiples of the input rate (cache read = 0.1×, 5-minute
// cache write = 1.25×, 1-hour cache write = 2×), so we only store input/output
// and derive the rest — this is how Anthropic prices every current tier.
type rate struct {
	in, out float64
}

func (r rate) cacheRead() float64    { return 0.10 * r.in }
func (r rate) cacheWrite5m() float64 { return 1.25 * r.in }
func (r rate) cacheWrite1h() float64 { return 2.00 * r.in }

// pricing maps a model tier (matched as a substring of the model id) to its
// base rate. Published list prices (platform.claude.com/docs/en/about-claude/pricing);
// update here when they change. First substring match wins, so keep tiers distinct.
var pricing = map[string]rate{
	"opus":   {in: 5, out: 25},
	"sonnet": {in: 3, out: 15},
	"haiku":  {in: 1, out: 5},
	"fable":  {in: 10, out: 50},
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
			// Breakdown of cache_creation_input_tokens by TTL; the two price
			// differently (5m = 1.25× input, 1h = 2× input). Absent on older
			// transcripts, where we fall back to the 5m rate.
			CacheCreation *struct {
				Ephemeral5m int `json:"ephemeral_5m_input_tokens"`
				Ephemeral1h int `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
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
	return filepath.Join(home, ".claude", "projects", EncodeDir(normalizeDir(dir)), sessionID+".jsonl")
}

// normalizeDir matches the path Claude Code encodes: symlinks resolved (macOS
// /var → /private/var) and the trailing slash stripped. EvalSymlinks is
// best-effort — a dir that no longer exists falls back to a cleaned path.
func normalizeDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
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
			float64(u.CacheReadTokens)*r.cacheRead()
		// Price cache creation by TTL when the breakdown is present; otherwise
		// charge the whole bucket at the 5-minute rate.
		if u.CacheCreation != nil {
			total += float64(u.CacheCreation.Ephemeral5m)*r.cacheWrite5m() +
				float64(u.CacheCreation.Ephemeral1h)*r.cacheWrite1h()
		} else {
			total += float64(u.CacheCreationTokens) * r.cacheWrite5m()
		}
	}
	return total / 1e6, sc.Err()
}
