// Package explain is Nina explaining herself (the user, 2026-11-24): when the domains work together
// there will be inconsistencies, and the way to use them is to narrate the inference trace. The
// trace is what happened, from the log: each domain's lines (what a rule said when it fired), the
// director's routing between domains, the person's requests, the world's surprises, the tasks. The
// scripts are the Metis sources of the domains in the trace, each rule with its %% comment (its
// meaning) and what it takes and leaves. A model reads both and narrates the chain: what started
// it, which domain concluded what, how it was carried to the next, what was done; where the domains
// disagreed or something did not fit, what is not understood.
//
// Two intents, as Metis has them (the user, 2026-11-24): NARRATE, the trace told with the rules'
// comments (the REPL's and the loop's narrate): the last minutes, or, for "why did you do that?",
// the closest term in the trace and the chain that led to it; SURPRISES, the workspace's surprise
// channel (.metis/surprises/, workspace.go): what surprised the home and where the loop is with it.
package explain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	nlog "github.com/ibnHatab/nina-live/log"
)

// Explainer narrates the trace.
type Explainer struct {
	Log     *nlog.Log
	Root    string // the catalog (its domains' sources)
	Ask     func(ctx context.Context, system, prompt string) (string, error)
	Persona string     // Nina's character, before the narrator's instructions
	WS      *Workspace // the Metis loop's workspace (.metis): where surprises are dropped for the loop
	Now     func() float64
}

const System = `You explain what Nina's home did and why, from its rules and its trace.

The rules are Metis rules, grouped by domain: each has a name, its meaning (the comment, which is also the line the home says when it fires), and what it takes and leaves (before ⊸ after; a $ marks what it only reads). The director routes one domain's wish to another.

The trace lists what happened, oldest first: the person's requests (call), each domain's lines (what a rule said when it fired), the director's routing (route), the world's surprises (surprise), the tasks (task).

Explain the chain in the order it happened: what started it (a request, a reading, the clock), which domain concluded what, how the director carried it to another domain, what was done in the end. Where domains disagreed, a rule refused, or something surprised the home, say so plainly, and say what is not understood. Never invent a step the trace does not show; if the trace is too thin to answer, say what is missing.

Speak to the person: two to five short sentences, in the language of their question, in Nina's voice. No lists, no markup, no rule names with underscores (say what the rule does).`

// Line is one trace line.
type Line struct {
	At     float64
	Domain string
	Text   string
	Act    bool // something the home did (a domain's result or event) or was asked (a call)
}

// Trace is what happened since `since` (world time), as lines, and the domains it touched.
func (x *Explainer) Trace(ctx context.Context, since float64) ([]Line, []string, error) {
	var out []Line
	doms := map[string]bool{}
	err := x.Log.Read(ctx, 0, func(e nlog.Event) error {
		if e.At < since {
			return nil
		}
		var d map[string]any
		json.Unmarshal(e.Data, &d)
		s := func(k string) string { v, _ := d[k].(string); return v }
		switch e.Stream {
		case "reasoning":
			dom := e.Key
			if dom == "" {
				dom = "home"
			}
			doms[dom] = true
			out = append(out, Line{e.At, dom, fmt.Sprintf("%s (%s): %s", dom, e.Kind, firstNonEmpty(s("line"), s("text"))), e.Kind == "result" || e.Kind == "event"})
		case "call":
			args, _ := json.Marshal(d["args"])
			out = append(out, Line{e.At, e.Kind, fmt.Sprintf("call: the person asked %s %s", s("tool"), args), true})
			doms[e.Kind] = true
		case "tick":
			if e.Kind == "tick-relay" && d["what"] == "goal" {
				doms["director"] = true
				out = append(out, Line{e.At, "director", fmt.Sprintf("route: the director carried %v to %v", firstNonEmpty(fmt.Sprint(d["kind"]), fmt.Sprint(d["lines"])), d["domain"]), false})
			}
		case "surprise":
			doms[e.Key] = true
			b, _ := json.Marshal(d)
			out = append(out, Line{e.At, e.Key, fmt.Sprintf("surprise (%s): %s", e.Key, b), false})
		case "task":
			switch e.Kind {
			case "created", "done", "steer", "followup":
				b, _ := json.Marshal(d)
				out = append(out, Line{e.At, "task", fmt.Sprintf("task %s: %.300s", e.Kind, b), e.Kind == "done"})
			}
		case "habits":
			b, _ := json.Marshal(d)
			out = append(out, Line{e.At, "habits", fmt.Sprintf("habits %s: %.300s", e.Kind, b), false})
		}
		return nil
	}, "reasoning", "call", "tick", "surprise", "task", "habits")
	var ds []string
	for d := range doms {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	return out, ds, err
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" && x != "<nil>" {
			return x
		}
	}
	return ""
}

var (
	ruleRe    = regexp.MustCompile(`^\s*([a-z_][a-z0-9_]*)\s*\[[^\]]*\]\s*:\s*(.+?)\s*\.\s*$`)
	includeRe = regexp.MustCompile(`^include\s+"([^"]+)"`)
)

// Script is a domain's rules: name, meaning, before ⊸ after; its included bases too (the taste's
// lifted rules, the director's base), each file once.
func (x *Explainer) Script(domain string) string {
	dir := filepath.Join(x.Root, "domains", domain)
	main := filepath.Join(dir, domain+".llp")
	if domain == "director" {
		main = filepath.Join(dir, "director.llp")
	}
	var b strings.Builder
	seen := map[string]bool{}
	var read func(path string)
	read = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		src, err := os.ReadFile(path)
		if err != nil {
			return
		}
		doc := ""
		for _, l := range strings.Split(string(src), "\n") {
			t := strings.TrimSpace(l)
			if m := includeRe.FindStringSubmatch(t); m != nil {
				inc := filepath.Join(filepath.Dir(path), m[1])
				if !strings.Contains(inc, "shared") && !strings.Contains(inc, "iface") && !strings.HasSuffix(inc, "_which.llp") {
					read(inc)
				}
				continue
			}
			if strings.HasPrefix(t, "%%") {
				doc = strings.TrimSpace(strings.TrimPrefix(t, "%%"))
				continue
			}
			if m := ruleRe.FindStringSubmatch(l); m != nil {
				body := strings.ReplaceAll(m[2], " -o ", " ⊸ ")
				fmt.Fprintf(&b, "- %s: %q — %s\n", m[1], doc, body)
				doc = ""
			}
		}
	}
	read(main)
	if b.Len() == 0 {
		return ""
	}
	return "## " + domain + "\n" + b.String()
}

func (x *Explainer) now() float64 {
	if x.Now != nil {
		return x.Now()
	}
	return float64(time.Now().UnixNano()) / 1e9
}

// Explain answers the person's question about the last minutes (default 10).
func (x *Explainer) Explain(ctx context.Context, question string, minutes float64) (string, error) {
	if minutes <= 0 {
		minutes = 10
	}
	lines, doms, err := x.Trace(ctx, x.now()-minutes*60)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "Nothing happened in the home in the last few minutes that I could explain.", nil
	}
	return x.narrate(ctx, question, lines, doms)
}

// Why answers "why did you do that?": the closest act in the trace (the last thing the home did, in
// the last 15 minutes) and the chain that led to it: back to the request that started it, or two
// minutes before it.
func (x *Explainer) Why(ctx context.Context, question string) (string, error) {
	lines, _, err := x.Trace(ctx, x.now()-15*60)
	if err != nil {
		return "", err
	}
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Act {
			last = i
			break
		}
	}
	if last < 0 {
		return "I haven't done anything in the last quarter of an hour that I could explain.", nil
	}
	start := last
	for j := last; j >= 0 && lines[j].At >= lines[last].At-120; j-- {
		start = j
		if strings.HasPrefix(lines[j].Text, "call:") {
			break
		}
	}
	chain := lines[start : last+1]
	doms := map[string]bool{}
	for _, l := range chain {
		doms[l.Domain] = true
	}
	var ds []string
	for d := range doms {
		ds = append(ds, d)
	}
	sort.Strings(ds)
	if strings.TrimSpace(question) == "" {
		question = "Why did you do that?"
	}
	return x.narrate(ctx, question+"\n\nExplain only the last line of the trace (the last thing the home did) and the chain that led to it: what started it, which rule concluded it and what that rule means. Nothing before it.", chain, ds)
}

// Surprises answers "what surprised you?" from the workspace's surprise channel: the surprises
// dropped there, the open ones (waiting for the loop), the taken ones (the loop is on them), the
// ruled ones with their verdict, told so the person knows where the rules need work.
func (x *Explainer) Surprises(ctx context.Context, question string) (string, error) {
	if x.WS == nil {
		return "I keep no surprises: no workspace is set.", nil
	}
	ss := x.WS.List()
	if len(ss) == 0 {
		return "Nothing has surprised me: every rule did what it expected.", nil
	}
	var p strings.Builder
	p.WriteString("# The surprises in the workspace (newest first)\n\n")
	for i, s := range ss {
		if i == 20 {
			break
		}
		rd, _ := json.Marshal(s.Reading)
		fmt.Fprintf(&p, "- %s, %s: status %s%s. What the runtime saw: %.300s. %s\n", s.TS, s.Member, s.Status,
			map[bool]string{true: " (verdict: " + s.Verdict + ")", false: ""}[s.Verdict != ""], rd, s.Words)
	}
	fmt.Fprintf(&p, "\nBy status: %v\n\n# The question\n\n%s\n", Counts(ss), firstNonEmpty(question, "What surprised you?"))
	sys := "You tell the person what surprised Nina's home: where a domain's rules did not expect what happened. For each, say the domain and what happened that the rules did not expect, in plain words; group the same surprise repeated. Then say how many wait for the loop (open), how many it is working on (taken) and what it ruled. Two to five short sentences, in the language of the question, in Nina's voice. No lists, no markup."
	if x.Persona != "" {
		sys = x.Persona + "\n\n# Your surprises\n\n" + sys
	}
	return x.Ask(ctx, sys, p.String())
}

// Surprise narrates a surprise with the trace that led to it (the last two minutes).
func (x *Explainer) Surprise(ctx context.Context, what string) (string, error) {
	lines, doms, err := x.Trace(ctx, x.now()-120)
	if err != nil {
		return "", err
	}
	return x.narrate(ctx, "Something surprised the home: "+what+". Say what was expected, what happened instead, and what is not understood.", lines, doms)
}

func (x *Explainer) narrate(ctx context.Context, question string, lines []Line, doms []string) (string, error) {
	if x.Ask == nil {
		return "", fmt.Errorf("no model to narrate with")
	}
	if len(lines) > 80 {
		lines = lines[len(lines)-80:]
	}
	var p strings.Builder
	p.WriteString("# The rules\n\n")
	for _, d := range doms {
		if s := x.Script(d); s != "" {
			p.WriteString(s + "\n")
		}
	}
	p.WriteString("# The trace\n\n")
	for _, l := range lines {
		fmt.Fprintf(&p, "%s %s\n", time.Unix(int64(l.At), 0).Format("15:04:05"), l.Text)
	}
	fmt.Fprintf(&p, "\n# The question\n\n%s\n", question)
	sys := System
	if x.Persona != "" {
		sys = x.Persona + "\n\n# Explaining yourself\n\n" + System
	}
	return x.Ask(ctx, sys, p.String())
}
