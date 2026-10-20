package account

import (
	"testing"
	"time"

	"github.com/hollis-labs/basalisk/internal/activity"
)

func TestArtifactBindingSurvivesContextSwitchAndRetry(t *testing.T) {
	s, c := newStore(t)
	c.call("POST", "/api/setup", setupBody(), 200)
	session := command(c, activity.Command{RequestID: "artifact-start-0001", Action: "start", Title: "Draft", Project: "Original"}, 200)
	h := s.CompanionHandler()
	device := pairDevice(t, c, h, nil)
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	context := artifactContext{SchemaVersion: 1, Origin: "http://127.0.0.1:18080", DeviceID: device.Device.ID, SessionID: session.ID, ContextID: session.Context.ID, Project: "Original", ContextAsOf: stamp, PublishedAt: stamp, ExpiresAt: now.Add(12 * time.Second).Format(time.RFC3339Nano), HistoryRefs: []string{}}
	receipt := artifactReceipt{CaptureID: "artifact-capture-0001", DraftAt: stamp, SavedAt: stamp, FragmentID: "confirmed-fragment", FragmentOrigin: "https://fragments.example.test", Draft: context}
	changed := command(c, activity.Command{RequestID: "artifact-change-001", Action: "context", SessionID: session.ID, Revision: session.Revision, Project: "Next"}, 200)
	command(c, activity.Command{RequestID: "artifact-end-000001", Action: "end", SessionID: session.ID, Revision: changed.Revision}, 200)
	// A late save/retry still belongs to the context frozen when the draft began.
	for range 2 {
		nativeCall(t, h, device.Token, "POST", "/companion/artifacts", receipt, 200)
	}
	var items int
	if err := s.db.QueryRow("SELECT count(*) FROM companion_artifacts WHERE session_id=?", session.ID).Scan(&items); err != nil || items != 1 {
		t.Fatal("receipt retry duplicated association", err, items)
	}
	page := responseAs[struct {
		Items []struct {
			Receipt artifactReceipt `json:"receipt"`
		} `json:"items"`
	}](t, c.call("GET", "/api/activity/sessions/"+session.ID+"/artifacts", nil, 200))
	if len(page.Items) != 1 || page.Items[0].Receipt.Draft.ContextID != session.Context.ID || page.Items[0].Receipt.FragmentRevisionID != nil {
		t.Fatal("frozen context or unknown revision lost")
	}
	receipt.FragmentID = "different-fragment"
	nativeCall(t, h, device.Token, "POST", "/companion/artifacts", receipt, 409)
	receipt.CaptureID = "artifact-capture-0002"
	receipt.Draft.ContextID = changed.Context.ID
	nativeCall(t, h, device.Token, "POST", "/companion/artifacts", receipt, 400)
	c.call("DELETE", "/api/devices/"+device.Device.ID, nil, 200)
	nativeCall(t, h, device.Token, "POST", "/companion/artifacts", receipt, 401)
}
