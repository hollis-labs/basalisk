package assurance

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"time"

	"github.com/hollis-labs/libs/mesh/core/hitl"
)

type ScopedTokens interface {
	Token(context.Context, string) (string, error)
}
type TachyonClient struct {
	origin string
	client *http.Client
	tokens ScopedTokens
}

func NewTachyonClient(origin string, tokens ScopedTokens, client *http.Client) (*TachyonClient, error) {
	if !httpsOrigin(origin) || tokens == nil {
		return nil, ErrInvalid
	}
	return &TachyonClient{origin, wireClient(client), tokens}, nil
}
func (c *TachyonClient) call(ctx context.Context, route, scope string, input, output any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx, scope)
	if err != nil {
		return err
	}
	return postWire(ctx, c.client, c.origin+"/api/approvals/trusted/v1/"+route, token, false, input, output, 64<<10)
}
func (c *TachyonClient) LookupItem(ctx context.Context, id string) (Item, error) {
	if c == nil || !validID(id) {
		return Item{}, ErrInvalid
	}
	query := rand.Text()
	input := struct {
		ItemID  string `json:"item_id"`
		QueryID string `json:"query_id"`
	}{id, query}
	var reply struct {
		QueryID string `json:"query_id"`
		Item    Item   `json:"item"`
	}
	if err := c.call(ctx, "item", ItemReadScope, input, &reply); err != nil {
		return Item{}, err
	}
	i := reply.Item
	_, digest, err := ParseOperation(i.Operation)
	if reply.QueryID != query || i.ID != id || i.Revision < 1 || i.ExpiresAt.IsZero() || err != nil || digest != i.Digest || (i.Status != "pending" && !hitl.State(i.Status).Valid()) {
		return Item{}, ErrDenied
	}
	return i, nil
}

type CompletionCommand struct {
	ItemID           string `json:"item_id"`
	CompletionID     string `json:"completion_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (c *TachyonClient) Deliver(ctx context.Context, cmd CompletionCommand) (json.RawMessage, error) {
	if c == nil || !validID(cmd.ItemID) || !validID(cmd.CompletionID) || cmd.ExpectedRevision < 1 {
		return nil, ErrInvalid
	}
	var reply struct {
		Outcome json.RawMessage `json:"outcome"`
	}
	if err := c.call(ctx, "complete", CompletionScope, cmd, &reply); err != nil {
		return nil, err
	}
	out, err := checkedOutcome(reply.Outcome, cmd.ItemID)
	if err != nil || out.OutcomeState() != hitl.StateResolved || out.OutcomeRevision() <= cmd.ExpectedRevision {
		return nil, ErrDenied
	}
	return reply.Outcome, nil
}

type OutcomeStatus struct {
	QueryID  string          `json:"query_id"`
	ItemID   string          `json:"item_id"`
	Revision int64           `json:"revision"`
	State    hitl.State      `json:"state"`
	Outcome  json.RawMessage `json:"terminal_outcome"`
}

func (c *TachyonClient) LookupOutcome(ctx context.Context, id string) (OutcomeStatus, error) {
	if c == nil || !validID(id) {
		return OutcomeStatus{}, ErrInvalid
	}
	query := rand.Text()
	input := struct {
		ItemID  string `json:"item_id"`
		QueryID string `json:"query_id"`
	}{id, query}
	var reply OutcomeStatus
	if err := c.call(ctx, "outcome", OutcomeReadScope, input, &reply); err != nil {
		return OutcomeStatus{}, err
	}
	if reply.QueryID != query || reply.ItemID != id || reply.Revision < 1 || !reply.State.Valid() {
		return OutcomeStatus{}, ErrDenied
	}
	if reply.State.IsTerminal() {
		out, err := checkedOutcome(reply.Outcome, id)
		if err != nil || out.OutcomeState() != reply.State || out.OutcomeRevision() != reply.Revision {
			return OutcomeStatus{}, ErrDenied
		}
	} else if string(reply.Outcome) != "null" {
		return OutcomeStatus{}, ErrDenied
	}
	return reply, nil
}
func checkedOutcome(raw json.RawMessage, id string) (hitl.Outcome, error) {
	if boundedJSON(raw) != nil {
		return nil, ErrInvalid
	}
	var version struct {
		Version string `json:"contract_version"`
	}
	if json.Unmarshal(raw, &version) != nil || version.Version != hitl.ContractVersion {
		return nil, ErrInvalid
	}
	out, err := hitl.UnmarshalOutcome(raw)
	if err != nil || out.OutcomeItemID() != id || out.OutcomeTime().IsZero() {
		return nil, ErrDenied
	}
	encoded, err := json.Marshal(out)
	if err != nil || sameWireJSON(raw, encoded) != nil {
		return nil, ErrInvalid
	}
	if resolved, ok := out.(hitl.Resolved); ok && resolved.Resolution.InteractionRevision != resolved.InteractionRevision {
		return nil, ErrInvalid
	}
	return out, nil
}

// Deliver uses only the already committed completion and its immutable item
// binding. A retry never creates another ceremony or silently changes revision.
func (s *Service) Deliver(ctx context.Context, handle, id string, client *TachyonClient) (json.RawMessage, error) {
	u, err := s.session(ctx, handle)
	if err != nil {
		return nil, err
	}
	a, err := s.loadAttempt(ctx, id)
	if err != nil || a.Subject != u.Subject || a.SessionHash != u.Binding {
		return nil, ErrDenied
	}
	c, err := s.LookupCompletion(ctx, id)
	if err != nil {
		return nil, err
	}
	return client.Deliver(ctx, CompletionCommand{c.ItemID, c.ID, c.ExpectedRevision})
}

// RecoverOutcome requires a current session for the same stable human, but not the
// original session or a still-live proof. It queries Tachyon only and conveys
// an inbox outcome, never authority to execute a credential operation.
func (s *Service) RecoverOutcome(ctx context.Context, handle, id string, client *TachyonClient) (OutcomeStatus, error) {
	u, err := s.session(ctx, handle)
	if err != nil {
		return OutcomeStatus{}, err
	}
	a, err := s.loadAttempt(ctx, id)
	if err != nil || a.Subject != u.Subject {
		return OutcomeStatus{}, ErrDenied
	}
	return client.LookupOutcome(ctx, a.Binding.ItemID)
}
