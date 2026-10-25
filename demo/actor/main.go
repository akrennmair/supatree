// Command actor stands in for the agent CLI (`claude`) in the demo sandbox. It
// plays a script — one per agent — that says things, waits for the human to
// type, runs shell commands, and makes real MCP tool calls through `supatree
// mcp`, so what the demo shows supatree doing it actually does.
//
// The script is $HOME/demo/actors/<who>.yml, where <who> is the PM's agent ID
// (pm, pm-docs) or <tree>.<agent> for a tree agent, falling back to
// default.yml. A script that runs out leaves the actor idle at its prompt.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

// Step is one beat of a script. Exactly one of its action fields is set.
type Step struct {
	Say         string         `yaml:"say,omitempty"`
	Think       string         `yaml:"think,omitempty"` // a duration
	Input       bool           `yaml:"input,omitempty"`
	Tool        string         `yaml:"tool,omitempty"`
	Args        map[string]any `yaml:"args,omitempty"`
	Lines       int            `yaml:"lines,omitempty"` // result lines shown; default 6
	Run         string         `yaml:"run,omitempty"`
	Sleep       string         `yaml:"sleep,omitempty"`
	WaitRequest bool           `yaml:"wait_request,omitempty"`
}

// Script is an actor's whole part.
type Script struct {
	Steps []Step `yaml:"steps"`
	// Idle is what the actor answers to anything typed once the script is done.
	Idle string `yaml:"idle,omitempty"`
}

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	orange = "\x1b[38;5;173m"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "mcp":
			// supatree init asks whether its MCP server is registered.
			if len(args) > 1 && args[1] == "list" {
				fmt.Println("supatree: supatree mcp - ✓ Connected")
			}
			return
		case "--version", "-v":
			fmt.Println("demo actor (stands in for claude)")
			return
		}
	}
	a := &actor{in: bufio.NewReader(os.Stdin), vars: map[string]string{}}
	defer a.close()
	a.banner()
	if p := prompt(args); p != "" {
		a.user(p)
	}
	script, err := load(who())
	if err != nil {
		a.fail(err)
	}
	for _, s := range script.Steps {
		if err := a.step(s); err != nil {
			a.fail(err)
		}
	}
	a.idle(script.Idle)
}

// who names the script: the PM's agent ID, or <tree>.<agent>.
func who() string {
	if os.Getenv("SUPATREE_PM") != "" {
		if n := os.Getenv("SUPATREE_PM_NAME"); n != "" && n != "pm" {
			return "pm-" + n
		}
		return "pm"
	}
	return os.Getenv("SUPATREE_NAME") + "." + os.Getenv("SUPATREE_AGENT")
}

func actorsDir() string {
	if d := os.Getenv("DEMO_ACTORS"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "demo", "actors")
}

func load(name string) (*Script, error) {
	for _, n := range []string{name, "default"} {
		data, err := os.ReadFile(filepath.Join(actorsDir(), n+".yml"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var s Script
		if err := yaml.Unmarshal(data, &s); err != nil {
			return nil, fmt.Errorf("%s.yml: %w", n, err)
		}
		return &s, nil
	}
	return &Script{}, nil
}

// prompt is the positional argument the launcher passed as the first turn
// (supatree's kickoff prompt), skipping the flags it passes before it.
func prompt(args []string) string {
	withValue := map[string]bool{"--session-id": true, "--resume": true, "--name": true, "--settings": true, "--model": true}
	for i := 0; i < len(args); i++ {
		switch {
		case withValue[args[i]]:
			i++
		case strings.HasPrefix(args[i], "-"):
		default:
			return args[i]
		}
	}
	return ""
}

type actor struct {
	in   *bufio.Reader
	mcp  *mcpClient
	vars map[string]string
}

func width() int {
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil && ws.Col > 20 {
		return int(ws.Col)
	}
	return 80
}

func (a *actor) banner() {
	// Clear what ran before us in the pane: nono's listing of the sandbox.
	fmt.Print("\x1b[2J\x1b[3J\x1b[H")
	name := who()
	if os.Getenv("SUPATREE_PM") == "" {
		name = os.Getenv("SUPATREE_AGENT") + " · " + os.Getenv("SUPATREE_NAME")
	}
	w := min(width()-2, 60)
	title := fmt.Sprintf(" ✻ %s", name)
	sub := " scripted demo agent"
	pad := func(s string) string { return s + strings.Repeat(" ", max(0, w-len([]rune(s)))) }
	fmt.Printf("%s╭%s╮%s\n", orange, strings.Repeat("─", w), reset)
	fmt.Printf("%s│%s%s%s%s│%s\n", orange, reset+bold, pad(title), reset, orange, reset)
	fmt.Printf("%s│%s%s%s%s│%s\n", orange, reset+dim, pad(sub), reset, orange, reset)
	fmt.Printf("%s╰%s╯%s\n\n", orange, strings.Repeat("─", w), reset)
}

var varRef = regexp.MustCompile(`\$\{(\w+)\}`)

// expand substitutes ${name} from earlier inputs and tool results, then from
// the environment.
func (a *actor) expand(s string) string {
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		k := varRef.FindStringSubmatch(m)[1]
		if v, ok := a.vars[k]; ok {
			return v
		}
		return os.Getenv(k)
	})
}

func (a *actor) expandAll(v any) any {
	switch t := v.(type) {
	case string:
		return a.expand(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = a.expandAll(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = a.expandAll(x)
		}
		return out
	}
	return v
}

func (a *actor) step(s Step) error {
	switch {
	case s.Say != "":
		a.say(a.expand(s.Say))
	case s.Think != "":
		d, err := time.ParseDuration(s.Think)
		if err != nil {
			return err
		}
		stop := spin("Thinking")
		time.Sleep(d)
		stop()
	case s.Input:
		a.vars["input"] = a.read()
	case s.Tool != "":
		args, _ := a.expandAll(s.Args).(map[string]any)
		// A failed call is shown and the script goes on, as an agent would.
		a.vars["result"], _ = a.call(s.Tool, args, s.Lines)
	case s.Run != "":
		// Never expanded: what was typed or returned reaches the shell only as
		// $input and $result, never as part of the command.
		a.run(s.Run, s.Lines)
	case s.Sleep != "":
		d, err := time.ParseDuration(s.Sleep)
		if err != nil {
			return err
		}
		time.Sleep(d)
	case s.WaitRequest:
		a.waitRequest(s.Lines)
	}
	return nil
}

// say streams text a few words at a time, the way a model's reply arrives.
func (a *actor) say(text string) {
	lines := wrap(text, width()-4)
	for i, line := range lines {
		lead := "  "
		if i == 0 {
			lead = "● "
		}
		fmt.Print(lead)
		for j, word := range strings.Fields(line) {
			if j > 0 {
				fmt.Print(" ")
			}
			fmt.Print(word)
			time.Sleep(25 * time.Millisecond)
		}
		fmt.Println()
	}
	fmt.Println()
}

func wrap(text string, w int) []string {
	var out []string
	for _, para := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			if line != "" && len([]rune(line))+1+len([]rune(word)) > w {
				out = append(out, line)
				line = word
				continue
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return out
}

func (a *actor) user(text string) {
	for i, line := range wrap(text, width()-4) {
		lead := "  "
		if i == 0 {
			lead = "> "
		}
		fmt.Printf("%s%s%s%s\n", dim, lead, line, reset)
	}
	fmt.Println()
}

// read waits for the human's turn: a line typed at the prompt.
func (a *actor) read() string {
	fmt.Printf("%s>%s ", bold, reset)
	line, err := a.in.ReadString('\n')
	if err != nil && line == "" {
		// No terminal left to read: stay up rather than close the pane.
		select {}
	}
	fmt.Println()
	return strings.TrimSpace(line)
}

func (a *actor) idle(reply string) {
	if reply == "" {
		reply = "That's the end of this scripted demo agent's part."
	}
	for {
		if a.read() != "" {
			a.say(reply)
		}
	}
}

func (a *actor) fail(err error) {
	fmt.Printf("%s● actor: %v%s\n\n", red, err, reset)
	a.idle("")
}

func (a *actor) close() {
	if a.mcp != nil {
		a.mcp.close()
	}
}

// spin shows a working indicator on the current line until stopped.
func spin(label string) func() {
	frames := []string{"·", "✢", "✳", "✶", "✻", "✽"}
	var done atomic.Bool
	finished := make(chan struct{})
	start := time.Now()
	go func() {
		defer close(finished)
		for i := 0; !done.Load(); i++ {
			fmt.Printf("\r%s%s %s… (%ds)%s\x1b[K", orange, frames[i%len(frames)], label, int(time.Since(start).Seconds()), reset)
			time.Sleep(120 * time.Millisecond)
		}
		fmt.Print("\r\x1b[K")
	}()
	return func() { done.Store(true); <-finished }
}

func describe(args map[string]any) string {
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := fmt.Sprint(args[k])
		if s, ok := args[k].(string); ok {
			if len([]rune(s)) > 40 {
				s = string([]rune(s)[:39]) + "…"
			}
			v = fmt.Sprintf("%q", s)
		}
		parts = append(parts, k+": "+v)
	}
	return strings.Join(parts, ", ")
}

// result prints the first lines of a tool's output under its call.
func result(out string, lines int, failed bool) {
	if lines == 0 {
		lines = 6
	}
	all := strings.Split(strings.TrimRight(out, "\n"), "\n")
	color := dim
	if failed {
		color = red
	}
	w := width() - 6
	for i, line := range all {
		if i == lines {
			fmt.Printf("     %s… +%d lines%s\n", dim, len(all)-lines, reset)
			break
		}
		if r := []rune(line); len(r) > w {
			line = string(r[:w-1]) + "…"
		}
		lead := "     "
		if i == 0 {
			lead = "  └  "
		}
		fmt.Printf("%s%s%s%s\n", lead, color, line, reset)
	}
	fmt.Println()
}

func (a *actor) call(tool string, args map[string]any, lines int) (string, error) {
	call := fmt.Sprintf("%s(%s)", tool, describe(args))
	if r := []rune(call); len(r) > width()-14 {
		call = string(r[:width()-15]) + "…"
	}
	fmt.Printf("%s● supatree - %s%s%s\n", green, reset+bold, call, reset)
	if a.mcp == nil {
		c, err := startMCP()
		if err != nil {
			result(err.Error(), lines, true)
			return "", err
		}
		a.mcp = c
	}
	stop := spin("Working")
	out, err := a.mcp.call(tool, args)
	stop()
	if err != nil {
		result(err.Error(), lines, true)
		return "", err
	}
	result(out, lines, false)
	return out, nil
}

func (a *actor) run(command string, lines int) {
	if r := []rune(command); len(r) > width()-10 {
		command = string(r[:width()-11]) + "…"
	}
	fmt.Printf("%s● %sBash%s(%s)\n", green, reset+bold, reset, command)
	stop := spin("Running")
	cmd := exec.CommandContext(context.Background(), "sh", "-c", command)
	cmd.Env = os.Environ()
	for k, v := range a.vars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	stop()
	result(string(out), lines, err != nil)
}

// waitRequest polls the PM's request queue until something arrives, the way
// a PM picks up a tree agent's reply.
func (a *actor) waitRequest(lines int) {
	stop := spin("Waiting for the team")
	if a.mcp == nil {
		c, err := startMCP()
		if err != nil {
			stop()
			result(err.Error(), lines, true)
			return
		}
		a.mcp = c
	}
	for {
		out, err := a.mcp.call("requests", map[string]any{})
		if err == nil && !strings.HasPrefix(strings.TrimSpace(out), "no pending requests") {
			stop()
			fmt.Printf("%s● supatree - %srequests%s()\n", green, reset+bold, reset)
			result(out, lines, false)
			a.vars["result"] = out
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// mcpClient speaks MCP's stdio JSON-RPC to one `supatree mcp` child, which
// inherits this agent's environment and so its tools.
type mcpClient struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	id  int
}

func startMCP() (*mcpClient, error) {
	cmd := exec.CommandContext(context.Background(), "supatree", "mcp")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start supatree mcp: %w", err)
	}
	c := &mcpClient{cmd: cmd, in: in, out: bufio.NewReader(out)}
	if _, err := c.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "demo-actor", "version": "0"},
	}); err != nil {
		c.close()
		return nil, err
	}
	if err := c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *mcpClient) send(msg map[string]any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.in.Write(append(data, '\n'))
	return err
}

func (c *mcpClient) request(method string, params any) (json.RawMessage, error) {
	c.id++
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		line, err := c.out.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("supatree mcp: %w", err)
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &resp) != nil || resp.ID != c.id {
			continue
		}
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *mcpClient) call(tool string, args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.request("tools/call", map[string]any{"name": tool, "arguments": args})
	if err != nil {
		return "", err
	}
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	if res.IsError {
		return "", errors.New(b.String())
	}
	return b.String(), nil
}

func (c *mcpClient) close() {
	_ = c.in.Close()
	_ = c.cmd.Wait()
}
