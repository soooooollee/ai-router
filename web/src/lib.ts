export function compact(n = 0) {
  return Intl.NumberFormat("zh-CN", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(n);
}

export function protocolName(protocol: string) {
  return (
    ({
      "openai-chat": "OpenAI Chat",
      "openai-responses": "OpenAI Responses",
      "anthropic-messages": "Anthropic Messages",
      "gemini-generate-content": "Gemini",
    } as Record<string, string>)[protocol] || protocol
  );
}
