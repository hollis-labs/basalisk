package api

import "testing"

// The columns the pre-Substrate layout added later (delivery state, leases, provenance) are in
// the fresh schema with defaults that leave a pending note pending and never attest
// provenance the device did not earn.
func TestFreshSchemaDefaultsKeepPendingNotesPending(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.db.Exec(`INSERT INTO fragment_submissions(device_id,capture_id,integration_id,payload_hash,payload,state,code,created_at,updated_at)
 VALUES('mac','old-capture','fe','original-hash','{"content":"keep this note"}','queued','delivery_unavailable','2026-10-18T00:00:00Z','2026-10-18T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO companion_artifacts(device_id,capture_id,session_id,payload_hash,payload,received_at)
 VALUES('mac','old-artifact','session','hash','{}','2026-10-18T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	status, err := readSubmission(s.db.QueryRow("SELECT " + submissionColumns + " FROM fragment_submissions WHERE sequence=1"))
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "queued" || status.MayHaveSent || status.Delivering || status.FragmentID != "" || status.NextAttempt != 0 {
		t.Fatal("defaults changed a pending note", status)
	}
	var source string
	if err = s.db.QueryRow("SELECT source FROM companion_artifacts WHERE sequence=1").Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "frag.fe-response (device-reported)" {
		t.Fatal("defaults falsely attest server confirmation", source)
	}
}
