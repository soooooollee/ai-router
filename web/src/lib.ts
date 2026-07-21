import { currentLocale } from "./app/i18n";
import type { Provider, RouteConfig } from "./types";

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

export function providerProtocolName(
	provider: Pick<Provider, "protocol" | "codex_integration" | "codex_compatibility" | "compatibility_mode" | "reasoning_with_tools">,
) {
  const base = protocolName(provider.protocol);
  if (provider.codex_integration === "direct") {
    return `${base}（Codex 官方直连）`;
  }
  if (provider.codex_compatibility === "full") {
    return provider.codex_integration === "compatibility"
      ? `${base}（Codex 经 AI Router 完整兼容）`
      : `${base}（Codex 完整兼容）`;
  }
	return provider.codex_integration === "compatibility" ||
    provider.compatibility_mode === "codex-chat" ||
    provider.compatibility_mode === "codex-responses" ||
    provider.reasoning_with_tools === "disabled"
    ? `${base}（Codex CLI / ChatGPT App 经 AI Router 兼容）`
    : base;
}

export function providerCodexCompatibilityName(
  provider: Pick<Provider, "codex_integration" | "codex_compatibility" | "compatibility_mode" | "reasoning_with_tools">,
) {
  if (provider.codex_compatibility === "full") {
    return provider.codex_integration === "direct"
      ? "Codex 官方直连完整兼容"
      : provider.codex_integration === "compatibility"
        ? "Codex 经 AI Router 完整兼容"
        : "Codex 完整兼容";
  }
  if (provider.codex_compatibility === "unverified") {
    return provider.codex_integration === "compatibility"
      ? "Codex 经 AI Router 待验证"
      : "Codex 尚未验证";
  }
  if (provider.codex_compatibility === "incompatible") return "Codex 不兼容";
  if (provider.codex_compatibility === "unavailable") return "Codex 检测未完成";
  if (
    provider.codex_compatibility === "degraded" ||
    provider.codex_integration === "compatibility" ||
    provider.compatibility_mode ||
    provider.reasoning_with_tools === "disabled"
  ) {
    return provider.codex_integration === "compatibility"
      ? "Codex CLI / ChatGPT App 经 AI Router 兼容"
      : "Codex 可用";
  }
  return "";
}

export type ApplicationRouteOption = {
  alias: string;
  protocol?: string;
  compatibility_mode?: "codex-chat" | "codex-responses";
  integration_mode?: "direct" | "passthrough" | "compatibility";
  codex_compatibility?: Provider["codex_compatibility"];
  reasoning_with_tools?: Provider["reasoning_with_tools"];
  direct_available?: boolean;
  provider_id?: string;
  provider_name?: string;
  provider_base_url?: string;
  provider_model?: string;
};

export function applicationProtocol(applicationID: string) {
  if (applicationID === "codex") {
    return "openai-responses";
  }
  if (applicationID === "mimo-code") return "openai-chat";
  if (applicationID === "claude-code" || applicationID === "claude-app") {
    return "anthropic-messages";
  }
  return "";
}

export function applicationRouteOptions(
  routes: RouteConfig[],
  applicationID: string,
  providers: Provider[] = [],
) {
  const expectedProtocol = applicationProtocol(applicationID);
  const providerByID = new Map(providers.map((provider) => [provider.id, provider]));
  const options = new Map<string, ApplicationRouteOption>();
  for (const route of routes) {
    const alias = route.match.model?.trim() || "";
    if (!alias || /[?*\[]/.test(alias)) continue;
    const protocol = route.match.protocol;
    if (protocol && expectedProtocol && protocol !== expectedProtocol) continue;
    const target = route.targets.length === 1 ? route.targets[0] : undefined;
    const provider = target ? providerByID.get(target.provider) : undefined;
    const requiresCompatibility =
      applicationID === "codex" &&
      route.targets.some((item) => {
        const current = providerByID.get(item.provider);
        return current?.codex_integration === "compatibility" ||
          current?.compatibility_mode === "codex-chat" ||
          current?.compatibility_mode === "codex-responses" ||
          current?.reasoning_with_tools === "disabled";
      });
    const directAvailable =
      applicationID === "codex" &&
      provider?.protocol === "openai-responses" &&
      provider?.codex_integration === "direct";
    const integrationMode = directAvailable
      ? "direct"
        : requiresCompatibility
        ? "compatibility"
        : applicationID === "codex" && provider
          ? "passthrough"
          : undefined;
    const existing = options.get(alias);
    if (!existing || (!existing.protocol && protocol)) {
      options.set(alias, {
        alias,
        ...(protocol ? { protocol } : {}),
        ...(requiresCompatibility ? { compatibility_mode: provider?.compatibility_mode || "codex-responses" } : {}),
        ...(integrationMode ? { integration_mode: integrationMode } : {}),
        ...(applicationID === "codex" && provider?.codex_compatibility
          ? { codex_compatibility: provider.codex_compatibility }
          : {}),
        ...(applicationID === "codex" && provider?.reasoning_with_tools
          ? { reasoning_with_tools: provider.reasoning_with_tools }
          : {}),
        ...(directAvailable ? { direct_available: true } : {}),
        ...(provider && target ? {
          provider_id: provider.id,
          provider_name: provider.name,
          provider_base_url: provider.base_url,
          provider_model: target.model,
        } : {}),
      });
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

export const automaticRouteProtocols = [
  "anthropic-messages",
  "openai-chat",
  "openai-responses",
  "gemini-generate-content",
] as const;

export function generatedProviderRoutes(
  existingRoutes: RouteConfig[],
  providerID: string,
  models: string[],
  date = new Date(),
): RouteConfig[] {
  const timestamp = routeIDTimestamp(date);
  const occupiedMatches = new Set(
    existingRoutes.map(
      (route) => `${route.match.model || ""}\u0000${route.match.protocol || ""}`,
    ),
  );
  const occupiedIDs = new Set(existingRoutes.map((route) => route.id));
  const usedAliases = new Set<string>();
  const generated: RouteConfig[] = [];
  const uniqueModels = [...new Set(models.map((model) => model.trim()).filter(Boolean))];

  for (const model of uniqueModels) {
    const baseAlias = routeIdentifier(model);
    let alias = baseAlias;
    let aliasIndex = 2;
    while (usedAliases.has(alias)) {
      alias = `${baseAlias}-${aliasIndex++}`;
    }
    usedAliases.add(alias);
    for (const protocol of automaticRouteProtocols) {
      const matchKey = `${alias}\u0000${protocol}`;
      if (occupiedMatches.has(matchKey)) continue;
      let id = generatedRouteID(alias, protocol, timestamp);
      let idIndex = 2;
      while (occupiedIDs.has(id)) {
        id = `${generatedRouteID(alias, protocol, timestamp)}-${idIndex++}`;
      }
      occupiedMatches.add(matchKey);
      occupiedIDs.add(id);
      generated.push({
        id,
        priority: 100,
        match: { model: alias, protocol },
        targets: [{ provider: providerID, model }],
      });
    }
  }
  return generated;
}

export function providerModelLabel(
  providerName: string | undefined,
  providerID: string,
  model: string,
) {
  const service = (providerName || providerID).trim();
  const modelName = model.trim();
  const label = !service
    ? modelName
    : !modelName
      ? service
      : service.toLocaleLowerCase() === modelName.toLocaleLowerCase()
    ? modelName
    : `${service} / ${modelName}`;
  return label;
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
