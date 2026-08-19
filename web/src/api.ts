// Thin fetch wrapper. Every backend error arrives as {"error": "..."} with a
// message written for a person, so surface it verbatim rather than inventing one.
export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

export async function api<T>(path: string, body?: unknown, method?: string): Promise<T> {
  const res = await fetch(path, {
    method: method ?? (body === undefined ? 'GET' : 'POST'),
    headers: body === undefined ? undefined : { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });

  const text = await res.text();
  let parsed: unknown = null;
  try {
    parsed = text ? JSON.parse(text) : null;
  } catch {
    // fall through to the status-based message below
  }

  if (!res.ok) {
    const message =
      (parsed as { error?: string } | null)?.error ??
      `The server returned ${res.status}.`;
    throw new ApiError(message, res.status);
  }
  return parsed as T;
}

export type Session =
  | { authenticated: true; username: string }
  | { authenticated: false; has_passkeys: boolean };

export type Passkey = {
  id: string;
  name: string;
  created_at: string;
  last_used_at: string | null;
  revoked: boolean;
  sign_count: number;
  backed_up: boolean;
};
