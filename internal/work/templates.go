package work

import (
	"context"
	"fmt"
)

// Planner templates: starting trees a new Planner workspace is made with. Each is a goal,
// a plan, its phases in order (one leads to the next) and a few items in each, with the
// dependencies that matter, written as ordinary items so everything can be changed after.

// TemplateInfo is what the app offers.
type TemplateInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	About string `json:"about"`
}

type tnode struct {
	key, kind, title, body string
	stage                  string
	kids                   []tnode
}

type template struct {
	info  TemplateInfo
	root  tnode
	links [][3]string // from key, rel, to key
}

var plannerTemplates = []template{
	{
		info: TemplateInfo{ID: "software", Label: "Software product", About: "A goal, a plan in phases (discovery, build, launch), features, tasks, a decision and a risk."},
		root: tnode{key: "goal", kind: "goal", title: "Ship version 1", body: "## Why\nWhat changes for the people who use it.\n\n## Measure\nHow we will know it worked.\n", kids: []tnode{
			{key: "plan", kind: "plan", title: "Version 1 plan", kids: []tnode{
				{key: "discover", kind: "phase", title: "Discovery", stage: "active", kids: []tnode{
					{key: "interviews", kind: "exploration", title: "Talk to five users"},
					{key: "pricing", kind: "decision", title: "Pricing model", stage: "open"},
				}},
				{key: "build", kind: "phase", title: "Build", kids: []tnode{
					{key: "accounts", kind: "feature", title: "Accounts and sign-in", kids: []tnode{
						{key: "auth", kind: "task", title: "Sign-in with email"},
					}},
					{key: "core", kind: "feature", title: "The core workflow"},
					{key: "ci", kind: "task", title: "CI and deploys"},
				}},
				{key: "launch", kind: "phase", title: "Launch", kids: []tnode{
					{key: "beta", kind: "milestone", title: "Public beta"},
					{key: "docs", kind: "task", title: "Getting-started docs"},
					{key: "scale", kind: "risk", title: "The database under load", stage: "open"},
				}},
			}},
		}},
		links: [][3]string{{"discover", "leads-to", "build"}, {"build", "leads-to", "launch"}, {"core", "depends-on", "accounts"}, {"beta", "depends-on", "core"}, {"core", "depends-on", "pricing"}},
	},
	{
		info: TemplateInfo{ID: "book", Label: "Book", About: "From premise to a finished draft and revision: outline, draft, revise, with milestones and a decision."},
		root: tnode{key: "goal", kind: "goal", title: "Finish the first book", body: "## Why\nWhat the book is for.\n\n## Measure\nA finished, revised draft.\n", kids: []tnode{
			{key: "plan", kind: "plan", title: "Book plan", kids: []tnode{
				{key: "outline", kind: "phase", title: "Outline", stage: "active", kids: []tnode{
					{key: "premise", kind: "task", title: "Premise and pitch"},
					{key: "chapters", kind: "task", title: "Chapter outline"},
				}},
				{key: "draft", kind: "phase", title: "Draft", kids: []tnode{
					{key: "half", kind: "milestone", title: "Halfway"},
					{key: "first", kind: "milestone", title: "First draft done"},
					{key: "habit", kind: "task", title: "Write 500 words a day"},
				}},
				{key: "revise", kind: "phase", title: "Revise", kids: []tnode{
					{key: "readers", kind: "task", title: "Beta readers"},
					{key: "route", kind: "decision", title: "Agent or self-publish", stage: "open"},
					{key: "momentum", kind: "risk", title: "Losing momentum mid-draft", stage: "open"},
				}},
			}},
		}},
		links: [][3]string{{"outline", "leads-to", "draft"}, {"draft", "leads-to", "revise"}, {"first", "depends-on", "half"}, {"readers", "depends-on", "first"}},
	},
	{
		info: TemplateInfo{ID: "saas", Label: "SaaS launch", About: "Pre-launch, launch week and after: pages, channels, the launch day milestone, onboarding, risks."},
		root: tnode{key: "goal", kind: "goal", title: "Launch publicly", body: "## Why\nWho it is for and why now.\n\n## Measure\nSign-ups in the first month.\n", kids: []tnode{
			{key: "plan", kind: "plan", title: "Launch plan", kids: []tnode{
				{key: "pre", kind: "phase", title: "Pre-launch", stage: "active", kids: []tnode{
					{key: "waitlist", kind: "feature", title: "Waitlist page"},
					{key: "pricing", kind: "task", title: "Pricing page"},
					{key: "channels", kind: "exploration", title: "Which channels work"},
				}},
				{key: "week", kind: "phase", title: "Launch week", kids: []tnode{
					{key: "day", kind: "milestone", title: "Launch day"},
					{key: "post", kind: "task", title: "Announcement post"},
					{key: "free", kind: "decision", title: "Free tier or trial", stage: "open"},
				}},
				{key: "after", kind: "phase", title: "After", kids: []tnode{
					{key: "onboard", kind: "feature", title: "Onboarding emails"},
					{key: "support", kind: "risk", title: "Support load", stage: "open"},
				}},
			}},
		}},
		links: [][3]string{{"pre", "leads-to", "week"}, {"week", "leads-to", "after"}, {"day", "depends-on", "waitlist"}, {"day", "depends-on", "pricing"}, {"post", "depends-on", "day"}},
	},
}

// TemplateInfos is what the Planner offers.
func TemplateInfos() []TemplateInfo {
	out := make([]TemplateInfo, 0, len(plannerTemplates))
	for _, t := range plannerTemplates {
		out = append(out, t.info)
	}
	return out
}

// ApplyTemplate makes a template's items in a workspace: the tree (each item in its parent's
// children, in order), then the links between them.
func (s *Store) ApplyTemplate(ctx context.Context, ws, id string) error {
	var t *template
	for i := range plannerTemplates {
		if plannerTemplates[i].info.ID == id {
			t = &plannerTemplates[i]
		}
	}
	if t == nil {
		return fmt.Errorf("there is no template %q", id)
	}
	ids := map[string]string{}
	var make func(n tnode, parent string) error
	make = func(n tnode, parent string) error {
		made, err := s.MakeItem(ctx, ws, NewItem{Kind: n.kind, Title: n.title, Stage: n.stage, Parent: parent})
		if err != nil {
			return fmt.Errorf("%s: %w", n.title, err)
		}
		ids[n.key] = made
		if n.body != "" {
			d, ok, err := s.Detail(ctx, ws, made)
			if err == nil && ok {
				secs := ParseBody(n.body)
				set := map[string]string{}
				for _, sec := range secs.Sections {
					set[sec.Heading] = sec.Text
				}
				if _, err := s.EditItem(ctx, ws, made, Edit{BaseRev: d.Rev, Sections: set}); err != nil {
					return err
				}
			}
		}
		for _, k := range n.kids {
			if err := make(k, made); err != nil {
				return err
			}
		}
		return nil
	}
	if err := make(t.root, ""); err != nil {
		return err
	}
	byFrom := map[string][]Link{}
	for _, l := range t.links {
		byFrom[l[0]] = append(byFrom[l[0]], Link{Rel: l[1], To: ids[l[2]]})
	}
	for from, links := range byFrom {
		d, ok, err := s.Detail(ctx, ws, ids[from])
		if err != nil || !ok {
			return fmt.Errorf("item %s was not found after it was made", from)
		}
		ls := links
		if _, err := s.EditItem(ctx, ws, ids[from], Edit{BaseRev: d.Rev, Links: &ls}); err != nil {
			return err
		}
	}
	return nil
}
