package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"time"

	"nvpair-shared/engines"
)

// notLoadedMaxBody bounds how much of an error body is read to classify it.
const notLoadedMaxBody = 4096

// notLoadedPatterns match (Ollama's quotes arrive JSON-escaped) each engine's "that model is not loaded" refusal.
var notLoadedPatterns = map[string]*regexp.Regexp{
	engines.NameLMStudio: regexp.MustCompile(`(?i)No models loaded`),
	engines.NameOllama:   regexp.MustCompile(`model \\?"[^"]*\\?" not found`),
	engines.NameLlamaCPP: regexp.MustCompile(`(?i)model .* not (found|loaded)`),
}

// errReader replays a read failure after the bytes already read.
type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

// classifyNotLoaded reports whether resp is the engine refusing a model that
// is not loaded. It reads the body only when it is 4 KB or less, and leaves
// resp.Body byte-identical to what the upstream sent. The read is abandoned
// when ctx ends or budget elapses, so a silent upstream cannot hold the
// request's reservation before commit; the body then carries that error. A
// read that fails part-way never classifies, and its failure is replayed.
func classifyNotLoaded(ctx context.Context, engine string, resp *http.Response, budget time.Duration) bool {
	re := notLoadedPatterns[engine]
	if re == nil || resp.Body == nil {
		return false
	}
	orig := resp.Body
	type result struct {
		buf []byte
		err error
	}
	ch := make(chan result, 1) // buffered: the reader can always finish
	go func() {
		buf, err := io.ReadAll(io.LimitReader(orig, notLoadedMaxBody+1))
		ch <- result{buf, err}
	}()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	var res result
	select {
	case res = <-ch:
	case <-ctx.Done():
		res.err = ctx.Err()
	case <-timer.C:
		res.err = errFirstBodyTimeout
	}
	tail := io.Reader(orig)
	if res.err != nil {
		// Closing unblocks a still-pending read; the caller sees the failure.
		_ = orig.Close()
		tail = errReader{res.err}
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(res.buf), tail), orig}
	return res.err == nil && len(res.buf) <= notLoadedMaxBody && re.Match(res.buf)
}
