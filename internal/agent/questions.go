package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

// Questions. When the agent has things to ask the owner it does not write a numbered list:
// it calls ask_owner, which returns at once, and each question is shown as a card with its
// options and room for the owner's own words. The answers are held on the cards, where the
// owner can change them, leave and come back, until they press Submit; then they go to the
// thread that asked as one message, and the agent takes it from there.

// Limits on what may be asked at once.
const (
	MaxQuestionsPerSet  = 12
	MaxOpenQuestionSets = 3
	maxQuestionOptions  = 6
)

// How a set and a question stand.
const (
	SetOpen      = "open"
	SetSubmitted = "submitted"
	SetWithdrawn = "withdrawn" // the agent took it back
	SetDismissed = "dismissed" // the owner put it away unanswered

	QuestionOpen     = "open"
	QuestionAnswered = "answered" // held on the card; not sent until the set is submitted
	QuestionSkipped  = "skipped"
)

// QuestionOption is one thing the owner can pick.
type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Note        string `json:"note,omitempty"`
	Recommended bool   `json:"recommended,omitempty"`
}

// QuestionLink names an item in one of the owner's Work workspaces (the Planner, say) that
// the answer is written to, as a note, when the set is submitted.
type QuestionLink struct {
	Workspace string `json:"workspace"`
	ItemID    string `json:"item_id"`
	Kind      string `json:"kind"` // "decision" or "exploration"
}

// QuestionAnswer is what the owner put on a card: the options picked, their own words, or both.
type QuestionAnswer struct {
	Options []string `json:"options"`
	Text    string   `json:"text"`
}

// Question is one card.
type Question struct {
	ID      string           `json:"id"`
	Prompt  string           `json:"prompt"`
	Detail  string           `json:"detail,omitempty"`
	Options []QuestionOption `json:"options"`
	Multi   bool             `json:"multi,omitempty"`
	Group   string           `json:"group,omitempty"`
	Link    *QuestionLink    `json:"link,omitempty"`
	State   string           `json:"state"`
	Answer  QuestionAnswer   `json:"answer"`
	// LinkResult says how writing the answer to the linked item went ("" until submitted).
	LinkResult string `json:"link_result,omitempty"`
}

// QuestionSet is what one ask_owner call asked.
type QuestionSet struct {
	ID        string     `json:"id"`
	Thread    string     `json:"thread"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	CreatedAt string     `json:"created_at"`
	ClosedAt  string     `json:"closed_at,omitempty"`
	Questions []Question `json:"questions"`
	// Task is the task whose agent asked, when it was not the running thread.
	Task string `json:"task,omitempty"`
}

// ErrNoQuestions is returned for a set or a card that is not the caller's or is not there.
var ErrNoQuestions = errors.New("there are no such questions")

// ErrQuestionsClosed is returned when a set has already been submitted or put away.
var ErrQuestionsClosed = errors.New("those questions are closed")

// card is what is stored of a question: everything but how it stands.
type card struct {
	Prompt  string           `json:"prompt"`
	Detail  string           `json:"detail,omitempty"`
	Options []QuestionOption `json:"options"`
	Multi   bool             `json:"multi,omitempty"`
	Group   string           `json:"group,omitempty"`
	Link    *QuestionLink    `json:"link,omitempty"`
}

func (s *Store) newQuestionSet(ctx context.Context, owner, thread, title string, qs []Question) (QuestionSet, error) {
	set := QuestionSet{ID: newID("qs"), Thread: thread, Title: title, State: SetOpen, CreatedAt: now()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return set, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO question_sets(id, owner, thread_id, title, state, created_at) VALUES(?,?,?,?,?,?)`,
		set.ID, owner, thread, title, set.State, set.CreatedAt); err != nil {
		return set, err
	}
	for i, q := range qs {
		q.ID, q.State, q.Answer = fmt.Sprintf("q%d", i+1), QuestionOpen, QuestionAnswer{Options: []string{}}
		raw, _ := json.Marshal(card{Prompt: q.Prompt, Detail: q.Detail, Options: q.Options, Multi: q.Multi, Group: q.Group, Link: q.Link})
		if _, err := tx.ExecContext(ctx, `INSERT INTO questions(set_id, id, position, card, state) VALUES(?,?,?,?,?)`, set.ID, q.ID, i, string(raw), q.State); err != nil {
			return set, err
		}
		set.Questions = append(set.Questions, q)
	}
	return set, tx.Commit()
}

// QuestionSet is one of the owner's sets, with its cards.
func (s *Store) QuestionSet(ctx context.Context, owner, id string) (QuestionSet, bool) {
	sets, err := s.questionSets(ctx, `q.owner=? AND q.id=?`, owner, id)
	if err != nil || len(sets) == 0 {
		return QuestionSet{}, false
	}
	return sets[0], true
}

// OpenQuestionSets are the owner's sets that wait on them, oldest first.
func (s *Store) OpenQuestionSets(ctx context.Context, owner string) ([]QuestionSet, error) {
	return s.questionSets(ctx, `q.owner=? AND q.state=?`, owner, SetOpen)
}

func (s *Store) openQuestionSetsOf(ctx context.Context, thread string) []QuestionSet {
	sets, err := s.questionSets(ctx, `q.thread_id=? AND q.state=?`, thread, SetOpen)
	if err != nil {
		slog.Warn("agent: the open questions could not be read", "thread", thread, "err", err)
	}
	return sets
}

func (s *Store) questionSets(ctx context.Context, where string, args ...any) ([]QuestionSet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT q.id, q.thread_id, q.title, q.state, q.created_at, q.closed_at,
		CASE WHEN t.kind='task' THEN t.id ELSE '' END
		FROM question_sets q LEFT JOIN threads t ON t.id=q.thread_id WHERE `+where+` ORDER BY q.created_at, q.id`, args...)
	if err != nil {
		return nil, err
	}
	out := []QuestionSet{}
	for rows.Next() {
		var set QuestionSet
		if err := rows.Scan(&set.ID, &set.Thread, &set.Title, &set.State, &set.CreatedAt, &set.ClosedAt, &set.Task); err != nil {
			_ = rows.Close()
			return nil, err
		}
		set.Questions = []Question{}
		out = append(out, set)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		qrows, err := s.db.QueryContext(ctx, `SELECT id, card, state, answer, link_result FROM questions WHERE set_id=? ORDER BY position`, out[i].ID)
		if err != nil {
			return nil, err
		}
		for qrows.Next() {
			var q Question
			var raw, answer string
			if err := qrows.Scan(&q.ID, &raw, &q.State, &answer, &q.LinkResult); err != nil {
				_ = qrows.Close()
				return nil, err
			}
			var c card
			_ = json.Unmarshal([]byte(raw), &c)
			q.Prompt, q.Detail, q.Options, q.Multi, q.Group, q.Link = c.Prompt, c.Detail, c.Options, c.Multi, c.Group, c.Link
			_ = json.Unmarshal([]byte(answer), &q.Answer)
			if q.Options == nil {
				q.Options = []QuestionOption{}
			}
			if q.Answer.Options == nil {
				q.Answer.Options = []string{}
			}
			out[i].Questions = append(out[i].Questions, q)
		}
		if err := qrows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) setQuestion(ctx context.Context, set, id, state string, a QuestionAnswer) error {
	raw, _ := json.Marshal(a)
	_, err := s.db.ExecContext(ctx, `UPDATE questions SET state=?, answer=? WHERE set_id=? AND id=?`, state, string(raw), set, id)
	return err
}

func (s *Store) setLinkResult(ctx context.Context, set, id, result string) {
	if _, err := s.db.ExecContext(ctx, `UPDATE questions SET link_result=? WHERE set_id=? AND id=?`, result, set, id); err != nil {
		slog.Warn("agent: a question's link result was not kept", "set", set, "err", err)
	}
}

// closeQuestionSet moves an open set to its end state. It reports whether it was open.
func (s *Store) closeQuestionSet(ctx context.Context, id, state string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE question_sets SET state=?, closed_at=? WHERE id=? AND state=?`, state, now(), id, SetOpen)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// AnswerLink writes an owner's answer to the item a card is linked to.
type AnswerLink func(ctx context.Context, link QuestionLink, text string) error

// SetAnswerLink says how a linked card's answer is written down. Call before serving; with
// none set, a link is kept on the card and nothing is written.
func (rt *Runtime) SetAnswerLink(fn AnswerLink) {
	rt.mu.Lock()
	rt.answerLink = fn
	rt.mu.Unlock()
}

// AnswerQuestion holds the owner's answer on a card. An answer with nothing in it clears
// the card; skip marks it as one the owner chose not to answer.
func (rt *Runtime) AnswerQuestion(ctx context.Context, owner, setID, id string, a QuestionAnswer, skip bool) (QuestionSet, error) {
	set, ok := rt.store.QuestionSet(ctx, owner, setID)
	if !ok {
		return QuestionSet{}, ErrNoQuestions
	}
	if set.State != SetOpen {
		return set, ErrQuestionsClosed
	}
	var q *Question
	for i := range set.Questions {
		if set.Questions[i].ID == id {
			q = &set.Questions[i]
		}
	}
	if q == nil {
		return set, ErrNoQuestions
	}
	a.Text = strings.TrimSpace(a.Text)
	if len(a.Text) > 8000 {
		return set, errors.New("the answer is too long")
	}
	picked := []string{}
	for _, opt := range q.Options { // kept in the card's order, each once
		for _, want := range a.Options {
			if want == opt.ID {
				picked = append(picked, opt.ID)
				break
			}
		}
	}
	if len(picked) != len(a.Options) {
		return set, errors.New("that is not one of the question's options")
	}
	if len(picked) > 1 && !q.Multi {
		return set, errors.New("the question takes one option")
	}
	a.Options = picked
	state := QuestionAnswered
	switch {
	case skip:
		state, a = QuestionSkipped, QuestionAnswer{Options: []string{}}
	case len(picked) == 0 && a.Text == "":
		state = QuestionOpen
	}
	if err := rt.store.setQuestion(ctx, setID, id, state, a); err != nil {
		return set, err
	}
	q.State, q.Answer = state, a
	return set, nil
}

// SubmitInput is what the page sends with Submit: the owner's own line, if any, and what
// the page can do (as with any message).
type SubmitInput struct {
	Note     string
	ClientID string
	Context  json.RawMessage
	Tools    []ToolSpec
}

// SubmitQuestions sends a set's answers, together, to the thread that asked, and starts
// the agent on them. Cards with no answer go as "not answered". ErrBusy when the agent is
// in the middle of a message there: the set stays open, to be submitted again.
func (rt *Runtime) SubmitQuestions(ctx context.Context, owner, setID string, in SubmitInput) (string, QuestionSet, error) {
	set, ok := rt.store.QuestionSet(ctx, owner, setID)
	if !ok {
		return "", QuestionSet{}, ErrNoQuestions
	}
	if set.State != SetOpen {
		return "", set, ErrQuestionsClosed
	}
	text := answersBlock(set)
	if note := strings.TrimSpace(in.Note); note != "" {
		text += "\n\n" + note
	}
	answered := 0
	for _, q := range set.Questions {
		if q.State == QuestionAnswered {
			answered++
		}
	}
	label := fmt.Sprintf("Answered %d of %d questions", answered, len(set.Questions))
	if len(set.Questions) == 1 {
		label = "Answered the question"
		if answered == 0 {
			label = "Sent the question back unanswered"
		}
	}
	start := StartInput{Owner: owner, Text: text, ClientID: in.ClientID, Context: in.Context, Tools: in.Tools, Auto: label}
	if _, isTask := rt.store.Task(ctx, owner, set.Thread); isTask {
		start.Thread = set.Thread // a task agent asked: it goes to the task, which sets it going again
	}
	runID, err := rt.Start(ctx, start)
	if err != nil {
		return "", set, err
	}
	bg := context.WithoutCancel(ctx)
	if _, err := rt.store.closeQuestionSet(bg, set.ID, SetSubmitted); err != nil {
		slog.Warn("agent: submitted questions were not marked so", "set", set.ID, "err", err)
	}
	// The answers that are for an item in the owner's plans are written there too. A write
	// that fails is shown on the card; the agent has the answer either way.
	rt.mu.Lock()
	write := rt.answerLink
	rt.mu.Unlock()
	for _, q := range set.Questions {
		if q.Link == nil || q.State != QuestionAnswered {
			continue
		}
		result := "not written: no plans are connected"
		if write != nil {
			result = "written"
			if err := write(bg, *q.Link, linkNote(q)); err != nil {
				result = "not written: " + plain(err)
			}
		}
		rt.store.setLinkResult(bg, set.ID, q.ID, result)
	}
	set, _ = rt.store.QuestionSet(bg, owner, setID)
	return runID, set, nil
}

// DismissQuestions puts an open set away at the owner's asking. Nothing is sent: the agent
// sees it is closed if it asks (ask_owner_status).
func (rt *Runtime) DismissQuestions(ctx context.Context, owner, setID string) error {
	set, ok := rt.store.QuestionSet(ctx, owner, setID)
	if !ok {
		return ErrNoQuestions
	}
	if set.State != SetOpen {
		return ErrQuestionsClosed
	}
	_, err := rt.store.closeQuestionSet(ctx, setID, SetDismissed)
	return err
}

// answerText is one card's answer in words: the labels picked, then the owner's own.
func answerText(q Question) (picked, said string) {
	labels := []string{}
	for _, id := range q.Answer.Options {
		for _, opt := range q.Options {
			if opt.ID == id {
				labels = append(labels, opt.Label)
			}
		}
	}
	return strings.Join(labels, "; "), q.Answer.Text
}

// answersBlock is what the agent is sent when a set is submitted. It carries each question
// again, so it reads on its own in a thread that has been shortened since.
func answersBlock(set QuestionSet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<answers set=%q title=%q>\n", set.ID, set.Title)
	for i, q := range set.Questions {
		fmt.Fprintf(&b, "%d. %s\n", i+1, q.Prompt)
		picked, said := answerText(q)
		switch {
		case q.State == QuestionSkipped:
			b.WriteString("   Skipped: the owner chose not to answer this one.\n")
		case q.State != QuestionAnswered:
			b.WriteString("   Not answered.\n")
		default:
			if picked != "" {
				fmt.Fprintf(&b, "   Answer: %s\n", picked)
			}
			if said != "" {
				fmt.Fprintf(&b, "   In their words: %s\n", strings.ReplaceAll(said, "\n", "\n   "))
			}
		}
	}
	b.WriteString("</answers>\nThese are the owner's answers to what you asked, sent together. Do not guess at one that is not answered: go on with what you have, or ask it again only if you cannot go on without it.")
	return b.String()
}

// linkNote is what is written on the linked item.
func linkNote(q Question) string {
	picked, said := answerText(q)
	kind := "Decision"
	if q.Link != nil && q.Link.Kind == "exploration" {
		kind = "Exploration"
	}
	parts := []string{}
	if picked != "" {
		parts = append(parts, picked)
	}
	if said != "" {
		parts = append(parts, said)
	}
	return fmt.Sprintf("%s. Asked: %s\nAnswer: %s", kind, q.Prompt, strings.Join(parts, "\n"))
}

// openQuestionsBlock reminds a thread, with a message that is not the answers, of what it
// has asked that still waits on the owner, so it does not ask again.
func openQuestionsBlock(sets []QuestionSet) string {
	if len(sets) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<questions_open>\nQuestions you asked that the owner has not sent back yet (they answer on cards and send them together). Do not ask them again.\n")
	for _, set := range sets {
		answered := 0
		for _, q := range set.Questions {
			if q.State != QuestionOpen {
				answered++
			}
		}
		fmt.Fprintf(&b, "- %q (%s): %d questions, %d with an answer held so far.\n", set.Title, set.ID, len(set.Questions), answered)
	}
	b.WriteString("</questions_open>")
	return b.String()
}

// questionsBlocks are what a thread's next message carries about its questions.
func (rt *Runtime) questionsBlocks(ctx context.Context, thread string) []llmtypes.ContentBlock {
	if text := openQuestionsBlock(rt.store.openQuestionSetsOf(ctx, thread)); text != "" {
		return []llmtypes.ContentBlock{{Type: "text", Text: text}}
	}
	return nil
}

// askTools are the agent's tools for asking the owner. Task agents have them too.
func (rt *Runtime) askTools() []ServerTool {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return []ServerTool{
		{
			Spec: ToolSpec{
				Name:  "ask_owner",
				Level: LevelAct,
				Description: "Ask the owner one or more questions as cards they answer in their own time, instead of writing a numbered list in your message. Use it whenever you have two or more questions, or one with clear options. " +
					"Each card has the options you give and room for the owner's own words. It returns at once: the owner answers in any order, may leave and come back, and sends all the answers together, which arrive as a later message in an <answers> block. " +
					"After calling it, finish what you can without the answers, say in a line that the questions are waiting, and end your turn. Do not repeat the questions in your message and do not ask them again. " +
					fmt.Sprintf("At most %d questions a call and %d unanswered calls at a time.", MaxQuestionsPerSet, MaxOpenQuestionSets),
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"title": str("A few words for what the questions are about."),
						"questions": map[string]any{
							"type":     "array",
							"minItems": 1,
							"maxItems": MaxQuestionsPerSet,
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"prompt": str("The question, in a sentence or two."),
									"detail": str("A few lines of background the owner may want; shown folded."),
									"options": map[string]any{
										"type":        "array",
										"maxItems":    maxQuestionOptions,
										"description": "The choices, when there are clear ones. Leave out for an open question. Do not add an \"other\" option: the card always takes the owner's own words.",
										"items": map[string]any{
											"type": "object",
											"properties": map[string]any{
												"label":       str("The choice, in a few words."),
												"note":        str("What choosing it means, in a line."),
												"recommended": map[string]any{"type": "boolean", "description": "True for the one you would pick."},
											},
											"required":             []string{"label"},
											"additionalProperties": false,
										},
									},
									"multi": map[string]any{"type": "boolean", "description": "True when several options may be chosen."},
									"group": str("A heading; questions with the same group are shown together."),
									"link": map[string]any{
										"type":        "object",
										"description": "An item in the owner's plans (a Work workspace such as the Planner) that the answer is written to as a note when it is sent.",
										"properties": map[string]any{
											"workspace": str("The workspace's id."),
											"item_id":   str("The item's id."),
											"kind":      map[string]any{"type": "string", "enum": []string{"decision", "exploration"}},
										},
										"required":             []string{"workspace", "item_id"},
										"additionalProperties": false,
									},
								},
								"required":             []string{"prompt"},
								"additionalProperties": false,
							},
						},
					},
					"required":             []string{"questions"},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run to ask from")
				}
				raw, _ := json.Marshal(input)
				var in struct {
					Title     string     `json:"title"`
					Questions []Question `json:"questions"`
				}
				if err := json.Unmarshal(raw, &in); err != nil {
					return "", errors.New("the questions are not in the shape the tool takes")
				}
				qs, err := checkQuestions(in.Questions)
				if err != nil {
					return "", err
				}
				if open := rt.store.openQuestionSetsOf(ctx, r.thread); len(open) >= MaxOpenQuestionSets {
					return "", fmt.Errorf("%d sets of questions already wait on the owner here; wait for an answer, or take one back with ask_owner_withdraw", len(open))
				}
				title := clip(strings.TrimSpace(in.Title), 90)
				if title == "" {
					title = clip(qs[0].Prompt, 90)
				}
				set, err := rt.store.newQuestionSet(context.WithoutCancel(ctx), r.owner, r.thread, title, qs)
				if err != nil {
					return "", err
				}
				value, _ := json.Marshal(map[string]string{"set_id": set.ID})
				r.emit(chatstream.Event{Verb: chatstream.VerbActivity, Kind: "basalisk.questions", Value: value})
				out, _ := json.Marshal(map[string]any{"set_id": set.ID, "state": set.State, "count": len(set.Questions),
					"next": "The owner has the questions as cards. Their answers will come together in a later message. Finish what you can, say the questions are waiting, and end your turn."})
				return string(out), nil
			},
		},
		{
			Spec: ToolSpec{
				Name:        "ask_owner_status",
				Level:       LevelRead,
				Description: "How questions you asked with ask_owner stand: whether the set is still open, and which cards have an answer held. The answers themselves come when the owner sends them. Leave out set_id for all that are open here.",
				InputSchema: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"set_id": str("One set's id, from ask_owner.")},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run")
				}
				var sets []QuestionSet
				if id, _ := input["set_id"].(string); id != "" {
					set, ok := rt.store.QuestionSet(ctx, r.owner, id)
					if !ok || set.Thread != r.thread {
						return "", fmt.Errorf("there is no set %s here", id)
					}
					sets = []QuestionSet{set}
				} else {
					sets = rt.store.openQuestionSetsOf(ctx, r.thread)
				}
				type row struct {
					ID       string `json:"set_id"`
					Title    string `json:"title"`
					State    string `json:"state"`
					Count    int    `json:"questions"`
					Answered int    `json:"with_an_answer_held"`
				}
				out := []row{}
				for _, set := range sets {
					n := 0
					for _, q := range set.Questions {
						if q.State != QuestionOpen {
							n++
						}
					}
					out = append(out, row{ID: set.ID, Title: set.Title, State: set.State, Count: len(set.Questions), Answered: n})
				}
				rawOut, _ := json.Marshal(map[string]any{"sets": out})
				return string(rawOut), nil
			},
		},
		{
			Spec: ToolSpec{
				Name:        "ask_owner_withdraw",
				Level:       LevelAct,
				Description: "Take back a set of questions you asked with ask_owner that no longer matters (you found the answer, or the work changed). Its cards are removed from the owner's view.",
				InputSchema: map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"set_id": str("The set's id, from ask_owner.")},
					"required":             []string{"set_id"},
					"additionalProperties": false,
				},
			},
			Run: func(ctx context.Context, input map[string]any) (string, error) {
				r := runOf(ctx)
				if r == nil {
					return "", errors.New("no run")
				}
				id, _ := input["set_id"].(string)
				set, ok := rt.store.QuestionSet(ctx, r.owner, id)
				if !ok || set.Thread != r.thread {
					return "", fmt.Errorf("there is no set %s here", id)
				}
				was, err := rt.store.closeQuestionSet(context.WithoutCancel(ctx), id, SetWithdrawn)
				if err != nil {
					return "", err
				}
				if !was {
					return "", fmt.Errorf("set %s is already %s", id, set.State)
				}
				value, _ := json.Marshal(map[string]string{"set_id": id})
				r.emit(chatstream.Event{Verb: chatstream.VerbActivity, Kind: "basalisk.questions", Value: value})
				return `{"state":"withdrawn"}`, nil
			},
		},
	}
}

// checkQuestions makes what the model asked fit a card, or says why it does not.
func checkQuestions(in []Question) ([]Question, error) {
	if len(in) == 0 {
		return nil, errors.New("there are no questions")
	}
	if len(in) > MaxQuestionsPerSet {
		return nil, fmt.Errorf("that is %d questions; a set takes at most %d. Ask the ones that matter most, or ask the rest after these are answered", len(in), MaxQuestionsPerSet)
	}
	out := make([]Question, 0, len(in))
	for i, q := range in {
		q.Prompt = clip(strings.TrimSpace(q.Prompt), 600)
		if q.Prompt == "" {
			return nil, fmt.Errorf("question %d has no prompt", i+1)
		}
		q.Detail, q.Group = clip(strings.TrimSpace(q.Detail), 2000), clip(strings.TrimSpace(q.Group), 60)
		if len(q.Options) > maxQuestionOptions {
			return nil, fmt.Errorf("question %d has %d options; a card takes at most %d", i+1, len(q.Options), maxQuestionOptions)
		}
		opts := []QuestionOption{}
		for j, opt := range q.Options {
			opt.Label = clip(strings.TrimSpace(opt.Label), 120)
			if opt.Label == "" {
				return nil, fmt.Errorf("question %d has an option with no label", i+1)
			}
			opt.ID, opt.Note = fmt.Sprintf("o%d", j+1), clip(strings.TrimSpace(opt.Note), 300)
			opts = append(opts, opt)
		}
		q.Options = opts
		if len(opts) == 0 {
			q.Multi = false
		}
		if q.Link != nil {
			q.Link.Workspace, q.Link.ItemID = strings.TrimSpace(q.Link.Workspace), strings.TrimSpace(q.Link.ItemID)
			if q.Link.Workspace == "" || q.Link.ItemID == "" {
				return nil, fmt.Errorf("question %d's link needs the workspace and the item", i+1)
			}
			if q.Link.Kind != "exploration" {
				q.Link.Kind = "decision"
			}
		}
		out = append(out, q)
	}
	return out, nil
}
