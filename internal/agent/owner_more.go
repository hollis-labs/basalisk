package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/assist"
	"github.com/hollis-labs/basalisk/internal/inbox"
)

// OwnerDeps is what the assistant's wider tools need beyond the owner's mail.
type OwnerDeps struct {
	// Watch sorts the owner's mail; it holds their rules and what the assistant has asked.
	Watch *assist.Watcher
	// Link reaches Torque and the registry through the Tether gateway.
	Link interface {
		Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
	}
	// Tasks are the assistant's own background runs, newest first.
	Tasks func(ctx context.Context) []Task
	// Plans is the owner's plans from the Work engine (Content, Planner, Writer workspaces),
	// as a value to put in the overview. Nil: none.
	Plans func(ctx context.Context) any
	// Own is the assistant's own mailbox (its address, not the owner's), for asking other
	// seats something as itself. Nil: it has no address of its own.
	Own *inbox.Inbox
	// Seats is the owner's roster: the seats the assistant writes to. A name the assistant
	// gives is looked for there before the registry. Nil: no roster.
	Seats func(ctx context.Context) []Seat
	// DevAddress is who builds Basalisk: where a request for something configuration cannot
	// do is sent. Empty: the tool says it has nowhere to send it.
	DevAddress string
}

// moreOwnerTools are the assistant's tools beyond single messages: the state of everything
// at once, work handed to the seat with host power, the owner's mail rules, and a request
// to whoever builds Basalisk.
func moreOwnerTools(mail *inbox.Inbox, log *assist.Log, d OwnerDeps, record func(context.Context, assist.Entry) error, sent func(ctx context.Context, id, kind, about, to string)) []ServerTool {
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	out := func(v any) (string, error) {
		raw, err := json.Marshal(v)
		return string(raw), err
	}
	var tools []ServerTool

	tools = append(tools, ServerTool{
		Spec: ToolSpec{Name: "status_overview", Level: LevelRead,
			Description: "How everything stands, in one call, from the owner's own systems: their mail as sorted (what waits on them, answers that came, what is held, what you are handling), what you did in their name lately, your background runs, their plans (the Content, Planner and Writer workspaces: items by stage, the unfinished ones last touched, with dates), and the work in flight in Torque (how many tasks are doing, in review and blocked, the most recently touched, and the count by team). Use it first when the owner asks where things are, what is going on, or what needs them. It is a snapshot: for one piece of work go to the task (torque_task_get) or ask its seat (owner_status_ask), and say what you did not check.",
			InputSchema: obj(map[string]any{"team": str("Narrow the work in flight to one team or seat: a tag such as cell-mig-tachyon or seat-chief.")})},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			res := map[string]any{"as_of": time.Now().UTC().Format(time.RFC3339)}
			if d.Watch != nil {
				brief := func(class string) []map[string]any {
					list, _ := d.Watch.Open(ctx, class)
					rows := []map[string]any{}
					for i, m := range list {
						if i == 15 {
							break
						}
						name := m.FromName
						if name == "" {
							name = m.From
						}
						rows = append(rows, map[string]any{"from": name, "subject": clip(m.Subject, 120), "kind": m.Kind, "at": m.At, "account": m.Account, "id": m.ID})
					}
					return rows
				}
				held, _ := d.Watch.Open(ctx, assist.ClassDigest)
				res["mail"] = map[string]any{"waiting_on_the_owner": brief(assist.ClassNeeds), "answers_to_what_you_asked": brief(assist.ClassAnswer), "with_you": brief(assist.ClassBas), "held_count": len(held)}
			}
			if recent, err := log.Recent(ctx, 0, 12); err == nil {
				rows := []map[string]any{}
				for _, e := range recent {
					rows = append(rows, map[string]any{"at": e.At, "what": e.Summary})
				}
				res["done_in_the_owners_name"] = rows
			}
			if d.Tasks != nil {
				rows := []map[string]any{}
				for i, t := range d.Tasks(ctx) {
					if i == 10 {
						break
					}
					rows = append(rows, map[string]any{"title": t.Title, "state": t.State, "started_by": t.Origin, "id": t.ID})
				}
				res["your_background_runs"] = rows
			}
			if d.Plans != nil {
				if plans := d.Plans(ctx); plans != nil {
					res["plans"] = plans
				}
			}
			if d.Link != nil {
				args := map[string]any{"statuses": `["doing","review","blocked"]`, "limit": "200", "sort_by": "updated_at", "sort_dir": "desc", "include_total": "true"}
				if team, _ := in["team"].(string); team != "" {
					args["tags"] = `["` + strings.ReplaceAll(team, `"`, "") + `"]`
				}
				raw, err := d.Link.Call(ctx, "torque_task_list", args)
				if err != nil {
					res["work_in_flight"] = map[string]any{"problem": "Torque could not be read: " + err.Error()}
				} else {
					var page struct {
						Data struct {
							Items []struct {
								ID      string   `json:"id"`
								Title   string   `json:"title"`
								Status  string   `json:"status"`
								Tags    []string `json:"tags"`
								Updated string   `json:"updated_at"`
							} `json:"items"`
							Meta struct {
								Total int `json:"total"`
							} `json:"meta"`
						} `json:"data"`
					}
					_ = json.Unmarshal(raw, &page)
					byStatus, byTeam := map[string]int{}, map[string]int{}
					latest := []map[string]any{}
					for i, t := range page.Data.Items {
						byStatus[t.Status]++
						for _, tag := range t.Tags {
							if strings.HasPrefix(tag, "cell-") {
								byTeam[tag]++
							}
						}
						if i < 20 {
							latest = append(latest, map[string]any{"id": t.ID, "title": clip(t.Title, 110), "status": t.Status, "updated": t.Updated, "tags": t.Tags})
						}
					}
					res["work_in_flight"] = map[string]any{"total": page.Data.Meta.Total, "counted_here": len(page.Data.Items), "by_status": byStatus, "by_team": byTeam, "most_recently_touched": latest}
				}
			}
			return out(res)
		},
	})

	if d.Link != nil {
		tools = append(tools, ServerTool{
			Spec: ToolSpec{Name: "chief_delegate", Level: LevelWrite,
				Description: "Hand work to @chief, the seat on the owner's agent system with full power on the hosts (shell, sudo, ssh, services, deploys): anything at a command line or on a machine that you cannot do from this desktop. It makes a Torque task in the shape that seat works from and sends @chief a request naming it, in the owner's name. Write it so it can be done without asking: the outcome, where things are, what to touch and what to leave, how to tell it is done. Set risk honestly: ground (reversible, inside one service), elevated (a deploy, a restart, a merge), critical (data loss, money, credentials, hosts, network, security): critical work waits for the owner whatever you send. The owner is asked before it goes unless they chose Always.",
				InputSchema: obj(map[string]any{
					"title":      str("Imperative, under 70 characters."),
					"goal":       str("The outcome, in a sentence or two."),
					"context":    str("Why now, and where things are: hosts, paths, task ids, links."),
					"scope_in":   str("What to change."),
					"scope_out":  str("What not to touch."),
					"acceptance": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Checks someone else could run to see it is done."},
					"evidence":   str("What closes it: a command's output, a commit, a deploy event."),
					"risk":       map[string]any{"type": "string", "enum": []string{"ground", "elevated", "critical"}},
				}, "title", "goal", "context", "scope_in", "scope_out", "acceptance", "evidence", "risk")},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				get := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
				title := get("title")
				var checks []string
				if list, ok := in["acceptance"].([]any); ok {
					for _, c := range list {
						if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
							checks = append(checks, "- [ ] "+strings.TrimSpace(s))
						}
					}
				}
				if title == "" || get("goal") == "" || get("scope_out") == "" || len(checks) == 0 {
					return "", errors.New("a delegation needs a title, a goal, what not to touch, and at least one check")
				}
				p, account, ok := mail.First()
				if !ok {
					return "", errors.New("the owner has no address to send from")
				}
				chief, err := d.address(ctx, "chief")
				if err != nil {
					return "", err
				}
				risk := get("risk")
				tier, complexity := "standard", "standard"
				if risk != "ground" {
					tier, complexity = "frontier", "complex"
				}
				body := fmt.Sprintf("## Goal\n%s\n## Context\nDelegated by Chrispian's assistant (Basalisk) on his behalf, %s.\n%s\n## Scope\nIn: %s\nOut: %s\n## Acceptance\n%s\n## Evidence required\n%s\n## Classification\ncomplexity: %s\nrisk: %s\nreasoning: medium\ncontext: small\nmodel_tier: %s\n",
					get("goal"), time.Now().UTC().Format("2006-01-02"), get("context"), get("scope_in"), get("scope_out"), strings.Join(checks, "\n"), get("evidence"), complexity, risk, tier)
				if err := record(ctx, assist.Entry{Kind: "send", Summary: fmt.Sprintf("Delegated to @chief: “%s” (%s)", clip(title, 100), risk), With: chief, Asked: true, Detail: map[string]any{"subject": title, "body": clip(body, 4000)}}); err != nil {
					return "", err
				}
				meta, _ := json.Marshal(map[string]any{"classification": map[string]string{"complexity": complexity, "risk": risk, "reasoning": "medium", "context": "small", "model_tier": tier}, "delegated_by": "basalisk-assistant", "on_behalf_of": account})
				raw, err := d.Link.Call(ctx, "torque_task_create", map[string]any{"title": title, "description": body, "priority": "2", "tags": `["seat-chief","from-basalisk"]`, "metadata": string(meta)})
				if err != nil {
					return "", fmt.Errorf("the task could not be made in Torque: %w", err)
				}
				var made struct {
					Data struct {
						ID string `json:"ID"`
					} `json:"data"`
				}
				_ = json.Unmarshal(raw, &made)
				if made.Data.ID == "" {
					return "", errors.New("Torque did not say the new task's id")
				}
				extra := onBehalf(account)
				extra["kind"], extra["re"], extra["attention"], extra["awaiting"] = "handoff", "task:"+made.Data.ID, "agent", "@chief"
				msg := fmt.Sprintf("Task %s is for you: %s\n\n%s\n\nThe task has the scope, the checks and what counts as evidence. Risk: %s. Reply here when it is done or if it cannot be.", made.Data.ID, title, get("goal"), risk)
				id, err := p.Send(ctx, account, inbox.Outgoing{To: chief, Kind: "request", Subject: title, Body: msg, Extra: extra})
				if err != nil {
					return "", fmt.Errorf("task %s was made but @chief was not told: %w", made.Data.ID, err)
				}
				sent(ctx, id, "delegate", made.Data.ID+" "+title, chief)
				return out(map[string]any{"task": made.Data.ID, "sent": id, "to": "@chief", "note": "Its reply comes back to you in an <around> block."})
			},
		})
	}

	if d.Watch != nil {
		tools = append(tools,
			ServerTool{
				Spec: ToolSpec{Name: "mail_rules", Level: LevelRead,
					Description: "The owner's rules for sorting their mail, newest first (the first that matches a message wins): what each matches (a sender, a kind, words in the subject) and where it sends the mail: needs (shown to the owner), bas (handed to you), digest (held and counted), settle (marked read, on the record). With no rule a message is sorted by what it is.",
					InputSchema: obj(map[string]any{})},
				Run: func(ctx context.Context, _ map[string]any) (string, error) {
					rules, err := d.Watch.Rules(ctx)
					if err != nil {
						return "", err
					}
					return out(map[string]any{"rules": rules, "defaults": "asks for a decision or escalates: needs; a request without options: bas; anything else: digest"})
				},
			},
			ServerTool{
				Spec: ToolSpec{Name: "mail_rule_add", Level: LevelWrite,
					Description: "Add a rule to how the owner's mail is sorted, when they tell you how they want something handled (\"stop showing me deploy notices\", \"always tell me when Archie writes\"). Give at least one thing to match (from: the sender's address; kind: request, notice, status_update, response, handoff, escalation; subject_has: words in the subject) and the class: needs, bas, digest or settle. Say back to the owner what the rule does in a line. The owner is asked before it is added unless they chose Always.",
					InputSchema: obj(map[string]any{"from": str("The sender's address."), "kind": str("The kind of message."), "subject_has": str("Words the subject contains."),
						"class": map[string]any{"type": "string", "enum": []string{"needs", "bas", "digest", "settle"}}, "note": str("Why, in the owner's words.")}, "class")},
				Run: func(ctx context.Context, in map[string]any) (string, error) {
					get := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
					r, err := d.Watch.AddRule(ctx, assist.Rule{From: get("from"), Kind: get("kind"), SubjectHas: get("subject_has"), Class: get("class"), Note: get("note")})
					if err != nil {
						return "", err
					}
					return out(map[string]any{"added": r})
				},
			},
			ServerTool{
				Spec: ToolSpec{Name: "mail_rule_remove", Level: LevelWrite,
					Description: "Remove one of the owner's mail rules, by its id from mail_rules.",
					InputSchema: obj(map[string]any{"id": map[string]any{"type": "integer"}}, "id")},
				Run: func(ctx context.Context, in map[string]any) (string, error) {
					id, _ := in["id"].(float64)
					if id <= 0 {
						return "", errors.New("give the rule's id")
					}
					if err := d.Watch.DropRule(ctx, int64(id)); err != nil {
						return "", err
					}
					return "removed", nil
				},
			})
	}

	if d.Own != nil && d.Link != nil {
		tools = append(tools, ServerTool{
			Spec: ToolSpec{Name: "consult", Level: LevelAct,
				Description: "Ask another seat something as yourself, from your own address (not the owner's): the architect about a design, an app's lead about how its part works, a seat about what it meant. Give the seat by its key on the roster, or by a handle, name or address (tether_registry_search finds the ones not on the roster) and the question, with enough context to answer without asking back. It carries no authority: it is a question from the owner's assistant, and says so. It runs at once. The answer comes back to you in an <around> block with a later message; tell the owner you have asked and go on. To have something done, or to speak for the owner, use owner_send or chief_delegate instead.",
				InputSchema: obj(map[string]any{"seat": str("The seat's key on the roster, or a handle, name or address."), "subject": str("One line."), "question": str("The question, with the context needed to answer it."), "about": str("task:<id> or topic:<slug>, when there is one.")}, "seat", "subject", "question")},
			Run: func(ctx context.Context, in map[string]any) (string, error) {
				get := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
				if get("question") == "" {
					return "", errors.New("ask something")
				}
				to, err := d.address(ctx, get("seat"))
				if err != nil {
					return "", err
				}
				p, account, ok := d.Own.First()
				if !ok {
					return "", errors.New("you have no address of your own to ask from")
				}
				if err := record(ctx, assist.Entry{Kind: "consult", Summary: fmt.Sprintf("Asked %s: “%s”", get("seat"), clip(get("subject"), 100)), With: to, Detail: map[string]any{"subject": get("subject"), "body": clip(get("question"), 4000)}}); err != nil {
					return "", err
				}
				extra := map[string]any{"kind": "question", "attention": "agent", "asked_by": "Chrispian's assistant in Basalisk, as itself", "authority": "none: a question, not an instruction from Chrispian"}
				if re := get("about"); re != "" {
					extra["re"] = re
				}
				id, err := p.Send(ctx, account, inbox.Outgoing{To: to, Kind: "request", Subject: get("subject"), Body: get("question") + "\n\n(From Chrispian's assistant in Basalisk, asking as itself. This is a question, not an instruction from him.)", Extra: extra})
				if err != nil {
					return "", err
				}
				sent(ctx, id, "consult", get("subject"), to)
				return out(map[string]any{"asked": to, "sent": id, "note": "The answer comes back to you in an <around> block."})
			},
		})
	}

	tools = append(tools, ServerTool{
		Spec: ToolSpec{Name: "friction_log", Level: LevelAct,
			Description: "Write down process friction while it is fresh: something in how the work is done that got in the way (a step with no defined way to do it, a tool that was missing or answered oddly, a rule that did not fit, a seat that could not be reached, the same question needed twice). One entry per thing. It only records it, in the owner's Done for you list, for the two of you to go over later; nothing is sent. Minor friction: log it and go on. A dead end, an error you cannot get past, something you have no access to, or friction that changes the outcome: log it as major and also tell the owner now.",
			InputSchema: obj(map[string]any{
				"what":     str("What got in the way, in a line or two, with the specifics (the tool, the seat, the message)."),
				"doing":    str("What you were doing for the owner at the time."),
				"severity": map[string]any{"type": "string", "enum": []string{"minor", "major"}},
				"idea":     str("How it might be handled from now on, if you have one."),
			}, "what", "severity")},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			get := func(k string) string { s, _ := in[k].(string); return strings.TrimSpace(s) }
			what, severity := get("what"), get("severity")
			if what == "" {
				return "", errors.New("say what got in the way")
			}
			if severity != "major" {
				severity = "minor"
			}
			detail := map[string]any{"body": clip(what, 2000), "severity": severity}
			if v := get("doing"); v != "" {
				detail["about"] = clip(v, 400)
			}
			if v := get("idea"); v != "" {
				detail["idea"] = clip(v, 1000)
			}
			if err := record(ctx, assist.Entry{Kind: "friction", Summary: fmt.Sprintf("Friction (%s): %s", severity, clip(what, 140)), Detail: detail}); err != nil {
				return "", err
			}
			if severity == "major" {
				return "logged. It is major: tell the owner now, plainly, and ask how they want it handled.", nil
			}
			return "logged for later", nil
		},
	}, ServerTool{
		Spec: ToolSpec{Name: "dev_request", Level: LevelWrite,
			Description: "Ask whoever builds Basalisk for something you cannot do with your tools, your notes or the owner's rules: a new tool, a new kind of rule or filter, a fix to how you or an app behaves. Use it when the owner asks for a change to Basalisk itself, or when you keep being unable to do something they want. Say what is wanted, what happens today, and what done would look like. It is sent in the owner's name and recorded; the owner is asked first unless they chose Always.",
			InputSchema: obj(map[string]any{"title": str("One line."), "what": str("What is wanted, what happens today, what done looks like.")}, "title", "what")},
		Run: func(ctx context.Context, in map[string]any) (string, error) {
			if d.DevAddress == "" {
				return "", errors.New("no developer address is configured (basalisk.dev_address); tell the owner what is needed instead")
			}
			title := strings.TrimSpace(fmt.Sprint(in["title"]))
			what := strings.TrimSpace(fmt.Sprint(in["what"]))
			p, account, ok := mail.First()
			if !ok {
				return "", errors.New("the owner has no address to send from")
			}
			if err := record(ctx, assist.Entry{Kind: "send", Summary: "Asked the developer: “" + clip(title, 100) + "”", With: d.DevAddress, Asked: true, Detail: map[string]any{"subject": title, "body": clip(what, 4000)}}); err != nil {
				return "", err
			}
			extra := onBehalf(account)
			extra["kind"], extra["re"] = "request", "topic:basalisk-request"
			id, err := p.Send(ctx, account, inbox.Outgoing{To: d.DevAddress, Kind: "request", Subject: "Basalisk request: " + title, Body: what + "\n\n(Sent by Basalisk's assistant for Chrispian.)", Extra: extra})
			if err != nil {
				return "", err
			}
			sent(ctx, id, "dev_request", title, d.DevAddress)
			return out(map[string]any{"sent": id})
		},
	})
	return tools
}

// address is where a recipient the assistant named is reached: an address as it is, a seat
// on the owner's roster by its key or name, and otherwise a seat found in the registry.
func (d OwnerDeps) address(ctx context.Context, to string) (string, error) {
	to = strings.TrimSpace(to)
	if strings.HasPrefix(to, "msg://") {
		return to, nil
	}
	if to == "" {
		return "", errors.New("say who it goes to: a seat on the roster, or an address")
	}
	if d.Seats != nil {
		if seat, ok := FindSeat(d.Seats(ctx), to); ok {
			return seat.Address, nil
		}
	}
	if d.Link == nil {
		return "", fmt.Errorf("%q is not on the roster and is not an address (msg://…)", to)
	}
	return seatAddress(ctx, d.Link, strings.TrimPrefix(to, "@"))
}

// seatAddress finds a seat's address by its handle, through the registry.
func seatAddress(ctx context.Context, link interface {
	Call(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}, handle string) (string, error) {
	raw, err := link.Call(ctx, "tether_registry_search", map[string]any{"kind": "agent", "query": handle, "limit": 50})
	if err != nil {
		return "", fmt.Errorf("the registry could not be searched for @%s: %w", handle, err)
	}
	type row struct {
		URN  string `json:"urn"`
		Name string `json:"display_name"`
	}
	var rows []row
	if json.Unmarshal(raw, &rows) != nil {
		var wrapped map[string]json.RawMessage
		_ = json.Unmarshal(raw, &wrapped)
		for _, key := range []string{"profiles", "results", "items", "rows", "agents"} {
			if json.Unmarshal(wrapped[key], &rows) == nil && len(rows) > 0 {
				break
			}
		}
	}
	for _, r := range rows {
		if strings.EqualFold(strings.TrimPrefix(r.Name, "@"), handle) {
			return r.URN, nil
		}
	}
	if len(rows) == 1 {
		return rows[0].URN, nil
	}
	var names []string
	for i, r := range rows {
		if i == 8 {
			break
		}
		names = append(names, r.Name)
	}
	if len(names) > 0 {
		return "", fmt.Errorf("no seat is named exactly %q; the registry has: %s. Give one of those names, or its address", handle, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("no seat named %q was found in the registry", handle)
}

// OwnWaiting is the answers waiting unread at the assistant's own address to what it asked
// as itself. It marks nothing: OwnAnswers tells them, once, with the next message.
func OwnWaiting(ctx context.Context, own *inbox.Inbox, watch *assist.Watcher) (ids []string, answers []Arrived) {
	items, _ := own.List(ctx, inbox.Filter{UnreadOnly: true, Limit: 40})
	for _, it := range items {
		if it.InReplyTo == "" {
			continue
		}
		about, ok := watch.Asked(ctx, it.InReplyTo)
		if !ok {
			continue
		}
		name := it.FromName
		if name == "" {
			name = it.From
		}
		ids = append(ids, it.ID)
		answers = append(answers, Arrived{From: name, About: about})
	}
	return ids, answers
}

// OwnAnswers is an ambient source: replies that came to the assistant's own address to what
// it asked as itself (consult). Each is told once and marked read; it stays in the mailbox.
func OwnAnswers(own *inbox.Inbox, watch *assist.Watcher) AmbientSource {
	return func(ctx context.Context) string {
		items, sources := own.List(ctx, inbox.Filter{UnreadOnly: true, Limit: 40})
		for _, s := range sources {
			if s.Error != "" {
				return ""
			}
		}
		var b strings.Builder
		other := 0
		for _, it := range items {
			about, ok := "", false
			if it.InReplyTo != "" {
				about, ok = watch.Asked(ctx, it.InReplyTo)
			}
			if !ok {
				other++
				continue
			}
			name := it.FromName
			if name == "" {
				name = it.From
			}
			fmt.Fprintf(&b, "- %s answered what you asked (%s):\n%s\n", name, about, clip(strings.TrimSpace(it.Body), 3000))
			if p, err := own.Provider(it.Provider, it.Account); err == nil {
				_ = p.MarkRead(ctx, it.Account, it.ID)
			}
		}
		text := ""
		if b.Len() > 0 {
			text = "Answers to what you asked other seats as yourself (each is told to you once; it stays in your own mailbox):\n" + strings.TrimRight(b.String(), "\n")
		}
		if other > 0 {
			if text != "" {
				text += "\n"
			}
			text += fmt.Sprintf("Your own mailbox has %d other unread message(s) (tether_message_list).", other)
		}
		return text
	}
}
