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

/** fetch with Authorization header so tokens never leak into URLs/logs. */
export function authedFetch(url: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  if (accessToken && !headers.has('Authorization')) headers.set('Authorization', `Bearer ${accessToken}`);
  return fetch(url, { ...init, headers });
}

let accessToken = '';
let refreshToken = '';

export function saveBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url; a.download = name; a.click();
  URL.revokeObjectURL(url);
}

// Client-side LRU cache for thumbnail URLs with eviction. Virtualized grids
// mount/unmount cells as you scroll; keeping the URL reuses the browser's HTTP
// cache (identical URL + Cache-Control: max-age) so scrolled-back items load
// instantly instead of re-requesting.
const thumbLru = new Map<string, string>();
const THUMB_LRU_MAX = 800;
let thumbRev = 0; // bumped on rotation so browsers don't serve stale cached thumbs
export function bumpThumbs() { thumbRev++; thumbLru.clear(); }
export function thumbSrc(scope: string, path: string, size: number, q = 60): string {
  const pre = api.preview.thumbURL(scope, path, size, q);
  const key = scope + '\u0000' + path + '\u0000' + size + '\u0000' + q + '\u0000' + thumbRev;
  const hit = thumbLru.get(key);
  if (hit) {
    thumbLru.delete(key);
    thumbLru.set(key, hit);
    return hit;
  }
  thumbLru.set(key, pre);
  if (thumbLru.size > THUMB_LRU_MAX) {
    thumbLru.delete(thumbLru.keys().next().value!);
  }
  return pre;
}

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

export interface TrashItem {
  id: number;
  orig_path: string;
  is_dir: boolean;
  size: number;
  deleted_at: string;
}

export interface Share {
  id: number;
  token: string;
  scope: string;
  path: string;
  expires_at: string;
  allow_down: boolean;
  max_uses: number;
  used: number;
  created_at: string;
}

export interface UserRow {
  id: number;
  username: string | null;
  is_admin: boolean;
  disabled: boolean;
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

  list: (scope: string, path: string) =>
    request<{ entries: Entry[] }>(`GET`, `/api/fs/list?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`),
  mkdir: (scope: string, path: string) =>
    request('POST', `/api/fs/mkdir?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`),
  rename: (scope: string, path: string, newName: string) =>
    request('POST', '/api/fs/rename', { scope, path, new_name: newName }),
  move: (scope: string, path: string, newParent: string) =>
    request('POST', '/api/fs/move', { scope, path, new_parent: newParent }),
  copy: (scope: string, path: string, newParent: string) =>
    request('POST', '/api/fs/copy', { scope, path, new_parent: newParent }),
  remove: (scope: string, path: string) =>
    request('POST', '/api/fs/delete', { scope, path }),

  trash: {
    list: () => request<{ trash: TrashItem[] }>('GET', '/api/trash/list'),
    restore: (id: number) => request('POST', '/api/trash/restore', { id }),
    purge: (id: number) => request<void>('POST', '/api/trash/purge', { id }),
    empty: () => request<{ purged: number }>('POST', '/api/trash/empty', {}),
  },

  preview: {
    thumbURL: (scope: string, path: string, size = 96, q = 70) =>
      `${baseURL()}/api/preview/thumb?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}&size=${size}&q=${q}&rev=${thumbRev}&auth=${encodeURIComponent(accessToken)}`,
    rawURL: (scope: string, path: string) =>
      `${baseURL()}/api/preview/raw?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}&rev=${thumbRev}&auth=${encodeURIComponent(accessToken)}`,
    async text(scope: string, path: string): Promise<string> {
      const res = await authedFetch(`${baseURL()}/api/preview/raw?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`);
      if (!res.ok) throw new Error('Preview failed');
      return res.text();
    },
  },

  rotate: (scope: string, paths: string[], angle: number) =>
    request<{ rotated: { path: string; ok?: boolean; error?: string }[] }>('POST', '/api/preview/rotate', { scope, paths, angle }),

  share: {
    list: () => request<{ shares: Share[] }>('GET', '/api/share/list'),
    create: (p: { scope: string; path: string; password?: string; expires_at?: number; allow_down?: boolean; max_uses?: number }) =>
      request<{ token: string }>('POST', '/api/share', p),
    remove: (token: string) => request<void>('DELETE', `/api/share/${token}`),
    publicList: (token: string, pw?: string) =>
      request<{ entries: Entry[] }>(`GET`, `/api/share/${token}/list${pw ? `?pw=${encodeURIComponent(pw)}` : ''}`),
  },

  users: {
    list: () => request<{ users: UserRow[] }>('GET', '/api/users'),
    create: (u: string, p: string, admin: boolean) =>
      request('POST', '/api/users', { username: u, password: p, is_admin: admin }),
    setState: (id: number, disabled: boolean) =>
      request('PATCH', `/api/users/${id}/state`, { disabled }),
    resetPassword: (id: number, pw: string) =>
      request('POST', `/api/users/${id}/password`, { password: pw }),
    delete: (id: number) => request('DELETE', `/api/users/${id}`),
  },
  me: () => request<UserRow>('GET', '/api/me'),
  upload: async (scope: string, dir: string, files: File[]) => {
    const fd = new FormData();
    for (const f of files) fd.append('files', f);
    fd.append('dir', dir);
    return request<{ uploaded: { name: string; ok?: boolean; size?: number; error?: string }[] }>(
      'POST', `/api/fs/upload?scope=${encodeURIComponent(scope)}`, fd);
  },
  downloadURL: (scope: string, path: string) =>
    `${baseURL()}/api/fs/download?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`,

  async downloadBlob(scope: string, path: string, name: string): Promise<void> {
    const res = await authedFetch(`${baseURL()}/api/fs/download?scope=${encodeURIComponent(scope)}&path=${encodeURIComponent(path)}`);
    if (!res.ok) throw new Error('Download failed');
    return saveBlob(await res.blob(), name);
  },
  /** Download multiple paths as a single zip archive. */
  async zipDownload(scope: string, paths: string[], name = 'selected.zip'): Promise<void> {
    const q = `scope=${encodeURIComponent(scope)}` + paths.map((p) => `&path=${encodeURIComponent(p)}`).join('');
    const res = await authedFetch(`${baseURL()}/api/fs/zip?${q}`);
    if (!res.ok) { let m = res.statusText; try { m = (await res.json()).error ?? m; } catch { /* ignore */ } throw new Error(m); }
    return saveBlob(await res.blob(), name);
  },
};