package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hollis-labs/basalisk/internal/fecapture"
	"github.com/hollis-labs/libs/security/pairing"
)

// DeliveryConfig is operator configuration, never input from a paired device.
// Its separate ParentRef cannot fall back to an integration's legacy FE key.
type DeliveryConfig struct {
	IntegrationID, PublicOrigin string
	fecapture.Config
}

func DeliveryConfigFromEnv(get func(string) string) DeliveryConfig {
	return DeliveryConfig{IntegrationID: get("BASALISK_FE_INTEGRATION_ID"), PublicOrigin: get("BASALISK_PUBLIC_ORIGIN"), Config: fecapture.Config{IssuerURL: get("BASALISK_TETHER_ISSUER_URL"), Subject: get("BASALISK_TETHER_SUBJECT"), ParentRef: get("BASALISK_TETHER_PARENT_REF")}}
}

type FragmentWorker struct {
	store  *Store
	config DeliveryConfig
	client *fecapture.Client
}

func (s *Store) NewFragmentWorker(config DeliveryConfig, client *http.Client) (*FragmentWorker, error) {
	if config == (DeliveryConfig{}) {
		return nil, nil
	}
	origin, err := pairing.Origin(config.PublicOrigin)
	if err != nil || origin != config.PublicOrigin || config.IntegrationID == "" {
		return nil, errors.New("fragment delivery requires an integration ID and canonical Basalisk public origin")
	}
	c, err := fecapture.New(config.Config, s.secrets, client)
	if err != nil {
		return nil, err
	}
	s.setDeliveryActivity(config.IntegrationID, "configured")
	return &FragmentWorker{s, config, c}, nil
}

// Run bounds each attempt below the durable lease lifetime. Another process or
// restart may reclaim only an expired lease; stale workers cannot commit results.
func (w *FragmentWorker) Run(ctx context.Context) {
	w.store.setDeliveryActivity(w.config.IntegrationID, "running")
	defer w.store.setDeliveryActivity(w.config.IntegrationID, "stopped")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := w.Once(ctx); err != nil {
			log.Print("Fragment delivery storage unavailable; retrying on the next worker tick")
		}
		w.store.setDeliveryActivity(w.config.IntegrationID, "running")
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type deliveryJob struct {
	sequence                                                                                           int64
	device, capture, integration, payload, manifest, origin, issuer, subject, upstream, receipt, lease string
	attempts                                                                                           int
}

func (w *FragmentWorker) claim(ctx context.Context) (deliveryJob, error) {
	var j deliveryJob
	tx, err := w.store.db.BeginTx(ctx, nil)
	if err != nil {
		return j, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	err = tx.QueryRowContext(ctx, `SELECT sequence,device_id,capture_id,integration_id,payload,manifest,delivery_origin,delivery_issuer,delivery_subject,upstream_capture_id,receipt,attempts FROM fragment_submissions WHERE integration_id=? AND state='queued' AND next_attempt<=? AND lease_until<=? ORDER BY next_attempt,sequence LIMIT 1`, w.config.IntegrationID, now, now).Scan(&j.sequence, &j.device, &j.capture, &j.integration, &j.payload, &j.manifest, &j.origin, &j.issuer, &j.subject, &j.upstream, &j.receipt, &j.attempts)
	if err != nil {
		return j, err
	}
	j.lease = token()
	_, err = tx.ExecContext(ctx, `UPDATE fragment_submissions SET lease_token=?,lease_until=?,code='delivering',updated_at=? WHERE sequence=?`, j.lease, now+120, time.Now().UTC().Format(timestampFormat), j.sequence)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func (w *FragmentWorker) authorized(ctx context.Context, j deliveryJob) (string, error) {
	var current int
	err := w.store.db.QueryRowContext(ctx, `SELECT 1 FROM fragment_submissions WHERE sequence=? AND state='queued' AND lease_token=? AND lease_until>?`, j.sequence, j.lease, time.Now().Unix()).Scan(&current)
	if err != nil {
		return "", err
	}
	var owner string
	if err := w.store.db.QueryRowContext(ctx, `SELECT user_id FROM activity_identity WHERE singleton=1`).Scan(&owner); err != nil {
		return "", err
	}
	devices, err := w.store.pairing.List(ctx, owner)
	if err != nil {
		return "", err
	}
	allowed := false
	for _, d := range devices {
		if d.ID == j.device && d.RevokedAt == nil && d.ExpiresAt.After(time.Now()) && slices.Contains(d.Scopes, scopeCapture) {
			allowed = true
		}
	}
	if !allowed {
		return "", &fecapture.Failure{Code: "device_authorization_required"}
	}
	var bound, provider, origin string
	err = w.store.db.QueryRowContext(ctx, `SELECT c.integration_id,i.provider,i.base_url FROM fragment_connections c JOIN integrations i ON i.id=c.integration_id WHERE c.device_id=?`, j.device).Scan(&bound, &provider, &origin)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	if err != nil {
		return "", err
	}
	if bound != j.integration || provider != "fragments-engine" || !fragmentOrigin(origin) {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	origin = strings.TrimRight(origin, "/")
	if j.manifest != "" && (j.origin != origin || j.issuer != w.config.IssuerURL || j.subject != w.config.Subject) {
		return "", &fecapture.Failure{Code: "connection_changed"}
	}
	return origin, nil
}
func (w *FragmentWorker) freeze(ctx context.Context, j *deliveryJob, origin string) error {
	if j.manifest != "" {
		return nil
	}
	var in fragmentSubmission
	if json.Unmarshal([]byte(j.payload), &in) != nil {
		return &fecapture.Failure{Code: "invalid_submission"}
	}
	j.upstream = "basalisk-" + token()
	j.origin = origin
	j.issuer = w.config.IssuerURL
	j.subject = w.config.Subject
	noteURL := w.config.PublicOrigin + "/api/fragment-submissions/" + strconv.FormatInt(j.sequence, 10)
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	title := "Frag note"
	provenance := map[string]any{"capture_id": in.CaptureID, "device_id": j.device, "draft_at": in.DraftAt, "submitted_at": in.SubmittedAt, "draft": in.Draft, "save_context": in.SaveContext}
	if in.ContextBindingID != "" {
		provenance["capture_context_binding_id"] = in.ContextBindingID
	}
	if in.Metadata != nil {
		if !validCaptureMetadata(in.Metadata) {
			return &fecapture.Failure{Code: "invalid_submission"}
		}
		title = "Captured note"
		if strings.TrimSpace(in.Metadata.Title) != "" {
			title = in.Metadata.Title
		}
		provenance["capture_metadata"] = in.Metadata
	}
	manifest := map[string]any{
		"schema_version": "fe.capture.v1", "capture_id": j.upstream, "captured_at": in.SubmittedAt, "principal_id": j.subject,
		"client":   map[string]string{"kind": "basalisk", "version": "1.0.0"},
		"source":   map[string]any{"submitted_url": noteURL, "canonical_url": noteURL, "provider": "basalisk", "source_registration_id": "basalisk-notes", "source_item_key": "note:" + j.upstream, "segment_key": "root", "canonicalizer": map[string]string{"adapter": "basalisk", "version": "1.0.0"}},
		"document": map[string]any{"title": title, "content": map[string]string{"format": "markdown", "body": in.Content}, "extensions": map[string]any{"provider.basalisk": provenance}},
		"tags":     tags, "annotations": []any{}, "media": []any{},
		"extraction": map[string]any{"adapter": "basalisk", "adapter_version": "1.0.0", "observed_capabilities": []string{"title", "body"}, "warnings": []any{}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	j.manifest = string(raw)
	return w.update(ctx, *j, `manifest=?,delivery_origin=?,delivery_issuer=?,delivery_subject=?,upstream_capture_id=?`, j.manifest, j.origin, j.issuer, j.subject, j.upstream)
}
func (w *FragmentWorker) update(ctx context.Context, j deliveryJob, set string, args ...any) error {
	args = append(args, j.sequence, j.lease)
	result, err := w.store.db.ExecContext(ctx, `UPDATE fragment_submissions SET `+set+` WHERE sequence=? AND lease_token=? AND state='queued'`, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("delivery lease lost")
	}
	return nil
}
func (w *FragmentWorker) failed(ctx context.Context, j deliveryJob, cause error) error {
	code, state := "storage_unavailable", "queued"
	var failure *fecapture.Failure
	if errors.As(cause, &failure) {
		code = failure.Code
		if !failure.Retryable {
			state = "needs_attention"
		}
	}
	delay := min(300, 5*(1<<min(j.attempts, 6)))
	return w.update(ctx, j, `state=?,code=?,attempts=attempts+1,next_attempt=?,lease_token='',lease_until=0,updated_at=?`, state, code, time.Now().Unix()+int64(delay), time.Now().UTC().Format(timestampFormat))
}
func (w *FragmentWorker) Once(ctx context.Context) error {
	j, err := w.claim(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	err = w.deliver(attemptCtx, &j)
	if err != nil { // Persist retry metadata even during orderly cancellation.
		saveCtx, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		return w.failed(saveCtx, j, err)
	}
	return nil
}
func (w *FragmentWorker) deliver(ctx context.Context, j *deliveryJob) error {
	origin, err := w.authorized(ctx, *j)
	if err != nil {
		return err
	}
	if err = w.freeze(ctx, j, origin); err != nil {
		return err
	}
	bearer, err := w.client.Exchange(ctx)
	if err != nil {
		return err
	}
	if _, err = w.authorized(ctx, *j); err != nil {
		return err
	}
	var receipt fecapture.Receipt
	if j.receipt == "" {
		// Set before the network request: a lost response may still mean FE saved it.
		if err = w.update(ctx, *j, `may_have_sent=1`); err != nil {
			return err
		}
		receipt, err = w.client.Accept(ctx, j.origin, bearer, j.upstream, []byte(j.manifest))
		if err != nil {
			return err
		}
		raw, _ := json.Marshal(receipt)
		j.receipt = string(raw)
		if err = w.update(ctx, *j, `receipt=?`, j.receipt); err != nil {
			return err
		}
	} else if json.Unmarshal([]byte(j.receipt), &receipt) != nil || !receipt.Valid(j.upstream) {
		return &fecapture.Failure{Code: "invalid_receipt"}
	}
	if _, err = w.authorized(ctx, *j); err != nil {
		return err
	}
	confirmed, err := w.client.Complete(ctx, j.origin, bearer, receipt)
	if err != nil {
		return err
	}
	return w.confirm(ctx, *j, confirmed)
}
func (w *FragmentWorker) confirm(ctx context.Context, j deliveryJob, r fecapture.Receipt) error {
	tx, err := w.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(timestampFormat)
	result, err := tx.ExecContext(ctx, `UPDATE fragment_submissions SET state='saved',code='confirmed',fragment_id=?,fragment_revision_id=?,lease_token='',lease_until=0,updated_at=? WHERE sequence=? AND lease_token=? AND state='queued'`, r.FragmentID, r.RevisionID, now, j.sequence, j.lease)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("delivery lease lost")
	}
	var in fragmentSubmission
	if json.Unmarshal([]byte(j.payload), &in) != nil {
		return errors.New("invalid submission")
	}
	if in.Draft != nil {
		receipt := artifactReceipt{CaptureID: in.CaptureID, DraftAt: in.DraftAt, SavedAt: now, FragmentID: r.FragmentID, FragmentRevisionID: &r.RevisionID, FragmentOrigin: j.origin, Draft: *in.Draft, SaveContext: in.SaveContext}
		raw, _ := json.Marshal(receipt)
		_, err = tx.ExecContext(ctx, `INSERT INTO companion_artifacts(device_id,capture_id,session_id,payload_hash,payload,received_at,source) VALUES(?,?,?,?,?,?,'basalisk.fe-response (server-confirmed)')`, j.device, "broker:"+j.upstream, in.Draft.SessionID, digest(string(raw)), string(raw), now)
		if err != nil {
			return fmt.Errorf("record confirmed association: %w", err)
		}
	}
	return tx.Commit()
}
