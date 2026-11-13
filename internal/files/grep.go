package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

// Content search: lines that contain q (case-insensitive, literal) in the files under a
// folder. ripgrep does it when the place has it (on this machine, or on the remote host over
// ssh); otherwise a bounded scan here, for local places only. Bounded: maxGrepMatches lines,
// 5 per file, files up to 2 MiB, grepTime.

const (
	maxGrepMatches = 200
	grepPerFile    = 5
	grepMaxFile    = 2 << 20
	grepTime       = 8 * time.Second
)

// Match is one line that matched.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// GrepResult is a content search's answer; Engine says which search ran.
type GrepResult struct {
	Matches   []Match `json:"matches"`
	Truncated bool    `json:"truncated"`
	Engine    string  `json:"engine"` // ripgrep | scan
}

type grepper interface {
	Grep(ctx context.Context, rel, q string) (GrepResult, error)
}

// Grep searches file contents under p.
func (m *Manager) Grep(ctx context.Context, root, p, q string) (GrepResult, error) {
	b, err := m.backend(root)
	if err != nil {
		return GrepResult{}, err
	}
	g, ok := b.(grepper)
	if !ok {
		return GrepResult{}, errors.New("this place cannot be searched")
	}
	rel, err := Clean(p)
	if err != nil {
		return GrepResult{}, err
	}
	q = strings.TrimSpace(q)
	if q == "" || len(q) > 200 || strings.ContainsAny(q, "\x00\n") {
		return GrepResult{Matches: []Match{}}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, grepTime)
	defer cancel()
	res, err := g.Grep(ctx, rel, q)
	if errors.Is(err, context.DeadlineExceeded) {
		res.Truncated, err = true, nil
	}
	if res.Matches == nil {
		res.Matches = []Match{}
	}
	return res, err
}

// rgArgs is the ripgrep command line for a literal, case-insensitive search of dir.
func rgArgs(q, dir string) []string {
	return []string{"--json", "--fixed-strings", "--ignore-case", "--max-count", "5", "--max-filesize", "2M",
		"--glob", "!node_modules", "--glob", "!.git", "--", q, dir}
}

// readRG reads ripgrep's JSON lines, mapping paths under base to root-relative ones, until
// maxGrepMatches. It reports whether it stopped early.
func readRG(r io.Reader, base string) ([]Match, bool) {
	out := []Match{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path  struct{ Text string } `json:"path"`
				Lines struct{ Text string } `json:"lines"`
				Line  int                   `json:"line_number"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "match" {
			continue
		}
		p := path.Clean(ev.Data.Path.Text)
		if p != base && !strings.HasPrefix(p, base+"/") {
			continue // never report outside the root
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, base), "/")
		out = append(out, Match{Path: rel, Line: ev.Data.Line, Text: trimLine(ev.Data.Lines.Text)})
		if len(out) >= maxGrepMatches {
			return out, true
		}
	}
	return out, false
}

func trimLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}

// rgPath finds ripgrep on this machine (a shell function or alias does not count).
func rgPath() string {
	for _, p := range []string{"rg", "/opt/homebrew/bin/rg", "/usr/local/bin/rg", "/usr/bin/rg"} {
		if found, err := exec.LookPath(p); err == nil {
			return found
		}
	}
	return ""
}

func (l *local) Grep(ctx context.Context, rel, q string) (GrepResult, error) {
	e, err := l.stat(rel)
	if err != nil {
		return GrepResult{}, err
	}
	if !e.Dir {
		return GrepResult{}, ErrNotDir
	}
	if rg := rgPath(); rg != "" {
		dir := filepath.Join(l.dir, filepath.FromSlash(rel))
		cmd := exec.CommandContext(ctx, rg, rgArgs(q, dir)...) //nolint:gosec // fixed argv; q is one argument after --
		out, err := cmd.StdoutPipe()
		if err == nil && cmd.Start() == nil {
			matches, cut := readRG(out, l.dir)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return GrepResult{Matches: matches, Truncated: cut, Engine: "ripgrep"}, ctx.Err()
		}
	}
	return l.scan(ctx, rel, q)
}

// scan is the search without ripgrep: text files only (no NUL in the first 8 KiB).
func (l *local) scan(ctx context.Context, rel, q string) (GrepResult, error) {
	res := GrepResult{Matches: []Match{}, Engine: "scan"}
	needle := []byte(strings.ToLower(q))
	err := l.Walk(ctx, rel, func(e Entry) bool {
		if e.Dir {
			return !skipDirs[e.Name]
		}
		if len(res.Matches) >= maxGrepMatches {
			res.Truncated = true
			return false
		}
		if e.Symlink || e.Size > grepMaxFile || e.Size == 0 {
			return true
		}
		b, err := fs.ReadFile(l.root.FS(), e.Path)
		if err != nil || bytes.IndexByte(b[:min(len(b), 8192)], 0) >= 0 {
			return true
		}
		n := 0
		for i, line := range bytes.Split(b, []byte("\n")) {
			if bytes.Contains(bytes.ToLower(line), needle) {
				res.Matches = append(res.Matches, Match{Path: e.Path, Line: i + 1, Text: trimLine(string(line))})
				if n++; n >= grepPerFile || len(res.Matches) >= maxGrepMatches {
					break
				}
			}
		}
		return true
	})
	return res, err
}

// shQuote quotes one argument for the remote shell ssh hands the command to.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Grep on a remote place runs ripgrep on the host over ssh (the same ssh setup as the place).
func (r *remote) Grep(ctx context.Context, rel, q string) (GrepResult, error) {
	var dir, base string
	err := r.do(func(c *sftp.Client, b string) error {
		if err := check(c, b, rel, false); err != nil {
			return err
		}
		base = b
		dir = abs(b, rel)
		return nil
	})
	if err != nil {
		return GrepResult{}, err
	}
	if r.dial != nil {
		return GrepResult{}, errors.New("content search needs ripgrep on the host") // tests: no ssh
	}
	quoted := make([]string, 0, 12)
	for _, a := range append([]string{"rg"}, rgArgs(q, dir)...) {
		quoted = append(quoted, shQuote(a))
	}
	cmd := exec.CommandContext(ctx, sshPath(), "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "--", r.host, strings.Join(quoted, " ")) //nolint:gosec // the host is checked config; every remote argument is quoted
	out, err := cmd.StdoutPipe()
	if err != nil {
		return GrepResult{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err = cmd.Start(); err != nil {
		return GrepResult{}, ErrRemote
	}
	matches, cut := readRG(out, base)
	_ = cmd.Process.Kill()
	waitErr := cmd.Wait()
	if len(matches) == 0 && strings.Contains(stderr.String(), "not found") {
		return GrepResult{}, errors.New("ripgrep is not installed on " + r.host)
	}
	_ = waitErr // rg exits 1 when nothing matched
	return GrepResult{Matches: matches, Truncated: cut, Engine: "ripgrep"}, ctx.Err()
}
