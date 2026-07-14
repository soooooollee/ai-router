import http from "node:http";

const server = http.createServer(async (request, response) => {
  if (request.method === "GET" && request.url === "/release") {
    response.setHeader("content-type", "application/json");
    response.end(JSON.stringify({ tag_name: "v0.2.3" }));
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
  if (request.method === "POST" && request.url.endsWith("/chat/completions")) {
    let body = "";
    for await (const chunk of request) body += chunk;
    const input = JSON.parse(body);
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
