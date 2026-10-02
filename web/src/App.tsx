import { useCallback, useEffect, useRef, useState } from 'react';
import './app.css';
import { api, setTokens, clearTokens, getAccess, getRefresh,
  type Entry, type UserInfo, type TrashItem, type Share, type UserRow } from './api';

type Scope = 'me' | 'share';
type Tab = 'files' | 'trash' | 'share' | 'admin';

interface Session { access_token: string; refresh_token: string; user: UserInfo; }
interface Item extends Entry { full: string; }

const PREVIEW_IMAGE = /\.(jpe?g|png|gif|webp|bmp)$/i;
const PREVIEW_VIDEO = /\.(mp4|webm|mov|mkv|ogv)$/i;
const PREVIEW_AUDIO = /\.(mp3|wav|ogg|m4a|flac)$/i;
const PREVIEW_TEXT = /\.(txt|md|log|json|ya?ml|toml|ini|cfg|csv|xml|html?|js|ts|py|go|rs|jsx|tsx|c|cpp|h|sh|java|rb|php|sql)$/i;
const PREVIEW_PDF = /\.pdf$/i;

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
  return (
    <Sheet
      key={session.user.id}
      user={session.user}
      onLogout={async () => {
        await api.logout().catch(() => {});
        clearTokens(); sessionStorage.removeItem('ss_session'); setSession(null);
      }}
    />
  );
}

function Login({ onLogin }: { onLogin: (s: Session) => void }) {
  const [u, setU] = useState('');
  const [p, setP] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  const submit = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr('');
    try { const s = await api.login(u, p); setTokens(s.access_token, s.refresh_token); onLogin(s); }
    catch (ex) { setErr((ex as Error).message); } finally { setBusy(false); }
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

function Sheet({ user, onLogout }: { user: UserInfo; onLogout: () => void }) {
  const [tab, setTab] = useState<Tab>('files');
  const [scope, setScope] = useState<Scope>('me');
  const [cwd, setCwd] = useState('/');
  const [items, setItems] = useState<Item[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [drag, setDrag] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);
  const [preview, setPreview] = useState<Item | null>(null);
  const [view, setView] = useState<'list' | 'grid'>('list');
  const [showHidden, setShowHidden] = useState(false);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [menu, setMenu] = useState<{ x: number; y: number; target: Item } | null>(null);

  const load = useCallback(async (sc: Scope, dir: string) => {
    setLoading(true); setError('');
    try { const res = await api.list(sc, dir); setItems(res.entries.map((e) => ({ ...e, full: joinDir(dir, e.name) }))); }
    catch (ex) { setError((ex as Error).message); } finally { setLoading(false); }
  }, []);

  useEffect(() => { if (tab === 'files') void load(scope, cwd); }, [scope, cwd, tab, load]);

  const go = (dir: string) => { setCwd(dir); setSel(new Set()); setMenu(null); };
  const refresh = () => void load(scope, cwd);

  const doUpload = async (files: File[]) => {
    if (!files.length) return; setError('');
    try {
      const res = await api.upload(scope, cwd, Array.from(files));
      res.uploaded.forEach((r) => { if (!r.ok) setError(`上传失败: ${r.name} ${r.error ?? ''}`); });
      refresh();
    } catch (ex) { setError((ex as Error).message); }
  };

  const mkdir = async () => {
    const name = prompt('文件夹名称'); if (!name) return;
    try { await api.mkdir(scope, joinDir(cwd, name)); refresh(); } catch (ex) { setError((ex as Error).message); }
  };
  const rename = async (it: Item) => {
    const name = prompt('新的名称', it.name);
    if (name && name !== it.name) { try { await api.rename(scope, it.full, name); refresh(); } catch (ex) { setError((ex as Error).message); } }
  };
  const canPreview = (n: string) => PREVIEW_IMAGE.test(n) || PREVIEW_VIDEO.test(n) || PREVIEW_AUDIO.test(n) || PREVIEW_PDF.test(n) || PREVIEW_TEXT.test(n);
  const openItem = (it: Item) => { if (it.is_dir) go(it.full); else setPreview(it); };

  const isImage = (n: string) => PREVIEW_IMAGE.test(n);
  const trail = cwd.split('/').filter(Boolean);
  const visible = showHidden ? items : items.filter((it) => !it.name.startsWith('.'));
  const parentPath = upPath(cwd, 1);
  const grandPath = upPath(cwd, 2);
  const showUp = parentPath !== cwd;
  const showUp2 = parentPath !== cwd && grandPath !== parentPath;

  // ---- multi-select ----
  const toggleSel = (full: string) => setSel((prev) => {
    const n = new Set(prev); n.has(full) ? n.delete(full) : n.add(full); return n;
  });
  const allSel = (parentPath !== cwd || visible.length > 0) && visible.length > 0 && visible.every((it) => sel.has(it.full));
  const toggleAll = () => setSel(allSel ? new Set() : new Set(visible.map((it) => it.full)));
  const clearSel = () => setSel(new Set());

  const batch = {
    download: async () => {
      if (!sel.size) return;
      try { await api.zipDownload(scope, [...sel]); clearSel(); }
      catch (e) { setError((e as Error).message); }
    },
    copy: async () => {
      if (!sel.size) return;
      const dest = prompt(`复制选中的 ${sel.size} 项到目录（绝对路径）`, '/');
      if (!dest) return;
      setError('');
      for (const p of sel) { try { await api.copy(scope, p, dest); } catch (e) { setError((e as Error).message); } }
      clearSel(); refresh();
    },
    move: async () => {
      if (!sel.size) return;
      const dest = prompt(`移动选中的 ${sel.size} 项到目录（绝对路径）`, '/');
      if (!dest) return;
      setError('');
      for (const p of sel) { try { await api.move(scope, p, dest); } catch (e) { setError((e as Error).message); } }
      clearSel(); refresh();
    },
    del: async () => {
      if (!sel.size) return;
      if (!confirm(`删除选中的 ${sel.size} 项？（进回收站）`)) return;
      setError('');
      for (const p of sel) { try { await api.remove(scope, p); } catch (e) { setError((e as Error).message); } }
      clearSel(); refresh();
    },
  };

  // ---- right-click context menu ----
  const openMenu = (e: React.MouseEvent, it: Item) => {
    e.preventDefault();
    e.stopPropagation();
    if (!sel.has(it.full)) setSel(new Set([it.full])); // right-click selects the item
    setMenu({ x: e.clientX, y: e.clientY, target: it });
  };
  const menuAct = (action: 'preview' | 'download' | 'copy' | 'move' | 'rename' | 'delete') => {
    if (!menu) return;
    const target = menu.target;
    setMenu(null);
    const multi = sel.size > 1;
    if (action === 'preview') { setPreview(target); return; }
    if (action === 'rename') { void rename(target); return; }
    if (action === 'download') {
      if (multi || target.is_dir) { void api.zipDownload(scope, [...sel]).catch((e) => setError((e as Error).message)); }
      else void api.downloadBlob(scope, target.full, target.name).catch(() => setError('下载失败'));
      return;
    }
    // bulk ops over the current selection
    if (action === 'copy') return void batch.copy();
    if (action === 'move') return void batch.move();
    if (action === 'delete') return void batch.del();
  };

  return (
    <div className="sheet">
      <header className="topbar">
        <div className="brand">StarStack</div>
        <div className="scopes">
          <button className={tab === 'files' && scope === 'me' ? 'on' : ''} onClick={() => { setTab('files'); setScope('me'); setCwd('/'); setSel(new Set()); }}>我的空间</button>
          <button className={tab === 'files' && scope === 'share' ? 'on' : ''} onClick={() => { setTab('files'); setScope('share'); setCwd('/'); setSel(new Set()); }}>共享盘</button>
        </div>
        <nav className="tabs">
          <button className={tab === 'files' ? 'on' : ''} onClick={() => setTab('files')}>文件</button>
          <button className={tab === 'trash' ? 'on' : ''} onClick={() => setTab('trash')}>回收站</button>
          <button className={tab === 'share' ? 'on' : ''} onClick={() => setTab('share')}>分享</button>
          {user.is_admin && <button className={tab === 'admin' ? 'on' : ''} onClick={() => setTab('admin')}>管理</button>}
        </nav>
        <div className="spacer" />
        <span className="muted">👤 {user.username}{user.is_admin ? ' (管理员)' : ''}</span>
        <button className="ghost" onClick={onLogout}>退出</button>
      </header>

      {tab === 'files' && (
        <>
          <div className="toolbar">
            <button className="ghost ico" title="上传" onClick={() => fileRef.current?.click()}>⬆</button>
            <button className="ghost ico" title="新建文件夹" onClick={mkdir}>＋</button>
            <button className="ghost ico" title="刷新" onClick={refresh}>↻</button>
            <input ref={fileRef} type="file" multiple hidden
              onChange={(e) => { if (e.target.files) void doUpload(Array.from(e.target.files)); e.target.value = ''; }} />
            <div className="spacer" />
            <button className={`ghost toggle${showHidden ? ' on' : ''}`} title="显示/隐藏以 . 开头的文件"
              onClick={() => setShowHidden((v) => !v)}>
              <span className="hidden-ico">{showHidden ? '◉' : '◎'}</span> 隐藏文件
            </button>
            <div className="view-toggle">
              {view === 'list'
                ? <button title="切换到网格视图" onClick={() => setView('grid')}>▦</button>
                : <button title="切换到列表视图" onClick={() => setView('list')}>☰</button>}
            </div>
          </div>
          {sel.size > 0 && (
            <div className="batchbar">
              <span className="batch-count">已选 {sel.size} 项</span>
              <button className="ghost ico" title="打包下载(选中)" onClick={() => void batch.download()}>⬇</button>
              <button className="ghost ico" title="复制到…" onClick={() => void batch.copy()}>📋</button>
              <button className="ghost ico" title="移动到…" onClick={() => void batch.move()}>➜</button>
              <button className="ghost ico" title="删除选中" onClick={() => void batch.del()}>🗑</button>
              <button className="ghost" onClick={clearSel}>取消选择 ✕</button>
            </div>
          )}
          <div className="pathbar">
            <span className="path-scope">{scope === 'me' ? '我的空间' : '共享盘'}</span>
            <span className="sep">/</span>
            <button className="link" onClick={() => go('/')} title="根目录">根</button>
            {trail.map((seg, i) => (
              <span key={i}>
                <span className="sep">/</span>
                <button className="link" onClick={() => go('/' + trail.slice(0, i + 1).join('/'))}>{seg}</button>
              </span>
            ))}
          </div>
          {error && <div className="banner err">{error}</div>}
          <div className={`body${drag ? ' drag' : ''}`}
            onDragOver={(e) => { e.preventDefault(); setDrag(true); }}
            onDragLeave={() => setDrag(false)}
            onDrop={(e) => { e.preventDefault(); setDrag(false); if (e.dataTransfer.files) void doUpload(Array.from(e.dataTransfer.files)); }}>
            {loading ? <p className="muted pad">加载中…</p> : visible.length === 0 ? <p className="muted pad">空目录 — 拖拽文件到此处上传{items.length ? '（隐藏文件已过滤，点“隐藏文件”可显示）' : ''}</p> : view === 'grid' ? (
              <div className="grid">
                {showUp && (
                  <div className="gcard gnav" title="上一级" onClick={() => go(parentPath)}>
                    <div className="gthumb"><span className="gicon-up">↩</span></div>
                    <div className="gname">..</div><div className="gmeta">上一级</div>
                  </div>
                )}
                {showUp2 && (
                  <div className="gcard gnav" title="上两级" onClick={() => go(grandPath)}>
                    <div className="gthumb"><span className="gicon-up">↪</span></div>
                    <div className="gname">...</div><div className="gmeta">上两级</div>
                  </div>
                )}
                {visible.map((it) => (
                  <div key={it.full} className={`gcard${it.name.startsWith('.') ? ' ghidden' : ''}${sel.has(it.full) ? ' gcsel' : ''}`}
                    onClick={() => toggleSel(it.full)} onDoubleClick={() => openItem(it)} onContextMenu={(e) => openMenu(e, it)}>
                    <span className="gcheck" onClick={(e) => { e.stopPropagation(); toggleSel(it.full); }}>
                      <input type="checkbox" checked={sel.has(it.full)} onChange={() => toggleSel(it.full)} />
                    </span>
                    <div className="gthumb">
                      {it.is_dir ? <span className="gico folder">📁</span>
                        : isImage(it.name) ? <img className="gimg" src={api.preview.thumbURL(scope, it.full, 256)} loading="lazy" alt={it.name} />
                        : <span className="gico file">📄</span>}
                    </div>
                    <div className="gname" title={it.full}>{it.name}</div>
                    <div className="gmeta">{it.is_dir ? '文件夹' : fmtSize(it.size)}</div>
                  </div>
                ))}
              </div>
            ) : (
              <table className="list">
                <thead><tr>
                  <th className="chk"><input type="checkbox" title="全选/取消" checked={allSel} onChange={toggleAll} /></th>
                  <th>名称</th><th>大小</th><th>修改时间</th>
                </tr></thead>
                <tbody>
                  {showUp && (
                    <tr className="nav-row" onClick={() => go(parentPath)}>
                      <td className="chk"></td>
                      <td>📁 <button className="link" onClick={(e) => { e.stopPropagation(); go(parentPath); }}>..</button></td>
                      <td>—</td><td>上一级</td>
                    </tr>
                  )}
                  {showUp2 && (
                    <tr className="nav-row" onClick={() => go(grandPath)}>
                      <td className="chk"></td>
                      <td>📁 <button className="link" onClick={(e) => { e.stopPropagation(); go(grandPath); }}>...</button></td>
                      <td>—</td><td>上两级</td>
                    </tr>
                  )}
                  {visible.map((it) => (
                    <tr key={it.full}
                        className={`${it.name.startsWith('.') ? 'ghidden ' : ''}${sel.has(it.full) ? 'sel' : ''}`}
                        onClick={() => toggleSel(it.full)} onDoubleClick={() => openItem(it)} onContextMenu={(e) => openMenu(e, it)}>
                      <td className="chk">
                        <input className="row-chk" type="checkbox" checked={sel.has(it.full)} onChange={() => toggleSel(it.full)} onClick={(e) => e.stopPropagation()} />
                      </td>
                      <td className="namecell">
                        <span className="type">{it.is_dir ? '📁' : '📄'}</span>
                        <button className="link" onClick={(e) => { e.stopPropagation(); openItem(it); }}>{it.name}</button>
                      </td>
                      <td>{it.is_dir ? '—' : fmtSize(it.size)}</td>
                      <td>{new Date(it.mtime).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      {tab === 'trash' && <TrashPage />}
      {tab === 'share' && <SharesPage />}
      {tab === 'admin' && user.is_admin && <AdminPage />}

      {menu && (
        <>
          <div className="menu-bg" onMouseDown={() => setMenu(null)} onContextMenu={(e) => { e.preventDefault(); setMenu(null); }} />
          <div className="ctxmenu" style={{ left: menu.x, top: menu.y }}>
            <div className="ctx-title">{menu.target.is_dir ? '📁' : '📄'} {menu.target.name}{sel.size > 1 ? `  (+${sel.size - 1})` : ''}</div>
            <button disabled={menu.target.is_dir || sel.size > 1 || !canPreview(menu.target.name)} onClick={() => menuAct('preview')}>👁 预览</button>
            <button onClick={() => menuAct('download')}>⬇ {sel.size > 1 ? `打包下载(${sel.size})` : '下载'}</button>
            <div className="ctxsep" />
            <button onClick={() => menuAct('copy')}>📋 {sel.size > 1 ? `复制(${sel.size})` : '复制到…'}</button>
            <button onClick={() => menuAct('move')}>➜ {sel.size > 1 ? `移动(${sel.size})` : '移动到…'}</button>
            <button disabled={sel.size > 1} onClick={() => menuAct('rename')}>✏️ 重命名</button>
            <div className="ctxsep" />
            <button className="danger" onClick={() => menuAct('delete')}>🗑 {sel.size > 1 ? `删除(${sel.size})` : '删除'}</button>
          </div>
        </>
      )}

      {preview && <PreviewModal item={preview} scope={scope} onClose={() => setPreview(null)} />}
    </div>
  );
}

function TrashPage() {
  const [list, setList] = useState<TrashItem[]>([]);
  const [err, setErr] = useState('');
  const reload = useCallback(async () => {
    try { setList((await api.trash.list()).trash); }
    catch (e) { setErr((e as Error).message); }
  }, []);
  useEffect(() => { void reload(); }, [reload]);
  const act = async (fn: () => Promise<unknown>) => { try { await fn(); void reload(); } catch (e) { setErr((e as Error).message); } };

  return (
    <div className="tab-page">
      <div className="toolbar">
        <button className="ghost" onClick={() => act(() => api.trash.empty())}>清空回收站</button>
        <button className="ghost" onClick={() => void reload()}>↻ 刷新</button>
        {err && <span className="err">{err}</span>}
      </div>
      <div className="body">
        {list.length === 0 ? <p className="muted pad">回收站为空</p> : (
          <table className="list">
            <thead><tr><th>原位置</th><th>大小</th><th>删除时间</th><th>操作</th></tr></thead>
            <tbody>
              {list.map((t) => (
                <tr key={t.id}>
                  <td>{t.is_dir ? '📁' : '📄'} {t.orig_path}</td>
                  <td>{fmtSize(t.size)}</td>
                  <td>{new Date(t.deleted_at).toLocaleString()}</td>
                  <td className="actions">
                    <button className="ghost sm" onClick={() => act(() => api.trash.restore(t.id))}>恢复</button>
                    <button className="ghost sm danger" onClick={() => confirm('彻底删除？') && act(() => api.trash.purge(t.id))}>彻底删除</button>
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

function SharesPage() {
  const [shares, setShares] = useState<Share[]>([]);
  const [err, setErr] = useState('');
  const [path, setPath] = useState('/');
  const [scope, setScope] = useState<Scope>('me');
  const [allowDown, setAllowDown] = useState(true);
  const [pub, setPub] = useState<{ shares: Share[]; list: Entry[]; token: string; pw: string } | null>(null);

  const reload = useCallback(async () => {
    try { setShares((await api.share.list()).shares); } catch (e) { setErr((e as Error).message); }
  }, []);
  useEffect(() => { void reload(); }, [reload]);

  const create = async () => {
    if (!path) return; setErr('');
    try {
      await api.share.create({ scope, path, allow_down: allowDown });
      setPath('/'); void reload();
    } catch (e) { setErr((e as Error).message); }
  };

  const viewPublic = async (token: string, pw: string) => {
    try { setPub({ shares: [], list: (await api.share.publicList(token, pw)).entries, token, pw }); }
    catch (e) { setErr((e as Error).message); }
  };

  return (
    <div className="tab-page">
      <div className="toolbar">
        <select value={scope} onChange={(e) => setScope(e.target.value as Scope)} title="空间">
          <option value="me">我的空间</option><option value="share">共享盘</option>
        </select>
        <input className="path-input" value={path} onChange={(e) => setPath(e.target.value)} placeholder="要分享的路径，如 /photos" />
        <label className="chk"><input type="checkbox" checked={allowDown} onChange={(e) => setAllowDown(e.target.checked)} /> 允许下载</label>
        <button className="ghost" onClick={create}>＋ 生成分享链接</button>
        {err && <span className="err">{err}</span>}
      </div>
      <div className="body">
        {pub && pub.list.length > 0 && (
          <div className="pub banner">
            <span>公开分享视图 <code>/api/share/{pub.token}/list</code></span>
            <button className="ghost sm" onClick={() => setPub(null)}>关闭</button>
            <ul>
              {pub.list.map((e) => <li key={e.name}>{e.is_dir ? '📁' : '📄'} {e.name} {e.is_dir ? '' : fmtSize(e.size)}</li>)}
            </ul>
          </div>
        )}
        {shares.length === 0 && !pub ? <p className="muted pad">还没有分享链接</p> : (
          <table className="list">
            <thead><tr><th>路径</th><th>Token</th><th>可下载</th><th>次数</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              {shares.map((s) => {
                const expired = s.expires_at && new Date(s.expires_at).getTime() < Date.now();
                return (
                  <tr key={s.token}>
                    <td>{s.path}</td>
                    <td><code>{s.token.slice(0, 8)}…</code>
                      <button className="ghost sm" onClick={() => { void (async () => {
                        try { await api.share.publicList(s.token); }
                        catch (e) { setErr((e as Error).message); }
                      })(); viewPublic(s.token, ''); }}>试览</button>
                    </td>
                    <td>{s.allow_down ? '✓' : '—'}</td>
                    <td>{s.used}{s.max_uses ? `/${s.max_uses}` : ''}</td>
                    <td>{expired ? '已过期' : '有效'}</td>
                    <td className="actions"><button className="ghost sm danger" onClick={() => confirm('删除分享？') && void api.share.remove(s.token).then(reload)}>删除</button></td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function AdminPage() {
  const [users, setUsers] = useState<UserRow[]>([]);
  const [err, setErr] = useState('');
  const [nu, setNu] = useState(''); const [np, setNp] = useState(''); const [admin, setAdmin] = useState(false);
  const reload = useCallback(async () => { try { setUsers((await api.users.list()).users); } catch (e) { setErr((e as Error).message); } }, []);
  useEffect(() => { void reload(); }, [reload]);
  const create = async () => { if (!nu || !np) return; try { await api.users.create(nu, np, admin); setNu(''); setNp(''); void reload(); } catch (e) { setErr((e as Error).message); } };
  const resetPw = async (id: number, name: string) => { const pw = prompt(`为用户 ${name} 设置新密码`); if (pw) { try { await api.users.resetPassword(id, pw); } catch (e) { setErr((e as Error).message); } } };

  return (
    <div className="tab-page">
      <div className="toolbar">
        <input className="path-input" placeholder="新用户名" value={nu} onChange={(e) => setNu(e.target.value)} />
        <input className="path-input" placeholder="初始密码" type="password" value={np} onChange={(e) => setNp(e.target.value)} />
        <label className="chk"><input type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} /> 管理员</label>
        <button className="ghost" onClick={create}>＋ 创建用户</button>
        {err && <span className="err">{err}</span>}
      </div>
      <div className="body">
        <table className="list">
          <thead><tr><th>ID</th><th>用户名</th><th>角色</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>{u.id}</td><td>{u.username}</td><td>{u.is_admin ? '管理员' : '用户'}</td><td>{u.disabled ? '已停用' : '正常'}</td>
                <td className="actions">
                  <button className="ghost sm" onClick={() => resetPw(u.id, u.username ?? String(u.id))}>重置密码</button>
                  <button className="ghost sm" onClick={() => void api.users.setState(u.id, !u.disabled).then(reload)}>{u.disabled ? '启用' : '停用'}</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function PreviewModal({ item, scope, onClose }: { item: Item; scope: Scope; onClose: () => void }) {
  const [text, setText] = useState<string | null>(null);
  useEffect(() => {
    if (PREVIEW_TEXT.test(item.name)) { setText(null); api.preview.text(scope, item.full).then(setText).catch(() => setText('（无法读取）')); }
  }, [item, scope]);
  const raw = api.preview.rawURL(scope, item.full);

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <span className="brand">{item.name}</span>
          <button className="ghost" onClick={() => void api.downloadBlob(scope, item.full, item.name)}>下载</button>
          <button className="ghost" onClick={onClose}>✕</button>
        </div>
        <div className="modal-body">
          {PREVIEW_IMAGE.test(item.name) ? <img className="preview-img" src={raw} alt={item.name} />
            : PREVIEW_VIDEO.test(item.name) ? <video className="preview-media" src={raw} controls autoPlay />
            : PREVIEW_AUDIO.test(item.name) ? <audio src={raw} controls autoPlay />
            : PREVIEW_PDF.test(item.name) ? <iframe className="preview-iframe" src={raw} title={item.name} />
            : PREVIEW_TEXT.test(item.name) ? <pre className="preview-text">{text ?? '加载中…'}</pre>
            : <p className="muted pad">该文件不支持预览，请下载查看。</p>}
        </div>
      </div>
    </div>
  );
}

function joinDir(dir: string, name: string): string {
  const d = dir === '/' ? '' : dir;
  return `${d}/${name}`;
}

/** Return the path `levels` directories above `dir` (clamped at '/'). */
function upPath(dir: string, levels: number): string {
  const parts = dir.split('/').filter(Boolean);
  const kept = parts.slice(0, Math.max(0, parts.length - levels));
  return '/' + kept.join('/');
}

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n, i = -1;
  do { v /= 1024; i++; } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(1)} ${units[i]}`;
}