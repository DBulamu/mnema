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

// SSE event coming off a Server-Sent Events stream. Type is the event:
// header (or "message" if absent); data is the parsed JSON payload.
export type SSEEvent<T = unknown> = { type: string; data: T };

// streamSSE opens a POST SSE stream and invokes onEvent for each
// "event: name\ndata: json" frame. EventSource cannot do POST or
// custom headers, so we use fetch + ReadableStream and parse the
// SSE wire format ourselves. The format is small enough that pulling
// in a library is overkill.
//
// On 401 we run the same silent-refresh dance as api() — if the
// refresh succeeds we re-open the stream once with the new token.
// We do not stream errors as events: a thrown ApiError is the same
// shape callers already handle for non-streaming paths.
export async function streamSSE<T = unknown>(
  path: string,
  opts: { method?: string; body?: unknown },
  onEvent: (ev: SSEEvent<T>) => void,
  signal?: AbortSignal,
): Promise<void> {
  const start = async (token: string | null): Promise<Response> => {
    const headers: Record<string, string> = { Accept: 'text/event-stream' };
    if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
    if (token) headers['Authorization'] = `Bearer ${token}`;
    return fetch(path, {
      method: opts.method ?? 'POST',
      headers,
      body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
      signal,
    });
  };

  let token = getAccessToken();
  let res = await start(token);
  if (res.status === 401) {
    const fresh = await refreshAccessToken();
    if (fresh) {
      token = fresh;
      res = await start(fresh);
    } else {
      clearTokens();
    }
  }
  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => '');
    const body = text ? safeJSON(text) : undefined;
    const message = extractMessage(body) ?? `${res.status} ${res.statusText}`;
    throw new ApiError(res.status, message, body);
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  // SSE frames are separated by a blank line. We accumulate bytes,
  // split on \n\n, and parse each frame's `event:` and `data:` lines.
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let sep: number;
    while ((sep = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      const ev = parseSSEFrame<T>(frame);
      if (ev) onEvent(ev);
    }
  }
  // Flush the tail in case the server closed without a final blank line.
  if (buf.trim()) {
    const ev = parseSSEFrame<T>(buf);
    if (ev) onEvent(ev);
  }
}

function parseSSEFrame<T>(frame: string): SSEEvent<T> | null {
  let type = 'message';
  const dataLines: string[] = [];
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) {
      type = line.slice(6).trim();
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trim());
    }
  }
  if (dataLines.length === 0) return null;
  const raw = dataLines.join('\n');
  let data: unknown = raw;
  try {
    data = JSON.parse(raw);
  } catch {
    // Non-JSON payloads stay as a string. Useful for error frames
    // that some upstreams emit before the contract has stabilised.
  }
  return { type, data: data as T };
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
