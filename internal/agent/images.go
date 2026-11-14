package agent

import (
	"encoding/base64"
	"errors"
	"sync"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
)

// Pictures a tool hands back (a look at a window, a marked region of an artifact). The
// thread keeps a tool result as text, so a picture is held here beside it, by the call it
// answers, and put into the request when the thread is sent. Only the most recent few are
// sent: an older one stays in the thread as its line of text. They are not kept across a
// restart; the agent looks again.

// Image is one picture, as the Messages API takes it.
type Image struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"` // base64
}

const (
	maxImageBytes  = 1500 << 10 // of base64
	maxCallImages  = 3
	keptImageCalls = 12 // calls whose pictures are held
	sentImageCalls = 3  // calls whose pictures go with a request
)

var imageStore = struct {
	sync.Mutex
	byCall map[string][]Image
	order  []string
}{byCall: map[string][]Image{}}

// validImages checks what the page sent: a few pictures, of a kind the model takes, not huge.
func validImages(list []Image) error {
	if len(list) > maxCallImages {
		return errors.New("too many pictures in one result")
	}
	for _, im := range list {
		switch im.MediaType {
		case "image/jpeg", "image/png", "image/webp":
		default:
			return errors.New("a picture must be JPEG, PNG or WebP")
		}
		if len(im.Data) == 0 || len(im.Data) > maxImageBytes {
			return errors.New("a picture is empty or too large")
		}
		if _, err := base64.StdEncoding.DecodeString(im.Data); err != nil {
			return errors.New("a picture is not base64")
		}
	}
	return nil
}

func keepImages(callID string, list []Image) {
	if len(list) == 0 {
		return
	}
	imageStore.Lock()
	defer imageStore.Unlock()
	imageStore.byCall[callID] = list
	imageStore.order = append(imageStore.order, callID)
	for len(imageStore.order) > keptImageCalls {
		delete(imageStore.byCall, imageStore.order[0])
		imageStore.order = imageStore.order[1:]
	}
}

// shownImages picks the pictures that go with a request: those of the last few tool results
// in the thread that have any.
func shownImages(in []llmtypes.ChatMessage) map[string][]Image {
	out := map[string][]Image{}
	imageStore.Lock()
	defer imageStore.Unlock()
	if len(imageStore.byCall) == 0 {
		return out
	}
	for i := len(in) - 1; i >= 0 && len(out) < sentImageCalls; i-- {
		for j := len(in[i].ContentBlocks) - 1; j >= 0 && len(out) < sentImageCalls; j-- {
			b := in[i].ContentBlocks[j]
			if b.Type != "tool_result" {
				continue
			}
			if list := imageStore.byCall[b.ToolUseID]; len(list) > 0 {
				out[b.ToolUseID] = list
			}
		}
	}
	return out
}

// resultContent is a tool result as it is sent: its text alone, or its text and pictures.
func resultContent(text string, images []Image) any {
	if len(images) == 0 {
		return text
	}
	blocks := []wireBlock{{Type: "text", Text: text}}
	for _, im := range images {
		blocks = append(blocks, wireBlock{Type: "image", Source: &wireSource{Type: "base64", MediaType: im.MediaType, Data: im.Data}})
	}
	return blocks
}
