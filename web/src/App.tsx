import { useCallback, useEffect, useRef, useState } from 'react';
import './app.css';
import { api, type Entry, type UserInfo, setTokens, clearTokens, getAccess, getRefresh } from './api';

type Scope = 'me' | 'share';

interface Session { access_token: string; refresh_token: string; user: UserInfo; }

/** Single-page file manager: login → browse → upload/download/mkdir/rename/move/delete. */
export default function App() {
  const [session, setSession] = useState<Session | null>(null);
  const [booted, setBooted] = useState(false);

  useEffect(() => {
    (async () => {
      const stored = sessionStorage.getItem('ss_session');
      if (stored) {
        const s = JSON.parse(stored) as Session;
        setTokens(s.access_token, s.refresh_token);
        if (await api.tryRefresh()) setSessionFrom(s);
        else { clearTokens(); sessionStorage.removeItem('ss_session'); }
      }
      setBooted(true);
    })();
  }, []);

  function setSessionFrom(s: Session) {
    const merged = { ...s, access_token: getAccess(), refresh_token: getRefresh() };
    sessionStorage.setItem('ss_session', JSON.stringify(merged));
    setSession(merged);
  }

  if (!booted) return null;
  if (!session) return <Login onLogin={(s) => setSessionFrom(s)} />;
  return <Sheet key={session.user.id} user={session.user} onLogout={async () => {
    await api.logout().catch(() => {});
    clearTokens(); sessionStorage.removeItem('ss_session'); setSession(null);
  }} />;
}

function Login({ onLogin }: { onLogin: (s: Session) => void }) {
  const [u, setU] = useState('');
  const [p, setP] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr('');
    try {
      const s = await api.login(u, p);
      setTokens(s.access_token, s.refresh_token);
      onLogin(s);
    } catch (ex) {
      setErr((ex as Error).message);
    } finally { setBusy(false); }
  };

  return (
    <div className="login-wrap">
      <form className="card login" onSubmit={submit}>
        <h1>StarStack</h1>
        <p className="muted">本地私有云盘</p>
        <input value={u} onChange={(e) => setU(e.target.value)} placeholder="用户名" autoFocus />
        <input value={p} onChange={(e) => setP(e.target.value)} placeholder="密码" type="password" />
        {err && <p className="err">{err}</p>}
        <button disabled={busy || !u || !p}>{busy ? '登录中…' : '登录'}</button>
        <p className="tip">首个注册的用户自动成为管理员</p>
      </form>
    </div>
  );
}

interface Item extends Entry { full: string; }

function Sheet({ user, onLogout }: { user: UserInfo; onLogout: () => void }) {
  const [scope, setScope] = useState<Scope>('me');
  const [cwd, setCwd] = useState('/');
  const [items, setItems] = useState<Item[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [drag, setDrag] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const [sel, setSel] = useState<string | null>(null);

  const load = useCallback(async (sc: Scope, dir: string) => {
    setLoading(true); setError('');
    try {
      const res = await api.list(sc, dir);
      setItems(res.entries.map((e) => ({
        ...e, full: joinDir(dir, e.name),
      })));
    } catch (ex) { setError((ex as Error).message); }
    finally { setLoading(false); }
  }, []);

  useEffect(() => { void load(scope, cwd); }, [scope, cwd, load]);

  const go = (dir: string) => { setCwd(dir); setSel(null); };
  const refresh = () => void load(scope, cwd);

  const doUpload = async (files: File[]) => {
    if (!files.length) return;
    setError('');
    try {
      const res = await api.upload(scope, cwd, Array.from(files));
      res.uploaded.forEach((r) => { if (!r.ok) setError(`上传失败: ${r.name} ${r.error ?? ''}`); });
      refresh();
    } catch (ex) { setError((ex as Error).message); }
  };

  const mkdir = async () => {
    const name = prompt('文件夹名称');
    if (!name) return;
    try { await api.mkdir(scope, joinDir(cwd, name)); refresh(); }
    catch (ex) { setError((ex as Error).message); }
  };
  const rename = async (it: Item) => {
    const name = prompt('新的名称', it.name);
    if (name && name !== it.name) {
      try { await api.rename(scope, it.full, name); refresh(); }
      catch (ex) { setError((ex as Error).message); }
    }
  };
  const move = async (it: Item) => {
    const dest = prompt('目标目录（绝对路径，如 /docs 或 /）', '/');
    if (dest) {
      try { await api.move(scope, it.full, dest); refresh(); }
      catch (ex) { setError((ex as Error).message); }
    }
  };
  const del = async (it: Item) => {
    if (!confirm(`删除 ${it.name} ？`)) return;
    try { await api.remove(scope, it.full); refresh(); }
    catch (ex) { setError((ex as Error).message); }
  };
  const download = async (it: Item) => {
    try {
      const res = await fetch(api.downloadURL(scope, it.full), {
        headers: { Authorization: `Bearer ${getAccess()}` },
      });
      if (!res.ok) { setError('下载失败'); return; }
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url; a.download = it.name; a.click();
      URL.revokeObjectURL(url);
    } catch { setError('下载失败'); }
  };

  const trail = cwd.split('/').filter(Boolean);

  return (
    <div className="sheet">
      <header className="topbar">
        <div className="brand">StarStack</div>
        <div className="scopes">
          <button className={scope === 'me' ? 'on' : ''} onClick={() => { go('/'); setScope('me'); }}>我的空间</button>
          <button className={scope === 'share' ? 'on' : ''} onClick={() => { go('/'); setScope('share'); }}>共享盘</button>
        </div>
        <div className="spacer" />
        <span className="muted">👤 {user.username}{user.is_admin ? ' (管理员)' : ''}</span>
        <button className="ghost" onClick={onLogout}>退出</button>
      </header>

      <div className="toolbar">
        <button className="ghost" onClick={() => fileRef.current?.click()}>⬆ 上传</button>
        <button className="ghost" onClick={mkdir}>＋ 新建文件夹</button>
        <button className="ghost" onClick={refresh}>↻ 刷新</button>
        <input
          ref={fileRef} type="file" multiple hidden
          onChange={(e) => { if (e.target.files) void doUpload(Array.from(e.target.files)); e.target.value = ''; }}
        />
        <div className="path">
          <button className="link" onClick={() => go('/')}>/</button>
          {trail.map((seg, i) => (
            <span key={i}>
              <button className="link" onClick={() => go('/' + trail.slice(0, i + 1).join('/'))}>{seg}</button>
            </span>
          ))}
        </div>
      </div>

      {error && <div className="banner err">{error}</div>}

      <div
        className={`body${drag ? ' drag' : ''}`}
        onDragOver={(e) => { e.preventDefault(); setDrag(true); }}
        onDragLeave={() => setDrag(false)}
        onDrop={(e) => { e.preventDefault(); setDrag(false); if (e.dataTransfer.files) void doUpload(Array.from(e.dataTransfer.files)); }}
      >
        {loading ? <p className="muted pad">加载中…</p> : items.length === 0 ? (
          <p className="muted pad">空目录 — 拖拽文件到此处上传</p>
        ) : (
          <table className="list">
            <thead><tr><th>名称</th><th>大小</th><th>修改时间</th><th>操作</th></tr></thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.full} className={sel === it.full ? 'sel' : ''}
                    onClick={() => setSel(it.full)}
                    onDoubleClick={() => { if (it.is_dir) go(it.full); }}>
                  <td>
                    {it.is_dir ? '📁' : '📄'} <button className="link" onClick={(e) => { e.stopPropagation(); if (it.is_dir) go(it.full); }}>
                      {it.name}
                    </button>
                  </td>
                  <td>{it.is_dir ? '—' : fmtSize(it.size)}</td>
                  <td>{new Date(it.mtime).toLocaleString()}</td>
                  <td className="actions">
                    {!it.is_dir && <button className="ghost sm" onClick={(e) => { e.stopPropagation(); void download(it); }}>下载</button>}
                    <button className="ghost sm" onClick={(e) => { e.stopPropagation(); void rename(it); }}>重命名</button>
                    <button className="ghost sm" onClick={(e) => { e.stopPropagation(); void move(it); }}>移动</button>
                    <button className="ghost sm danger" onClick={(e) => { e.stopPropagation(); void del(it); }}>删除</button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function joinDir(dir: string, name: string): string {
  const d = dir === '/' ? '' : dir;
  return `${d}/${name}`;
}

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n, i = -1;
  do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(1)} ${units[i]}`;
}