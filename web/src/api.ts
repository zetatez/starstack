/// <reference types="vite/client" />

// Lightweight API client for the StarStack REST endpoints.
export interface Entry {
  name: string;
  is_dir: boolean;
  size: number;
  mtime: string;
}

export interface UserInfo {
  id: number;
  username: string;
  is_admin: boolean;
}

export interface Session {
  access_token: string;
  refresh_token: string;
  user: UserInfo;
}

function baseURL(): string {
  return import.meta.env.VITE_API_BASE ?? '';
}

let accessToken = '';
let refreshToken = '';

export function setTokens(a: string, r: string) { accessToken = a; refreshToken = r; }
export function getAccess(): string { return accessToken; }
export function getRefresh(): string { return refreshToken; }
export function clearTokens() { accessToken = ''; refreshToken = ''; }

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined && !(body instanceof FormData)) headers['Content-Type'] = 'application/json';
  if (accessToken) headers['Authorization'] = `Bearer ${accessToken}`;
  const res = await fetch(baseURL() + url, {
    method,
    headers,
    body: body === undefined ? undefined : body instanceof FormData ? body : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json()).error ?? msg; } catch { /* ignore */ }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

/** Attempt to refresh the access token using the stored refresh token. */
export async function tryRefresh(): Promise<boolean> {
  if (!refreshToken) return false;
  try {
    const s = await request<Session>('POST', '/api/auth/refresh', { refresh_token: refreshToken });
    setTokens(s.access_token, s.refresh_token);
    return true;
  } catch { return false; }
}

export const api = {
  tryRefresh,
  login: (u: string, p: string) =>
    request<Session>('POST', '/api/auth/login', { username: u, password: p }),
  logout: () => request('POST', '/api/auth/logout', { refresh_token: refreshToken }),
  users: () => request<{ has_users: boolean }>('GET', '/api/users/first'),

  list: (scope: string, path: string) =>
    request<{ entries: Entry[] }>(`GET`, `/api/fs/list?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`),
  mkdir: (scope: string, path: string) =>
    request('POST', `/api/fs/mkdir?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`),
  rename: (scope: string, path: string, newName: string) =>
    request('POST', '/api/fs/rename', { scope, path, new_name: newName }),
  move: (scope: string, path: string, newParent: string) =>
    request('POST', '/api/fs/move', { scope, path, new_parent: newParent }),
  remove: (scope: string, path: string) =>
    request('POST', '/api/fs/delete', { scope, path }),
  upload: async (scope: string, dir: string, files: File[]) => {
    const fd = new FormData();
    for (const f of files) fd.append('files', f);
    fd.append('dir', dir);
    return request<{ uploaded: { name: string; ok?: boolean; size?: number; error?: string }[] }>(
      'POST', `/api/fs/upload?scope=${encodeURIComponent(scope)}`, fd);
  },
  downloadURL: (scope: string, path: string) =>
    `${baseURL()}/api/fs/download?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}&t=${accessToken}`,
};