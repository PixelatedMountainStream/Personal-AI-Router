package main

import (
	"bytes"
	"io"
	"net/http"
	"regexp"

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

// classifyNotLoaded reports whether resp is the engine refusing a model that
// is not loaded. It reads the body only when it is 4 KB or less, and always
// leaves resp.Body byte-identical to what the upstream sent.
func classifyNotLoaded(engine string, resp *http.Response) bool {
	re := notLoadedPatterns[engine]
	if re == nil || resp.Body == nil {
		return false
	}
	orig := resp.Body
	buf, _ := io.ReadAll(io.LimitReader(orig, notLoadedMaxBody+1))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(buf), orig), orig}
	return len(buf) <= notLoadedMaxBody && re.Match(buf)
}
