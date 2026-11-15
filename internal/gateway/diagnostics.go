package gateway

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/soooooollee/ai-router/internal/config"
	"github.com/soooooollee/ai-router/internal/protocol/common"
	"github.com/soooooollee/ai-router/internal/protocol/ir"
)

var errLossyConversion = errors.New("request requires a lossy protocol conversion")

// strictConversionLoss reports the diagnostics that make a conversion
// unacceptable under conversion.unsupported_fields: strict. Native
// (same-protocol) conversions are always allowed, and information can be lost
// either while decoding the client request or while encoding the provider
// payload, so every diagnostic set produced for the request is considered.
func strictConversionLoss(c *config.Config, source, target ir.Protocol, sets ...[]ir.Diagnostic) []ir.Diagnostic {
	if source == target || c.Conversion.UnsupportedFields != "strict" {
		return nil
	}
	var lossy []ir.Diagnostic
	for _, set := range sets {
		for _, d := range set {
			if common.IsLossy([]ir.Diagnostic{d}) {
				lossy = append(lossy, d)
			}
		}
	}
	return lossy
}

// lossyConversionError keeps errors.Is(errLossyConversion) working while
// naming the offending fields so the 422 response explains what would be lost.
func lossyConversionError(diagnostics []ir.Diagnostic) error {
	var parts []string
	seen := map[string]bool{}
	for _, d := range diagnostics {
		label := d.Code
		if d.Path != "" {
			label += " (" + d.Path + ")"
		}
		if d.Action != "" {
			label += " [" + d.Action + "]"
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		parts = append(parts, label)
	}
	if len(parts) == 0 {
		return errLossyConversion
	}
	return fmt.Errorf("%w: %s", errLossyConversion, strings.Join(parts, ", "))
}

func customToolNames(request *ir.Request) map[string]bool {
	result := map[string]bool{}
	if request == nil {
		return result
	}
	for _, tool := range request.Tools {
		if tool.Type == "custom" && tool.Name != "" {
			result[tool.Name] = true
		}
	}
	return result
}

// Claude App only accepts Claude-shaped model identifiers in its discovery
// profile. The application adapter encodes the real AI Router alias as hex so
// the gateway can restore it before normal route matching.
func decodeClaudeAppRouteModel(model string) string {
	const prefix = "anthropic/claude-ccr-h"
	if !strings.HasPrefix(strings.ToLower(model), prefix) {
		return model
	}
	decoded, err := hex.DecodeString(model[len(prefix):])
	if err != nil || len(decoded) == 0 {
		return model
	}
	return string(decoded)
}

func bridgeCodexCustomTools(request *ir.Request) {
	if request == nil {
		return
	}
	for index := range request.Tools {
		if request.Tools[index].Type == "custom" {
			request.Tools[index].Type = "function"
		}
	}
	for messageIndex := range request.Messages {
		for blockIndex := range request.Messages[messageIndex].Content {
			block := &request.Messages[messageIndex].Content[blockIndex]
			if block.SourceType == "custom" {
				block.SourceType = "function"
			}
		}
	}
}
