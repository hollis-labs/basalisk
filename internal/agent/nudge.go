package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Nudges. The assistant answers when the owner speaks; a nudge is Basalisk speaking to it
// instead, in the owner's running thread, when something came that the owner is waiting to
// hear: the answer to what they had it ask. The assistant then tells the owner in the
// chat, unasked. The owner asked for the information, so the information is what they get,
// not a place to go and look for it.

// Nudge starts the assistant on the owner's running thread with a message Basalisk wrote.
// label is what the interface shows for it; text is what the assistant is told. It uses
// the tools of the owner's desktop page as last seen. ErrBusy when the assistant is in the
// middle of a message.
func (rt *Runtime) Nudge(ctx context.Context, owner, label, text string) (string, error) {
	if owner == "" {
		return "", errors.New("no owner has been seen yet")
	}
	rt.mu.Lock()
	pg := rt.pages[owner]
	rt.mu.Unlock()
	return rt.Start(ctx, StartInput{Owner: owner, Text: text, ClientID: pg.client, Context: pg.context, Tools: pg.tools, Auto: label})
}

// NudgeWhenFree is Nudge, waiting while the assistant is busy or no owner has been seen (up
// to a quarter of an hour; after that the news still reaches it with the owner's next
// message, in the <around> block).
func (rt *Runtime) NudgeWhenFree(ctx context.Context, owner func() string, label, text string) {
	deadline := time.Now().Add(15 * time.Minute)
	for {
		_, err := rt.Nudge(ctx, owner(), label, text)
		if err == nil {
			return
		}
		if (!errors.Is(err, ErrBusy) && owner() != "") || time.Now().After(deadline) {
			slog.Warn("agent: the assistant was not told of what came", "label", label, "err", err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

// Arrived is one answer that came to something the assistant asked.
type Arrived struct {
	From, About string
	// Account and ID name the message in the owner's mail; empty for an answer that came to
	// the assistant's own address (it is in the <around> block whole).
	Account, ID string
}

// ArrivedNudge is the label and the text of the nudge for answers that came.
func ArrivedNudge(answers []Arrived) (label, text string) {
	var who []string
	var b strings.Builder
	b.WriteString("<arrived>\nBasalisk wrote this; the owner did not type it and has not seen it. ")
	if len(answers) == 1 {
		b.WriteString("An answer has come to something you asked on the owner's behalf:\n")
	} else {
		fmt.Fprintf(&b, "%d answers have come to things you asked on the owner's behalf:\n", len(answers))
	}
	for _, a := range answers {
		seen := false
		for _, w := range who {
			seen = seen || w == a.From
		}
		if !seen {
			who = append(who, a.From)
		}
		fmt.Fprintf(&b, "- from %s, to: %s", a.From, a.About)
		if a.ID != "" {
			fmt.Fprintf(&b, " (account %s, id %s)", a.Account, a.ID)
		} else {
			b.WriteString(" (it came to your own address; it is in the <around> block)")
		}
		b.WriteString("\n")
	}
	b.WriteString("The owner asked you for this and is waiting on it, so give it to them now. Read each answer whole (owner_mail_read for the ones with an id), then tell the owner what it says: the conclusion first, then the specifics they need, complete enough to act on without opening their inbox. Do not tell them an answer has arrived or where to find it; tell them the answer. If it asks something back or needs their decision, say that plainly at the end. If it shows the question was not answered, say so and what you will do about it. Then settle each one you have told them (owner_mail_settle).\n</arrived>")
	label = "An answer came from " + strings.Join(who, ", ")
	if len(who) > 3 {
		label = fmt.Sprintf("%d answers came", len(answers))
	}
	return label, b.String()
}

// MainMark says where the owner's running thread stands: the run working on it now, if
// any, and the number of its last message. The interface watches it to show what the
// assistant said unasked.
func (rt *Runtime) MainMark(ctx context.Context, owner string) (run string, last int) {
	thread, err := rt.store.MainThread(ctx, owner)
	if err != nil {
		return "", 0
	}
	_ = rt.store.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM messages WHERE thread_id=?`, thread).Scan(&last)
	return rt.ActiveRun(thread), last
}
