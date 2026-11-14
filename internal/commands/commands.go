// Package commands is the list of command lines the assistant may run for the owner, and
// the runner for them. A command is named and fixed: a program, its arguments, and the
// parameters it takes, each checked against a pattern. Nothing is passed through a shell on
// this machine; on another host the words are quoted one by one. A command that only looks
// may be marked to run outright; one that changes something runs outright only when it says
// so and its guards (checks that must pass first) do, and otherwise asks the owner.
//
// The owner's rule: ask for anything destructive, disruptive or financial; otherwise follow
// the standing approvals, which is what this list is.
package commands

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Command is one named command line.
type Command struct {
	Name  string `json:"name"`
	About string `json:"about"`
	// Argv is the program and its arguments; "{name}" is replaced by a parameter's value.
	Argv []string `json:"argv,omitempty"`
	// Params are the parameters, each with the pattern its value must match in full.
	Params map[string]Param `json:"params,omitempty"`
	// Where it may run: "local" (this machine), "host" (another, over ssh) or "any".
	Where string `json:"where"`
	// Changes says it changes something (as against only looking).
	Changes bool `json:"changes,omitempty"`
	// Run is "outright" (no question) or "ask" (the owner is asked each time).
	Run string `json:"run"`
	// Guards are command lines that must succeed first, on the same machine, for a command
	// that changes something to run outright: the conditions it is safe under.
	Guards []Guard `json:"guards,omitempty"`
	// Builtin names something done here instead of a program ("secret").
	Builtin string `json:"builtin,omitempty"`
	// Off takes a built-in command out of the list (in the owner's file).
	Off bool `json:"off,omitempty"`
}

// Param is one parameter.
type Param struct {
	About   string `json:"about"`
	Pattern string `json:"pattern"`
	Default string `json:"default,omitempty"`
}

// Guard is a check that must pass before a changing command runs outright.
type Guard struct {
	About string   `json:"about"`
	Argv  []string `json:"argv"`
}

const (
	unit = `[A-Za-z0-9@._-]{1,80}`
	path = `~?/[A-Za-z0-9_./@-]{1,200}`
)

// builtin is the list that ships: host-neutral, mostly looking.
var builtin = []Command{
	{Name: "host-reachable", About: "Whether a host answers over ssh.", Argv: []string{"true"}, Where: "host", Run: "outright"},
	{Name: "host-uptime", About: "How long a host has been up, and its load.", Argv: []string{"uptime"}, Where: "any", Run: "outright"},
	{Name: "host-disk", About: "Disk space by filesystem.", Argv: []string{"df", "-h"}, Where: "any", Run: "outright"},
	{Name: "host-memory", About: "Memory in use (Linux).", Argv: []string{"free", "-h"}, Where: "host", Run: "outright"},
	{Name: "service-status", About: "A systemd service's state and its last lines.", Argv: []string{"systemctl", "status", "{unit}", "--no-pager", "-n", "15"}, Where: "host", Run: "outright",
		Params: map[string]Param{"unit": {About: "The unit's name.", Pattern: unit}}},
	{Name: "service-user-status", About: "A systemd user service's state and its last lines.", Argv: []string{"systemctl", "--user", "status", "{unit}", "--no-pager", "-n", "15"}, Where: "host", Run: "outright",
		Params: map[string]Param{"unit": {About: "The unit's name.", Pattern: unit}}},
	{Name: "service-log", About: "The last lines of a systemd service's log.", Argv: []string{"journalctl", "-u", "{unit}", "-n", "{lines}", "--no-pager"}, Where: "host", Run: "outright",
		Params: map[string]Param{"unit": {About: "The unit's name.", Pattern: unit}, "lines": {About: "How many lines.", Pattern: `[0-9]{1,3}`, Default: "80"}}},
	{Name: "service-restart", About: "Restart a systemd service. It interrupts whatever the service is doing.", Argv: []string{"sudo", "-n", "systemctl", "restart", "{unit}"}, Where: "host", Changes: true, Run: "ask",
		Params: map[string]Param{"unit": {About: "The unit's name.", Pattern: unit}}},
	{Name: "caddy-validate", About: "Check a Caddy configuration without applying it.", Argv: []string{"caddy", "validate", "--config", "{config}"}, Where: "any", Run: "outright",
		Params: map[string]Param{"config": {About: "The Caddyfile's path.", Pattern: path}}},
	{Name: "caddy-reload", About: "Apply a Caddy configuration without dropping connections. It is checked first.", Argv: []string{"caddy", "reload", "--config", "{config}"}, Where: "any", Changes: true, Run: "ask",
		Params: map[string]Param{"config": {About: "The Caddyfile's path.", Pattern: path}},
		Guards: []Guard{{About: "the configuration is valid", Argv: []string{"caddy", "validate", "--config", "{config}"}}}},
	{Name: "git-status", About: "A repository's branch and uncommitted changes.", Argv: []string{"git", "-C", "{repo}", "status", "--short", "--branch"}, Where: "any", Run: "outright",
		Params: map[string]Param{"repo": {About: "The repository's folder.", Pattern: path}}},
	{Name: "git-log", About: "A repository's recent commits.", Argv: []string{"git", "-C", "{repo}", "log", "--oneline", "-n", "{count}"}, Where: "any", Run: "outright",
		Params: map[string]Param{"repo": {About: "The repository's folder.", Pattern: path}, "count": {About: "How many.", Pattern: `[0-9]{1,2}`, Default: "15"}}},
	{Name: "git-fetch", About: "Fetch a repository's remotes. Nothing in the working tree changes.", Argv: []string{"git", "-C", "{repo}", "fetch", "--all", "--prune"}, Where: "any", Changes: true, Run: "outright",
		Params: map[string]Param{"repo": {About: "The repository's folder.", Pattern: path}}},
	{Name: "secret-generate", About: "Make a new random secret and keep it in a file of its own (never shown, never replacing one that exists). You are told the file's path, not the secret.", Builtin: "secret", Where: "local", Changes: true, Run: "outright",
		Params: map[string]Param{"name": {About: "A name for the file.", Pattern: `[a-z0-9][a-z0-9-]{0,63}`}, "bytes": {About: "How many random bytes.", Pattern: `(16|24|32|48|64)`, Default: "32"}}},
}

// List is the commands on offer: the built-in ones, changed and added to by the owner's file.
type List struct {
	// File is the owner's own list (JSON: {"commands": [...]}); it need not exist.
	File string
	// Home is the owner's home folder, for "~/" and for the secrets folder.
	Home string
}

// All is the list as it stands now. A broken file is reported and the built-in list stands.
func (l List) All() ([]Command, error) {
	byName := map[string]Command{}
	for _, c := range builtin {
		byName[c.Name] = c
	}
	var problem error
	if data, err := os.ReadFile(l.File); err == nil {
		var doc struct {
			Commands []Command `json:"commands"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			problem = fmt.Errorf("%s could not be read (%v); the built-in commands stand", l.File, err)
		} else {
			for _, c := range doc.Commands {
				if c.Off {
					delete(byName, c.Name)
					continue
				}
				if err := c.valid(); err != nil {
					problem = fmt.Errorf("%s: the command %q is not used: %v", l.File, c.Name, err)
					continue
				}
				byName[c.Name] = c
			}
		}
	}
	out := make([]Command, 0, len(byName))
	for _, c := range byName {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, problem
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,48}$`)

func (c Command) valid() error {
	if !nameRe.MatchString(c.Name) {
		return errors.New("its name is lowercase letters, digits and dashes")
	}
	if c.Builtin == "" && len(c.Argv) == 0 {
		return errors.New("it has no program")
	}
	if c.Where != "local" && c.Where != "host" && c.Where != "any" {
		return errors.New(`where is "local", "host" or "any"`)
	}
	if c.Run != "outright" && c.Run != "ask" {
		return errors.New(`run is "outright" or "ask"`)
	}
	for name, p := range c.Params {
		if _, err := regexp.Compile("^(?:" + p.Pattern + ")$"); err != nil || p.Pattern == "" {
			return fmt.Errorf("the parameter %q needs a pattern its value must match", name)
		}
	}
	return nil
}

// Outright says whether the command runs without asking.
func (c Command) Outright() bool { return c.Run == "outright" }

// Hosts are the ssh hosts the owner has named in their ssh config (no patterns).
func (l List) Hosts() []string {
	data, err := os.ReadFile(filepath.Join(l.Home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !strings.EqualFold(f[0], "Host") {
			continue
		}
		for _, h := range f[1:] {
			if strings.ContainsAny(h, "*?!") || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	sort.Strings(out)
	return out
}

// Result is what a run gave.
type Result struct {
	Command string `json:"command"`
	Host    string `json:"host,omitempty"`
	Line    string `json:"line"` // what was run, for the record
	Exit    int    `json:"exit"`
	Output  string `json:"output"`
	Cut     bool   `json:"output_cut,omitempty"`
	Took    string `json:"took"`
}

const (
	maxOutput = 24 << 10
	timeout   = 60 * time.Second
)

// fill puts the parameters into an argv, checking each against its pattern.
func (c Command) fill(argv []string, params map[string]string, home string) ([]string, error) {
	vals := map[string]string{}
	for name, p := range c.Params {
		v, ok := params[name]
		if !ok || v == "" {
			v = p.Default
		}
		if v == "" {
			return nil, fmt.Errorf("the parameter %q is needed: %s", name, p.About)
		}
		re, err := regexp.Compile("^(?:" + p.Pattern + ")$")
		if err != nil || !re.MatchString(v) || strings.Contains(v, "..") {
			return nil, fmt.Errorf("the value given for %q is not one this command takes", name)
		}
		vals[name] = v
	}
	for name := range params {
		if _, ok := c.Params[name]; !ok {
			return nil, fmt.Errorf("this command takes no parameter %q", name)
		}
	}
	out := make([]string, len(argv))
	for i, a := range argv {
		for name, v := range vals {
			a = strings.ReplaceAll(a, "{"+name+"}", v)
		}
		if strings.Contains(a, "{") {
			return nil, errors.New("the command names a parameter it does not define")
		}
		out[i] = a
	}
	return out, nil
}

func quote(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}

var hostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// exec runs one argv here or on a host and returns its output and exit status.
func (l List) exec(ctx context.Context, host string, argv []string) (out string, exit int, cut bool, err error) {
	var cmd *exec.Cmd
	if host == "" {
		for i, a := range argv {
			if rest, ok := strings.CutPrefix(a, "~/"); ok {
				argv[i] = filepath.Join(l.Home, rest)
			}
		}
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // argv comes from the named list, its values from checked parameters
	} else {
		// The remote shell reads one line: each word is quoted, and ~/ is left for it.
		line := quote(argv)
		line = strings.ReplaceAll(line, "'~/", "~/'")
		cmd = exec.CommandContext(ctx, "ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", host, "--", line) //nolint:gosec // see above
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	cmd.Stdin = nil
	runErr := cmd.Run()
	out = buf.String()
	if len(out) > maxOutput {
		out, cut = out[len(out)-maxOutput:], true
	}
	var ee *exec.ExitError
	switch {
	case runErr == nil:
	case errors.As(runErr, &ee):
		exit = ee.ExitCode()
	default:
		return out, -1, cut, runErr
	}
	return out, exit, cut, nil
}

// Run runs a named command. host is "" for this machine. asked says the owner was asked:
// without it a command runs only if it is marked outright, and one that changes something
// only if its guards pass.
func (l List) Run(ctx context.Context, name, host string, params map[string]string, asked bool) (Result, error) {
	all, _ := l.All()
	var c *Command
	for i := range all {
		if all[i].Name == name {
			c = &all[i]
		}
	}
	if c == nil {
		return Result{}, fmt.Errorf("there is no command named %q", name)
	}
	if !asked && !c.Outright() {
		return Result{}, fmt.Errorf("%q asks the owner each time", name)
	}
	switch {
	case host == "" && c.Where == "host":
		return Result{}, fmt.Errorf("%q runs on a host: say which", name)
	case host != "" && c.Where == "local":
		return Result{}, fmt.Errorf("%q runs on this machine only", name)
	}
	if host != "" {
		known := false
		for _, h := range l.Hosts() {
			known = known || h == host
		}
		if !hostRe.MatchString(host) || !known {
			return Result{}, fmt.Errorf("%q is not a host in the owner's ssh config", host)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	res := Result{Command: name, Host: host}
	if c.Builtin == "secret" {
		filled, err := c.fill([]string{"{name}", "{bytes}"}, params, l.Home)
		if err != nil {
			return res, err
		}
		file, err := l.secret(filled[0], filled[1])
		if err != nil {
			return res, err
		}
		res.Line, res.Output, res.Took = "secret-generate "+filled[0], "A new secret is in "+file+" (not shown). Refer to it by that path.", time.Since(started).Round(time.Millisecond).String()
		return res, nil
	}
	argv, err := c.fill(c.Argv, params, l.Home)
	if err != nil {
		return res, err
	}
	for _, g := range c.Guards {
		gv, err := c.fill(g.Argv, params, l.Home)
		if err != nil {
			return res, err
		}
		out, exit, _, err := l.exec(ctx, host, gv)
		if err != nil || exit != 0 {
			return res, fmt.Errorf("it was not run: the check that %s did not pass:\n%s", g.About, strings.TrimSpace(out))
		}
	}
	res.Line = strings.Join(argv, " ")
	out, exit, cut, err := l.exec(ctx, host, argv)
	if err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("it did not finish in %s", timeout)
		}
		return res, fmt.Errorf("it could not be run: %v", err)
	}
	res.Output, res.Exit, res.Cut, res.Took = out, exit, cut, time.Since(started).Round(time.Millisecond).String()
	return res, nil
}

// secret writes n random bytes, base64, to a new file under the owner's config folder.
func (l List) secret(name, n string) (string, error) {
	var size int
	if _, err := fmt.Sscan(n, &size); err != nil || size < 16 || size > 64 {
		return "", errors.New("bytes is 16 to 64")
	}
	dir := filepath.Join(l.Home, ".config", "basalisk", "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file := filepath.Join(dir, name)
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("a secret named %q already exists; it is not replaced. Pick another name", name)
		}
		return "", err
	}
	defer func() { _ = f.Close() }()
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	if _, err := f.WriteString(base64.RawURLEncoding.EncodeToString(raw) + "\n"); err != nil {
		return "", err
	}
	return file, nil
}
