package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/ui/ui-go/chatstream"
)

const twoQuestions = `{"title":"Before I draft the plan","questions":[
	{"prompt":"Which quarter?","options":[{"label":"Q1","recommended":true},{"label":"Q2","note":"after the launch"}],"group":"Timing"},
	{"prompt":"Anything to leave out?","link":{"workspace":"planner","item_id":"it1","kind":"decision"}}]}`

// ask runs one message in which the model asks the owner the two questions, and returns the set.
func ask(t *testing.T, rt *Runtime) QuestionSet {
	t.Helper()
	id, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "plan the quarter", ClientID: "c1", Tools: []ToolSpec{lookTool}})
	if err != nil {
		t.Fatal(err)
	}
	asked := false
	evs := follow(t, rt, id, func(e chatstream.Event) {
		if e.Verb == chatstream.VerbApprovalRequest {
			t.Error("asking the owner asked for permission")
		}
		if e.Verb == chatstream.VerbActivity && e.Kind == "basalisk.questions" {
			asked = true
		}
	})
	if last(evs).Verb != chatstream.VerbRunFinish || !asked {
		t.Fatalf("the run ended %+v; the page was told of the questions: %v", last(evs), asked)
	}
	sets, err := rt.Store().OpenQuestionSets(t.Context(), "u1")
	if err != nil || len(sets) != 1 {
		t.Fatalf("open sets: %+v, %v", sets, err)
	}
	return sets[0]
}

func TestTheAgentAsksOnCardsAndTheAnswersGoTogetherOnSubmit(t *testing.T) {
	s := newStandIn(t,
		toolReply("toolu_1", "ask_owner", twoQuestions),
		textReply("Two questions are waiting for you."),
		textReply("Still waiting on your answers."),
		textReply("Thanks, Q2 it is."),
	)
	rt := newRuntime(t, s)
	var linked []string
	rt.SetAnswerLink(func(_ context.Context, link QuestionLink, text string) error {
		linked = append(linked, link.Workspace+"/"+link.ItemID+"/"+link.Kind+": "+text)
		return nil
	})
	set := ask(t, rt)
	if set.Title != "Before I draft the plan" || set.State != SetOpen || set.Task != "" || len(set.Questions) != 2 {
		t.Fatalf("set: %+v", set)
	}
	q1, q2 := set.Questions[0], set.Questions[1]
	if q1.ID != "q1" || q1.State != QuestionOpen || len(q1.Options) != 2 || q1.Options[0].ID != "o1" || !q1.Options[0].Recommended || q1.Group != "Timing" || q2.Link == nil || q2.Link.ItemID != "it1" {
		t.Fatalf("cards: %+v", set.Questions)
	}
	// The model was told the questions are with the owner, not given answers.
	if sent, _ := json.Marshal(s.bodies[1]["messages"]); !strings.Contains(string(sent), set.ID) || !strings.Contains(string(sent), "end your turn") {
		t.Errorf("the tool's result: %s", sent)
	}

	// Answers are held, in any order, and can be changed; nothing is sent.
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q2", QuestionAnswer{Text: "  the old pricing page "}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q1", QuestionAnswer{Options: []string{"o1"}}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q1", QuestionAnswer{Options: []string{"o2"}, Text: "not before March"}, false); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]QuestionAnswer{"unknown option": {Options: []string{"o9"}}, "two on a single": {Options: []string{"o1", "o2"}}} {
		if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q1", bad, false); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	if _, err := rt.AnswerQuestion(t.Context(), "someone-else", set.ID, "q1", QuestionAnswer{Text: "x"}, false); !errors.Is(err, ErrNoQuestions) {
		t.Errorf("another owner answered: %v", err)
	}
	if len(s.bodies) != 2 {
		t.Fatalf("holding answers called the model: %d requests", len(s.bodies))
	}

	// An ordinary message meanwhile: the thread is reminded of what it asked, and the set stays open.
	id, _ := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "how is it going?", ClientID: "c1"})
	follow(t, rt, id, nil)
	if sent := lastUserText(t, s.bodies[2]); !strings.Contains(sent, "<questions_open>") || !strings.Contains(sent, set.ID) || !strings.Contains(sent, "2 with an answer held") {
		t.Errorf("the thread was not reminded: %s", sent)
	}

	// Submit: one message with every answer, and the linked answer written down.
	runID, done, err := rt.SubmitQuestions(t.Context(), "u1", set.ID, SubmitInput{Note: "and keep it short", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt, runID, nil)
	sent := lastUserText(t, s.bodies[3])
	for _, want := range []string{"<answers set=", "1. Which quarter?", "Answer: Q2", "In their words: not before March", "2. Anything to leave out?", "In their words: the old pricing page", "and keep it short"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the answers message lacks %q:\n%s", want, sent)
		}
	}
	if strings.Contains(sent, "<questions_open>") {
		t.Errorf("the answers came with a reminder of themselves:\n%s", sent)
	}
	if done.State != SetSubmitted || done.Questions[1].LinkResult != "written" || done.Questions[0].LinkResult != "" {
		t.Fatalf("after submit: %+v", done)
	}
	if len(linked) != 1 || !strings.HasPrefix(linked[0], "planner/it1/decision: Decision. Asked: Anything to leave out?") || !strings.Contains(linked[0], "the old pricing page") {
		t.Errorf("written to the plans: %q", linked)
	}
	// The interface shows a short line for it, not the block.
	msgs, _ := rt.Store().Messages(t.Context(), mustMain(t, rt, "u1"))
	var meta struct{ Auto string }
	for _, m := range msgs {
		if m.RunID == runID && m.Role == "user" {
			_ = json.Unmarshal(m.Meta, &meta)
		}
	}
	if meta.Auto != "Answered 2 of 2 questions" {
		t.Errorf("shown as %q", meta.Auto)
	}
	// It is closed: no second submit, no more answers, nothing left open.
	if _, _, err := rt.SubmitQuestions(t.Context(), "u1", set.ID, SubmitInput{}); !errors.Is(err, ErrQuestionsClosed) {
		t.Errorf("submitted twice: %v", err)
	}
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q1", QuestionAnswer{Text: "late"}, false); !errors.Is(err, ErrQuestionsClosed) {
		t.Errorf("answered after submit: %v", err)
	}
	if open, _ := rt.Store().OpenQuestionSets(t.Context(), "u1"); len(open) != 0 {
		t.Errorf("still open: %+v", open)
	}
}

func mustMain(t *testing.T, rt *Runtime, owner string) string {
	t.Helper()
	id, err := rt.Store().MainThread(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestUnansweredAndSkippedCardsAreSentAsSuchAndAFailedLinkDoesNotHoldTheAnswers(t *testing.T) {
	s := newStandIn(t, toolReply("toolu_1", "ask_owner", twoQuestions), textReply("Waiting."), textReply("Understood."))
	rt := newRuntime(t, s)
	rt.SetAnswerLink(func(context.Context, QuestionLink, string) error { return errors.New("the item is gone") })
	set := ask(t, rt)
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q1", QuestionAnswer{}, true); err != nil {
		t.Fatal(err)
	}
	// An answer taken back leaves the card open.
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q2", QuestionAnswer{Text: "something"}, false); err != nil {
		t.Fatal(err)
	}
	if got, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q2", QuestionAnswer{}, false); err != nil || got.Questions[1].State != QuestionOpen {
		t.Fatalf("cleared: %+v, %v", got, err)
	}
	runID, done, err := rt.SubmitQuestions(t.Context(), "u1", set.ID, SubmitInput{})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt, runID, nil)
	sent := lastUserText(t, s.bodies[2])
	if !strings.Contains(sent, "Skipped: the owner chose not to answer") || !strings.Contains(sent, "Not answered.") || !strings.Contains(sent, "Do not guess") {
		t.Errorf("the answers message:\n%s", sent)
	}
	if done.Questions[1].LinkResult != "" {
		t.Errorf("an unanswered card was written to the plans: %+v", done.Questions[1])
	}

	// A linked answer whose write fails still reaches the agent, and the card says so.
	s2 := newStandIn(t, toolReply("toolu_1", "ask_owner", twoQuestions), textReply("Waiting."), textReply("Got it."))
	rt2 := newRuntime(t, s2)
	rt2.SetAnswerLink(func(context.Context, QuestionLink, string) error { return errors.New("the item is gone") })
	set2 := ask(t, rt2)
	if _, err := rt2.AnswerQuestion(t.Context(), "u1", set2.ID, "q2", QuestionAnswer{Text: "the footer"}, false); err != nil {
		t.Fatal(err)
	}
	runID, done, err = rt2.SubmitQuestions(t.Context(), "u1", set2.ID, SubmitInput{})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt2, runID, nil)
	if !strings.Contains(lastUserText(t, s2.bodies[2]), "the footer") || done.Questions[1].LinkResult != "not written: the item is gone" || done.State != SetSubmitted {
		t.Fatalf("after a failed write: %+v", done)
	}
}

func TestHeldAnswersAreKeptWhenBasaliskIsClosedAndOpenedAgain(t *testing.T) {
	s := newStandIn(t, toolReply("toolu_1", "ask_owner", twoQuestions), textReply("Waiting."))
	rt := newRuntime(t, s)
	set := ask(t, rt)
	if _, err := rt.AnswerQuestion(t.Context(), "u1", set.ID, "q1", QuestionAnswer{Options: []string{"o2"}}, false); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := rt.Store().db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	again, err := OpenStore(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	sets, err := again.OpenQuestionSets(t.Context(), "u1")
	if err != nil || len(sets) != 1 || sets[0].Questions[0].State != QuestionAnswered || len(sets[0].Questions[0].Answer.Options) != 1 || sets[0].Questions[0].Answer.Options[0] != "o2" || sets[0].Questions[1].State != QuestionOpen {
		t.Fatalf("after reopening: %+v, %v", sets, err)
	}
}

func TestOnlySoMuchMayBeAskedAndASetCanBeTakenBackOrPutAway(t *testing.T) {
	many := make([]string, MaxQuestionsPerSet+1)
	for i := range many {
		many[i] = fmt.Sprintf(`{"prompt":"Question %d?"}`, i+1)
	}
	one := func(n int) string { return fmt.Sprintf(`{"questions":[{"prompt":"Set %d?"}]}`, n) }
	replies := []string{toolReply("toolu_0", "ask_owner", `{"questions":[`+strings.Join(many, ",")+`]}`)}
	for i := 1; i <= MaxOpenQuestionSets+1; i++ {
		replies = append(replies, toolReply(fmt.Sprintf("toolu_%d", i), "ask_owner", one(i)))
	}
	replies = append(replies, textReply("Asked."))
	s := newStandIn(t, replies...)
	rt := newRuntime(t, s)
	id, _ := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "ask away", ClientID: "c1"})
	follow(t, rt, id, nil)
	sets, _ := rt.Store().OpenQuestionSets(t.Context(), "u1")
	if len(sets) != MaxOpenQuestionSets {
		t.Fatalf("%d sets are open, want %d", len(sets), MaxOpenQuestionSets)
	}
	results, _ := json.Marshal(s.bodies[len(s.bodies)-1]["messages"])
	if !strings.Contains(string(results), "a set takes at most") || !strings.Contains(string(results), "already wait on the owner") {
		t.Errorf("the model was not told of the limits: %s", results)
	}
	if sets[0].Title != "Set 1?" {
		t.Errorf("a set with no title is called %q", sets[0].Title)
	}

	// The owner puts one away; the agent takes one back and can then see how they stand.
	if err := rt.DismissQuestions(t.Context(), "u1", sets[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := rt.DismissQuestions(t.Context(), "u1", sets[0].ID); !errors.Is(err, ErrQuestionsClosed) {
		t.Errorf("put away twice: %v", err)
	}
	s.replies = append(s.replies,
		toolReply("toolu_w", "ask_owner_withdraw", fmt.Sprintf(`{"set_id":%q}`, sets[1].ID)),
		toolReply("toolu_s", "ask_owner_status", `{}`),
		textReply("One left."))
	id, _ = rt.Start(t.Context(), StartInput{Owner: "u1", Text: "tidy up", ClientID: "c1"})
	follow(t, rt, id, nil)
	left, _ := rt.Store().OpenQuestionSets(t.Context(), "u1")
	if len(left) != 1 || left[0].ID != sets[2].ID {
		t.Fatalf("left open: %+v", left)
	}
	if got, _ := rt.Store().QuestionSet(t.Context(), "u1", sets[1].ID); got.State != SetWithdrawn {
		t.Errorf("the set taken back is %q", got.State)
	}
	results, _ = json.Marshal(s.bodies[len(s.bodies)-1]["messages"])
	if !strings.Contains(string(results), `\"with_an_answer_held\":0`) || strings.Contains(string(results), sets[1].ID+`\",\"title`) {
		t.Errorf("ask_owner_status: %s", results)
	}
}

func TestATaskAgentsQuestionsAreAnsweredInItsOwnThreadAndSetItGoingAgain(t *testing.T) {
	s := newStandIn(t,
		toolReply("toolu_1", "ask_owner", `{"questions":[{"prompt":"Which folder?","options":[{"label":"Docs"},{"label":"Notes"}],"multi":true}]}`),
		textReply("I need the folder before I can go on."),
		textReply("Both folders are read."),
	)
	rt := newRuntime(t, s)
	task, _, err := rt.StartTask(t.Context(), TaskInput{Owner: "u1", Brief: "read the folder", Origin: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	waitTask(t, rt, "u1", task.ID)
	sets, _ := rt.Store().OpenQuestionSets(t.Context(), "u1")
	if len(sets) != 1 || sets[0].Task != task.ID || sets[0].Thread != task.ID {
		t.Fatalf("the task's questions: %+v", sets)
	}
	if _, err := rt.AnswerQuestion(t.Context(), "u1", sets[0].ID, "q1", QuestionAnswer{Options: []string{"o2", "o1"}}, false); err != nil {
		t.Fatal(err)
	}
	runID, _, err := rt.SubmitQuestions(t.Context(), "u1", sets[0].ID, SubmitInput{})
	if err != nil {
		t.Fatal(err)
	}
	follow(t, rt, runID, nil)
	if got := waitTask(t, rt, "u1", task.ID); got.State != TaskDone || got.Result != "Both folders are read." {
		t.Fatalf("the task after its answers: %+v", got)
	}
	if sent := lastUserText(t, s.bodies[2]); !strings.Contains(sent, "Answer: Docs; Notes") {
		t.Errorf("the task agent was sent: %s", sent)
	}
	if sys, _ := json.Marshal(s.bodies[2]["system"]); !strings.Contains(string(sys), "working as a task agent") {
		t.Error("the answers did not go to the task agent")
	}
	if main, _ := rt.Store().Messages(t.Context(), mustMain(t, rt, "u1")); len(main) != 0 {
		t.Errorf("the running thread got %d messages", len(main))
	}
}

func TestSubmittingWhileTheAgentIsBusyKeepsTheSetOpen(t *testing.T) {
	s := newStandIn(t, toolReply("toolu_1", "ask_owner", twoQuestions), textReply("Waiting."), "hang")
	rt := newRuntime(t, s)
	set := ask(t, rt)
	busy, err := rt.Start(t.Context(), StartInput{Owner: "u1", Text: "something long", ClientID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := rt.SubmitQuestions(t.Context(), "u1", set.ID, SubmitInput{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("submit while busy: %v", err)
	}
	if got, _ := rt.Store().QuestionSet(t.Context(), "u1", set.ID); got.State != SetOpen {
		t.Errorf("the set is %q after a submit that did not go", got.State)
	}
	_ = rt.Cancel("u1", busy)
}

func TestTheStoreHasTheQuestionTablesAfterUpgrade(t *testing.T) {
	store, err := OpenStore(t.Context(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var n int
	if err := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE name IN ('question_sets','questions')`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("tables: %d, %v", n, err)
	}
}
