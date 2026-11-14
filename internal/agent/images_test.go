package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolResultPictures(t *testing.T) {
	// "aGk=" is base64 for two bytes; the content does not matter to the wire shape.
	keepImages("call-old", []Image{{MediaType: "image/jpeg", Data: "aGk="}})
	keepImages("call-new", []Image{{MediaType: "image/png", Data: "aGk="}})
	if err := validImages([]Image{{MediaType: "image/svg+xml", Data: "aGk="}}); err == nil {
		t.Fatal("only JPEG, PNG and WebP are taken")
	}
	if err := validImages([]Image{{MediaType: "image/png", Data: "not base64!"}}); err == nil {
		t.Fatal("a picture must be base64")
	}
	plain := resultContent("just text", nil)
	if s, ok := plain.(string); !ok || s != "just text" {
		t.Fatalf("a result without pictures stays a string: %#v", plain)
	}
	raw, _ := json.Marshal(wireBlock{Type: "tool_result", ToolUseID: "call-new", Content: resultContent("A look at the window.", imageStore.byCall["call-new"])})
	got := string(raw)
	for _, want := range []string{`"content":[{"type":"text","text":"A look at the window."}`, `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
}
