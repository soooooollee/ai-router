import http from "node:http";

const server = http.createServer(async (request, response) => {
  if (request.method === "GET" && request.url === "/release") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ tag_name: "v0.2.4" }));
    return;
  }
  if (request.method === "GET" && request.url.endsWith("/models")) {
    response.setHeader("content-type", "application/json");
    response.end(
      JSON.stringify({
        object: "list",
        data: [{ id: "mock-model", object: "model" }],
      }),
    );
    return;
  }
  if (
    request.method === "POST" &&
    request.url.endsWith("/anthropic/v1/messages")
  ) {
    if (request.headers.authorization !== "Bearer e2e-mimo-key") {
      response.statusCode = 401;
      response.setHeader("content-type", "application/json");
      response.end(JSON.stringify({ error: { message: "bad auth" } }));
      return;
    }
    let body = "";
    for await (const chunk of request) body += chunk;
    const input = JSON.parse(body);
    if (input.stream && input.tools) {
      if (input.tool_choice?.type !== "auto") {
        response.statusCode = 400;
        response.end(JSON.stringify({ error: { message: "invalid tool_choice" } }));
        return;
      }
      response.setHeader("content-type", "text/event-stream");
      response.write('event: message_start\ndata: {"type":"message_start","message":{"id":"msg-e2e","type":"message","role":"assistant","model":"mimo-v2.5","usage":{"input_tokens":1,"output_tokens":0}}}\n\n');
      response.write('event: content_block_start\ndata: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool-call-1","name":"apply_patch","input":{}}}\n\n');
      response.write('event: content_block_delta\ndata: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\\"input\\":\\"*** Begin Patch\\\\n*** End Patch\\"}"}}\n\n');
      response.write('event: content_block_stop\ndata: {"type":"content_block_stop","index":0}\n\n');
      response.write('event: message_delta\ndata: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}\n\n');
      response.end('event: message_stop\ndata: {"type":"message_stop"}\n\n');
      return;
    }
    response.setHeader("content-type", "application/json");
    response.end(
      JSON.stringify({
        id: "mock-anthropic-response",
        type: "message",
        role: "assistant",
        model: "mimo-v2.5",
        content: [{ type: "text", text: "hello from MiMo" }],
        stop_reason: "end_turn",
        usage: { input_tokens: 3, output_tokens: 3 },
      }),
    );
    return;
  }
  if (request.method === "POST" && request.url.endsWith("/chat/completions")) {
    let body = "";
    for await (const chunk of request) body += chunk;
    const input = JSON.parse(body);
    if (input.tools && input.reasoning_effort) {
      response.statusCode = 400;
      response.end(
        JSON.stringify({
          error: {
            message:
              "Function tools with reasoning_effort are not supported",
          },
        }),
      );
      return;
    }
    if (input.stream && input.tools) {
      const name = input.tools[0]?.function?.name || "airoute_probe";
      const argumentsValue = name === "apply_patch"
        ? JSON.stringify({ input: "*** Begin Patch\n*** End Patch" })
        : JSON.stringify({ city: "Beijing" });
      response.setHeader("content-type", "text/event-stream");
      response.write(
        `data: ${JSON.stringify({ id: "mock-stream", model: "mock-model", choices: [{ index: 0, delta: { role: "assistant", tool_calls: [{ index: 0, id: "mock-probe", type: "function", function: { name, arguments: argumentsValue } }] }, finish_reason: "tool_calls" }] })}\n\n`,
      );
      response.end("data: [DONE]\n\n");
      return;
    }
    if (input.stream) {
      response.setHeader("content-type", "text/event-stream");
      response.write(
        `data: ${JSON.stringify({ id: "mock-stream", model: "mock-model", choices: [{ index: 0, delta: { role: "assistant" }, finish_reason: null }] })}\n\n`,
      );
      response.write(
        `data: ${JSON.stringify({ id: "mock-stream", model: "mock-model", choices: [{ index: 0, delta: { content: "hello from mock" }, finish_reason: null }] })}\n\n`,
      );
      response.write(
        `data: ${JSON.stringify({ id: "mock-stream", model: "mock-model", choices: [{ index: 0, delta: {}, finish_reason: "stop" }] })}\n\n`,
      );
      response.end("data: [DONE]\n\n");
      return;
    }
    response.setHeader("content-type", "application/json");
    if (input.tools) {
      const name = input.tools[0]?.function?.name || "airoute_probe";
      response.end(
        JSON.stringify({
          id: "mock-tool-response",
          object: "chat.completion",
          model: "mock-model",
          choices: [
            {
              index: 0,
              message: {
                role: "assistant",
                tool_calls: [
                  {
                    id: "mock-probe",
                    type: "function",
                    function: { name, arguments: JSON.stringify({ city: "Beijing" }) },
                  },
                ],
              },
              finish_reason: "tool_calls",
            },
          ],
        }),
      );
      return;
    }
    response.end(
      JSON.stringify({
        id: "mock-response",
        object: "chat.completion",
        model: "mock-model",
        choices: [
          {
            index: 0,
            message: { role: "assistant", content: "hello from mock" },
            finish_reason: "stop",
          },
        ],
        usage: { prompt_tokens: 3, completion_tokens: 3, total_tokens: 6 },
      }),
    );
    return;
  }
  response.statusCode = 404;
  response.end("not found");
});

server.listen(19090, "127.0.0.1", () => console.log("mock provider ready"));
