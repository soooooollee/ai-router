export type Page =
  | "apps"
  | "overview"
  | "providers"
  | "routes"
  | "playground"
  | "logs"
  | "settings";
export type Status = {
  status: string;
  version: string;
  uptime_seconds: number;
  config_version: string;
  config_error?: string;
  runtime_state_persistent?: boolean;
  gateway_url: string;
  providers: number;
  routes: number;
  provider_health?: Record<
    string,
    { ok: boolean; latency_ms?: number; status?: number; checked_at?: string }
  >;
  metrics: Metrics;
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
  p50_latency_ms: number;
  p95_latency_ms: number;
};
export type Provider = {
  id: string;
  name: string;
  profile?: string;
  protocol: string;
  base_url: string;
  models: string[];
  api_key_set: boolean;
  api_key?: string;
  health?: { ok: boolean; latency_ms?: number; checked_at?: string };
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
  "detect" | "configure" | "preview" | "verify" | "rollback";
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
  content: Record<string, unknown>;
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
