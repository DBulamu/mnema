import {
  clearTokens,
  getAccessToken,
  getRefreshToken,
  setAccessToken,
} from './auth';

export class ApiError extends Error {
  status: number;
  body: unknown;

  constructor(status: number, message: string, body: unknown) {
    super(message);
    this.status = status;
    this.body = body;
  }
}

type RequestOptions = {
  method?: string;
  body?: unknown;
  // skipAuth — for /auth/* endpoints that must NOT carry a Bearer token,
  // since a stale access token would otherwise leak into the request.
  skipAuth?: boolean;
};

let refreshInFlight: Promise<string | null> | null = null;

async function refreshAccessToken(): Promise<string | null> {
  if (refreshInFlight) return refreshInFlight;
  const refresh = getRefreshToken();
  if (!refresh) return null;

  refreshInFlight = (async () => {
    try {
      const res = await fetch('/v1/auth/refresh', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refresh }),
      });
      if (!res.ok) return null;
      const data = (await res.json()) as { access_token?: string };
      if (!data.access_token) return null;
      setAccessToken(data.access_token);
      return data.access_token;
    } catch {
      return null;
    } finally {
      refreshInFlight = null;
    }
  })();

  return refreshInFlight;
}

async function doFetch(
  path: string,
  opts: RequestOptions,
  token: string | null,
): Promise<Response> {
  const headers: Record<string, string> = {};
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (token && !opts.skipAuth) headers['Authorization'] = `Bearer ${token}`;
  return fetch(path, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
}

export async function api<T = unknown>(
  path: string,
  opts: RequestOptions = {},
): Promise<T> {
  const token = opts.skipAuth ? null : getAccessToken();
  let res = await doFetch(path, opts, token);

  // 401 → try one silent refresh, then retry once. If refresh fails,
  // wipe tokens so the router can bounce the user to /login.
  if (res.status === 401 && !opts.skipAuth) {
    const fresh = await refreshAccessToken();
    if (fresh) {
      res = await doFetch(path, opts, fresh);
    } else {
      clearTokens();
    }
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  const body: unknown = text ? safeJSON(text) : undefined;

  if (!res.ok) {
    const message = extractMessage(body) ?? `${res.status} ${res.statusText}`;
    throw new ApiError(res.status, message, body);
  }

  return body as T;
}

function safeJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function extractMessage(body: unknown): string | null {
  if (!body || typeof body !== 'object') return null;
  const b = body as Record<string, unknown>;
  if (typeof b.detail === 'string') return b.detail;
  if (typeof b.title === 'string') return b.title;
  if (typeof b.message === 'string') return b.message;
  return null;
}
