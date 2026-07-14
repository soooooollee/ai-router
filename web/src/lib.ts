import { currentLocale } from "./app/i18n";
import type { RouteConfig } from "./types";

export function compact(n = 0) {
  return Intl.NumberFormat(currentLocale(), {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(n);
}

export function protocolName(protocol: string) {
  return (
    (
      {
        "openai-chat": "OpenAI Chat",
        "openai-responses": "OpenAI Responses",
        "anthropic-messages": "Anthropic Messages",
        "gemini-generate-content": "Gemini",
      } as Record<string, string>
    )[protocol] || protocol
  );
}

export type ApplicationRouteOption = {
  alias: string;
  protocol?: string;
};

export function applicationProtocol(applicationID: string) {
  if (applicationID === "codex") return "openai-responses";
  if (applicationID === "mimo-code") return "openai-chat";
  if (applicationID === "claude-code" || applicationID === "claude-app") {
    return "anthropic-messages";
  }
  return "";
}

export function applicationRouteOptions(
  routes: RouteConfig[],
  applicationID: string,
) {
  const expectedProtocol = applicationProtocol(applicationID);
  const options = new Map<string, ApplicationRouteOption>();
  for (const route of routes) {
    const alias = route.match.model?.trim() || "";
    if (!alias || /[?*\[]/.test(alias)) continue;
    const protocol = route.match.protocol;
    if (protocol && expectedProtocol && protocol !== expectedProtocol) continue;
    const existing = options.get(alias);
    if (!existing || (!existing.protocol && protocol)) {
      options.set(alias, { alias, protocol });
    }
  }
  return [...options.values()];
}

export function applicationGatewayURL(
  configured: unknown,
  currentGateway: string,
) {
  if (typeof configured !== "string" || !configured.trim()) {
    return currentGateway;
  }
  const normalized = configured.trim().replace(/\/+$/, "");
  if (
    normalized === "http://127.0.0.1:8080" ||
    normalized === "http://localhost:8080"
  ) {
    return currentGateway;
  }
  return configured;
}

export function routeIdentifier(model: string) {
  return (
    model
      .trim()
      .split("/")
      .pop()
      ?.toLowerCase()
      .replace(/[^a-z0-9._-]+/g, "-")
      .replace(/^-+|-+$/g, "") || "model"
  );
}

export function routeIDTimestamp(date = new Date()) {
  const part = (value: number, length = 2) =>
    String(value).padStart(length, "0");
  return [
    part(date.getFullYear(), 4),
    part(date.getMonth() + 1),
    part(date.getDate()),
    part(date.getHours()),
    part(date.getMinutes()),
    part(date.getSeconds()),
    part(date.getMilliseconds(), 3),
  ].join("");
}

export function generatedRouteID(
  model: string,
  protocol: string,
  timestamp = routeIDTimestamp(),
) {
  return `${routeIdentifier(model)}-${routeIdentifier(protocol)}-${timestamp}`;
}

export function providerModelLabel(
  providerName: string | undefined,
  providerID: string,
  model: string,
) {
  const service = (providerName || providerID).trim();
  const modelName = model.trim();
  if (!service) return modelName;
  if (!modelName) return service;
  return service.toLocaleLowerCase() === modelName.toLocaleLowerCase()
    ? modelName
    : `${service} / ${modelName}`;
}

type ConfigTarget = { provider?: string; [key: string]: unknown };
type ConfigTargetList = { targets?: ConfigTarget[]; [key: string]: unknown };
type EditableConfig = {
  providers?: { id?: string; [key: string]: unknown }[];
  routes?: ConfigTargetList[];
  default_route?: ConfigTargetList;
  [key: string]: unknown;
};

export function removeProviderReferences(
  document: EditableConfig,
  providerID: string,
) {
  document.providers = (document.providers || []).filter(
    (provider) => provider.id !== providerID,
  );
  document.routes = (document.routes || [])
    .map((route) => ({
      ...route,
      targets: (route.targets || []).filter(
        (target) => target.provider !== providerID,
      ),
    }))
    .filter((route) => route.targets.length > 0);
  if (document.default_route) {
    const targets = (document.default_route.targets || []).filter(
      (target) => target.provider !== providerID,
    );
    if (targets.length) document.default_route = { ...document.default_route, targets };
    else delete document.default_route;
  }
  return document;
}
