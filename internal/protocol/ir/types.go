package ir

import "encoding/json"

type Protocol string

const (
	OpenAIChat      Protocol = "openai-chat"
	OpenAIResponses Protocol = "openai-responses"
	Anthropic       Protocol = "anthropic-messages"
	Gemini          Protocol = "gemini-generate-content"
)

type Request struct {
	ID             string                     `json:"id,omitempty"`
	Source         Protocol                   `json:"source_protocol,omitempty"`
	Model          string                     `json:"model"`
	Instructions   []ContentBlock             `json:"instructions,omitempty"`
	Messages       []Message                  `json:"messages,omitempty"`
	Tools          []Tool                     `json:"tools,omitempty"`
	ToolChoice     *ToolChoice                `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat            `json:"response_format,omitempty"`
	Sampling       SamplingOptions            `json:"sampling,omitempty"`
	Stream         bool                       `json:"stream,omitempty"`
	Metadata       map[string]any             `json:"metadata,omitempty"`
	Extensions     map[string]json.RawMessage `json:"extensions,omitempty"`
}

type Message struct {
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type ContentBlock struct {
	Type       string          `json:"type"`
	Source     Protocol        `json:"source_protocol,omitempty"`
	Text       string          `json:"text,omitempty"`
	MediaType  string          `json:"media_type,omitempty"`
	URL        string          `json:"url,omitempty"`
	Data       string          `json:"data,omitempty"`
	FileID     string          `json:"file_id,omitempty"`
	Filename   string          `json:"filename,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	SourceType string          `json:"source_type,omitempty"`
	Extension  json.RawMessage `json:"extension,omitempty"`
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type ResponseFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name,omitempty"`
	Schema json.RawMessage `json:"schema,omitempty"`
	Strict bool            `json:"strict,omitempty"`
}

type SamplingOptions struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	MaxOutputTokens  *int     `json:"max_output_tokens,omitempty"`
	Stop             []string `json:"stop,omitempty"`
	ReasoningEffort  string   `json:"reasoning_effort,omitempty"`
	ReasoningEnabled *bool    `json:"reasoning_enabled,omitempty"`
}

type Response struct {
	ID         string         `json:"id,omitempty"`
	Model      string         `json:"model,omitempty"`
	Messages   []Message      `json:"messages,omitempty"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      Usage          `json:"usage,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type Usage struct {
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	CachedTokens    int `json:"cached_tokens,omitempty"`
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
	TotalTokens     int `json:"total_tokens,omitempty"`
}

type Event struct {
	Type       string          `json:"type"`
	Sequence   int             `json:"sequence,omitempty"`
	ResponseID string          `json:"response_id,omitempty"`
	Model      string          `json:"model,omitempty"`
	Role       string          `json:"role,omitempty"`
	MessageID  string          `json:"message_id,omitempty"`
	Index      int             `json:"index,omitempty"`
	Block      *ContentBlock   `json:"block,omitempty"`
	Delta      string          `json:"delta,omitempty"`
	Arguments  string          `json:"arguments,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
	StopReason string          `json:"stop_reason,omitempty"`
	Error      *Error          `json:"error,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
}

type Error struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	StatusCode int    `json:"status_code,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Action   string `json:"action"`
}
