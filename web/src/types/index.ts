export type Page =
  | "apps"
  | "clients"
  | "overview"
  | "providers"
  | "routes"
  | "playground"
  | "logs"
  | "settings";

export type ClientPolicy = {
  id: string;
  project_id?: string;
  allowed_models?: string[];
  allowed_protocols?: string[];
  allowed_cidrs?: string[];
  requests_per_minute?: number;
  burst?: number;
  max_concurrent?: number;
  daily_request_limit?: number;
  daily_input_tokens?: number;
  daily_output_tokens?: number;
  max_output_tokens?: number;
};
export type ClientCredential = {
  id: string;
  client_id: string;
  kind: "standard" | "managed";
  prefix: string;
  recoverable: boolean;
  status: "active" | "disabled" | "expired" | "revoked";
  created_at: string;
  expires_at?: string;
  last_used_at?: string;
  revoked_at?: string;
};
export type ClientUsageBucket = {
  requests: number;
  errors: number;
  rejected: number;
  input_tokens: number;
  output_tokens: number;
};
export type GatewayClient = {
  client: {
    id: string;
    name: string;
    description?: string;
    status: "active" | "disabled" | "deleted";
    created_at: string;
    updated_at: string;
  };
  policy: ClientPolicy;
  credentials: ClientCredential[];
  active_credentials: number;
  today: ClientUsageBucket;
};
export type ClientListResponse = {
  clients: GatewayClient[];
  authentication_enabled: boolean;
  managed_store: boolean;
  legacy_keys: { id: string }[];
  gateway_public: boolean;
};
export type Status = {
  status: string;
  version: string;
  uptime_seconds: number;
  config_version: string;
  config_error?: string;
  runtime_state_persistent?: boolean;
  gateway_url: string;
  providers: number;
  models?: number;
  routes: number;
  applications_configured?: number;
  applications_total?: number;
  logs?: number;
  logs_capacity?: number;
  logging_persist?: boolean;
  logging_capture_bodies?: boolean;
  provider_health?: Record<
    string,
    { ok: boolean; latency_ms?: number; status?: number; checked_at?: string }
  >;
  metrics: Metrics;
};
export type UpdateInfo = {
  checked: boolean;
  current_version: string;
  latest_version?: string;
  update_available: boolean;
  release_url: string;
  checked_at: string;
  check_unavailable?: boolean;
};
export type Metrics = {
  requests: number;
  errors: number;
  in_flight: number;
  retries: number;
  fallbacks: number;
  input_tokens: number;
  output_tokens: number;
  timeouts: number;
  cancellations: number;
  diagnostics: number;
  average_latency_ms?: number;
  average_first_token_ms?: number;
  p50_latency_ms: number;
  p95_latency_ms: number;
};
export type Provider = {
  id: string;
  name: string;
  profile?: string;
  protocol: string;
  codex_integration?: "direct" | "passthrough" | "compatibility";
  codex_compatibility?: "full" | "degraded" | "unverified" | "incompatible" | "unavailable";
  compatibility_mode?: "codex-chat" | "codex-responses";
  tool_choice_mode?: "standard" | "required" | "auto-only";
  reasoning_history?: "preserve" | "drop";
  reasoning_with_tools?: "supported" | "disabled";
  base_url: string;
  models: string[];
  api_key_set: boolean;
  api_key?: string;
  request_policy?: { omit_fields?: string[] };
  health?: {
    ok: boolean;
    latency_ms?: number;
    checked_at?: string;
    models_ok?: boolean;
    models_status?: number;
  };
};
export type RouteConfig = {
  id: string;
  priority: number;
  match: {
    model?: string;
    protocol?: string;
    stream?: boolean;
    tools?: boolean;
    image?: boolean;
    headers?: Record<string, string>;
  };
  targets: { provider: string; model: string }[];
};
export type RouteTableRow = RouteConfig & {
  key: string;
  order: string;
  fallback?: boolean;
};
export type AppConfig = {
  providers: Provider[];
  routes: RouteConfig[];
  auth?: { enabled?: boolean };
  logging?: { web_redaction?: boolean };
  default_route?: { targets: { provider: string; model: string }[] };
};
export type ApplicationCapability =
  | "detect"
  | "configure"
  | "preview"
  | "verify"
  | "rollback"
  | "cleanup"
  | "edit-preview";
export type ApplicationManifest = {
  id: string;
  name: string;
  description: string;
  status: string;
  capabilities: ApplicationCapability[];
  config_format: string;
};
export type ApplicationDetection = {
  installed: boolean;
  executable?: string;
  version?: string;
  message?: string;
};
export type ApplicationListItem = {
  manifest: ApplicationManifest;
  detection: ApplicationDetection;
};
export type ApplicationState = {
  manifest: ApplicationManifest;
  detection: ApplicationDetection;
  path: string;
  exists: boolean;
  managed: Record<string, string | boolean>;
  preserved_fields: number;
  synced: boolean;
};
export type ApplicationPreview = {
  path: string;
  current: Record<string, unknown> | string;
  content: Record<string, unknown> | string;
  diff: string;
  preserved_fields: number;
  will_create_backup: boolean;
};
export type ApplicationBackup = {
  name: string;
  path: string;
  size: number;
  modified_at: string;
  contains_sensitive_config?: boolean;
};
export type ApplicationVerifyResult = {
  ok: boolean;
  verified_at: string;
  stages: {
    id: string;
    label: string;
    ok: boolean;
    message: string;
    detail?: string;
    latency_ms?: number;
  }[];
};
export type Usage = {
  input_tokens?: number;
  output_tokens?: number;
  total_tokens?: number;
  cached_tokens?: number;
  reasoning_tokens?: number;
};
export type LogRecord = {
  id: string;
  started_at: string;
  client_protocol: string;
  requested_model: string;
  route_id: string;
  provider_id: string;
  upstream_protocol?: string;
  resolved_model: string;
  status: number;
  duration_ms: number;
  first_token_ms?: number;
  usage?: Usage;
  error_code?: string;
  request_body?: string;
  response_body?: string;
  attempts?: {
    number: number;
    provider_id: string;
    model: string;
    status: number;
    error?: string;
    duration_ms: number;
  }[];
  diagnostics?: {
    severity: string;
    code: string;
    path?: string;
    message: string;
    action: string;
  }[];
};
