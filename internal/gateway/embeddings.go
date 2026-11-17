package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/soooooollee/ai-router/internal/clientauth"
	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
	providerprofile "github.com/soooooollee/ai-router/internal/provider"
	"github.com/soooooollee/ai-router/internal/routing"
	"github.com/soooooollee/ai-router/internal/secure"
	"github.com/soooooollee/ai-router/internal/tokencount"
)

// embeddingRequest is the OpenAI embedding shape the gateway accepts.
type embeddingRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format,omitempty"`
	Dimensions     int             `json:"dimensions,omitempty"`
	User           string          `json:"user,omitempty"`
}

// serveEmbeddings proxies /v1/embeddings. Embedding payloads carry no message
// history, so they bypass the IR entirely: the request is rewritten for the
// target provider and the response is normalized back to the OpenAI shape.
func (g *Gateway) serveEmbeddings(w http.ResponseWriter, r *http.Request, c *config.Config, id string, principal clientauth.Principal) {
	if r.Method != http.MethodPost {
		errorOut(w, ir.OpenAIChat, http.StatusMethodNotAllowed, "invalid_request", "method not allowed", id)
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		errorOut(w, ir.OpenAIChat, http.StatusUnsupportedMediaType, "invalid_request", "content-type must be application/json", id)
		return
	}
	g.semMu.RLock()
	sem := g.sem
	g.semMu.RUnlock()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		errorOut(w, ir.OpenAIChat, http.StatusTooManyRequests, "rate_limited", "gateway concurrency limit reached", id)
		return
	}
	body, err := readRequestBody(r, c.Server.MaxBodySize)
	if err != nil {
		errorOut(w, ir.OpenAIChat, http.StatusBadRequest, "invalid_request", err.Error(), id)
		return
	}
	var input embeddingRequest
	if err := json.Unmarshal(body, &input); err != nil {
		errorOut(w, ir.OpenAIChat, http.StatusBadRequest, "invalid_request", "invalid JSON body", id)
		return
	}
	input.Model = strings.TrimSpace(input.Model)
	if input.Model == "" {
		errorOut(w, ir.OpenAIChat, http.StatusBadRequest, "invalid_request", "model is required", id)
		return
	}
	texts, err := embeddingInputs(input.Input)
	if err != nil {
		errorOut(w, ir.OpenAIChat, http.StatusBadRequest, "invalid_request", err.Error(), id)
		return
	}
	decision, err := routing.Resolve(c, routing.Input{Request: &ir.Request{Model: input.Model}, Protocol: ir.OpenAIChat, Headers: map[string]string{}})
	if err != nil {
		errorOut(w, ir.OpenAIChat, http.StatusNotFound, "not_found", err.Error(), id)
		return
	}
	var lastStatus int
	var lastBody []byte
	for _, target := range g.orderTargets(c, decision.Targets) {
		if target.Provider.Protocol == ir.Anthropic {
			lastStatus, lastBody = http.StatusBadRequest, []byte(`{"error":{"message":"Anthropic Messages has no embeddings endpoint"}}`)
			continue
		}
		payload, endpoint, err := embeddingPayload(target.Provider, target.Model, input, texts)
		if err != nil {
			lastStatus, lastBody = http.StatusBadRequest, []byte(fmt.Sprintf(`{"error":{"message":%q}}`, err.Error()))
			continue
		}
		status, raw, err := g.doEmbedding(r.Context(), target.Provider, endpoint, payload)
		if err != nil {
			g.noteProviderResult(c, target.Provider.ID, false)
			lastStatus, lastBody = http.StatusBadGateway, []byte(fmt.Sprintf(`{"error":{"message":%q}}`, redactSnippet([]byte(err.Error()))))
			continue
		}
		if status < 200 || status >= 300 {
			g.noteProviderResult(c, target.Provider.ID, false)
			lastStatus, lastBody = status, raw
			continue
		}
		g.noteProviderResult(c, target.Provider.ID, true)
		normalized, err := normalizeEmbeddingResponse(target.Provider.Protocol, input.Model, texts, raw)
		if err != nil {
			errorOut(w, ir.OpenAIChat, http.StatusBadGateway, "upstream_unavailable", "upstream returned an unreadable embedding response", id)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.Header().Set("x-airoute-provider-id", target.Provider.ID)
		w.Header().Set("x-airoute-model", target.Model)
		w.WriteHeader(status)
		_, _ = w.Write(normalized)
		return
	}
	if lastStatus == 0 {
		lastStatus = http.StatusBadGateway
	}
	if lastBody == nil {
		lastBody = []byte(`{"error":{"message":"no embedding provider accepted the request"}}`)
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(lastStatus)
	_, _ = w.Write(lastBody)
}

// embeddingInputs accepts the string, string-array and token-array forms.
func embeddingInputs(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, errors.New("input is required")
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if strings.TrimSpace(single) == "" {
			return nil, errors.New("input cannot be empty")
		}
		return []string{single}, nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		if len(list) == 0 {
			return nil, errors.New("input cannot be empty")
		}
		return list, nil
	}
	return nil, errors.New("input must be a string or an array of strings")
}

func embeddingPayload(p config.Provider, model string, input embeddingRequest, texts []string) ([]byte, string, error) {
	base := strings.TrimRight(p.BaseURL, "/")
	var payload any
	var endpoint string
	if p.Protocol == ir.Gemini {
		if !strings.Contains(base, "/v1beta") {
			base += "/v1beta"
		}
		if len(texts) == 1 {
			payload = map[string]any{"model": "models/" + model, "content": map[string]any{"parts": []any{map[string]any{"text": texts[0]}}}}
			endpoint = base + "/models/" + model + ":embedContent"
		} else {
			requests := make([]any, 0, len(texts))
			for _, text := range texts {
				requests = append(requests, map[string]any{"model": "models/" + model, "content": map[string]any{"parts": []any{map[string]any{"text": text}}}})
			}
			payload = map[string]any{"requests": requests}
			endpoint = base + "/models/" + model + ":batchEmbedContents"
		}
	} else {
		body := map[string]any{"model": model, "input": texts}
		if len(texts) == 1 {
			body["input"] = texts[0]
		}
		if input.EncodingFormat != "" {
			body["encoding_format"] = input.EncodingFormat
		}
		if input.Dimensions > 0 {
			body["dimensions"] = input.Dimensions
		}
		if input.User != "" {
			body["user"] = input.User
		}
		payload = body
		endpoint = base + "/embeddings"
		if !strings.HasSuffix(base, "/v1") {
			endpoint = base + "/v1/embeddings"
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}
	return encoded, endpoint, nil
}

func (g *Gateway) doEmbedding(ctx context.Context, p config.Provider, endpoint string, payload []byte) (int, []byte, error) {
	if err := secure.ValidatePublicTarget(ctx, endpoint, p.AllowPrivateURL); err != nil {
		return 0, nil, err
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("accept", "application/json")
	if traceparent := traceparentFromContext(ctx); traceparent != "" {
		request.Header.Set(traceparentHeader, traceparent)
	}
	providerprofile.ApplyAuthenticationHeaders(request.Header, p)
	for key, value := range p.Headers {
		request.Header.Set(key, value)
	}
	client := g.Client
	if !p.AllowPrivateURL {
		client = g.RestrictedClient
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return response.StatusCode, nil, err
	}
	return response.StatusCode, raw, nil
}

// normalizeEmbeddingResponse converts a provider response into the OpenAI
// embedding shape so any OpenAI client can read it.
func normalizeEmbeddingResponse(protocolName ir.Protocol, model string, texts []string, raw []byte) ([]byte, error) {
	if protocolName != ir.Gemini {
		return raw, nil
	}
	var gemini struct {
		Embedding *struct {
			Values []float64 `json:"values"`
		} `json:"embedding"`
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.Unmarshal(raw, &gemini); err != nil {
		return nil, err
	}
	vectors := make([][]float64, 0, len(texts))
	if gemini.Embedding != nil {
		vectors = append(vectors, gemini.Embedding.Values)
	}
	for _, item := range gemini.Embeddings {
		vectors = append(vectors, item.Values)
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("gemini returned %d embeddings for %d inputs", len(vectors), len(texts))
	}
	data := make([]any, 0, len(vectors))
	for index, values := range vectors {
		data = append(data, map[string]any{"object": "embedding", "index": index, "embedding": values})
	}
	tokens := 0
	for _, text := range texts {
		tokens += tokencount.Heuristic{}.Count(&ir.Request{Messages: []ir.Message{{Role: "user", Content: []ir.ContentBlock{{Type: "text", Text: text}}}}}).InputTokens
	}
	return json.Marshal(map[string]any{
		"object": "list",
		"data":   data,
		"model":  model,
		"usage":  map[string]any{"prompt_tokens": tokens, "total_tokens": tokens},
	})
}
