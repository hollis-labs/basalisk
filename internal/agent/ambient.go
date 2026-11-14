package agent

import (
	"context"
	"strings"
	"sync"
)

// Ambient knowledge: what the assistant should know about the state of things without being
// told by the owner or looking it up (the owner's mail: what needs them, what answers came).
// A source says how things stand now; the owner's message carries it in an <around> block
// when it differs from what the thread was last told, so the thread is not repeated to.

// AmbientSource says how something stands now, in a few lines, or "".
type AmbientSource func(ctx context.Context) string

var ambient = struct {
	sync.Mutex
	sources []AmbientSource
	told    map[string]string // thread -> the text last put in a message
}{told: map[string]string{}}

// AddAmbient gives the assistant a source of ambient knowledge. Call before serving.
func (rt *Runtime) AddAmbient(src AmbientSource) {
	ambient.Lock()
	ambient.sources = append(ambient.sources, src)
	ambient.Unlock()
}

func (rt *Runtime) ambientBlock(ctx context.Context, thread string) string {
	ambient.Lock()
	sources := append([]AmbientSource{}, ambient.sources...)
	ambient.Unlock()
	var parts []string
	for _, src := range sources {
		if text := strings.TrimSpace(src(ctx)); text != "" {
			parts = append(parts, text)
		}
	}
	text := strings.Join(parts, "\n\n")
	ambient.Lock()
	same := ambient.told[thread] == text
	ambient.told[thread] = text
	ambient.Unlock()
	if same || text == "" {
		return ""
	}
	return "<around>\n" + text + "\n</around>"
}
