import { useCallback, useEffect, useRef, useState } from 'react';
import './app.css';
import { api, setTokens, clearTokens, getAccess, getRefresh, thumbSrc, bumpThumbs,
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
        <p className="muted">Your private cloud drive</p>
        <input value={u} onChange={(e) => setU(e.target.value)} placeholder="Username" autoFocus />
        <input value={p} onChange={(e) => setP(e.target.value)} placeholder="Password" type="password" />
        {err && <p className="err">{err}</p>}
        <button disabled={busy || !u || !p}>{busy ? 'Signing in…' : 'Sign in'}</button>
        <p className="tip">The first registered account becomes the admin</p>
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
  const [viewManual, setViewManual] = useState(false); // user overrode auto view
  const [showHidden, setShowHidden] = useState(false);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [menu, setMenu] = useState<{ x: number; y: number; target: Item } | null>(null);
  const [shareInfo, setShareInfo] = useState('');

  // Viewport tracking for the virtualized grid.
  const bodyRef = useRef<HTMLDivElement>(null);
  const [vw, setVw] = useState({ top: 0, height: 600, width: 0 });
  useEffect(() => {
    if (tab !== 'files') return;
    const el = bodyRef.current;
    if (!el) return;
    const upd = () => setVw((v) => {
      const width = el.clientWidth || 0;
      const height = el.clientHeight || 600;
      const top = el.scrollTop;
      return v.width === width && v.height === height && v.top === top ? v : { top, height, width };
    });
    upd();
    const ro = new ResizeObserver(upd);
    ro.observe(el);
    el.addEventListener('scroll', upd, { passive: true });
    return () => { ro.disconnect(); el.removeEventListener('scroll', upd); };
  }, [tab]);

  const load = useCallback(async (sc: Scope, dir: string) => {
    setLoading(true); setError('');
    try { const res = await api.list(sc, dir); setItems(res.entries.map((e) => ({ ...e, full: joinDir(dir, e.name) }))); }
    catch (ex) { setError((ex as Error).message); } finally { setLoading(false); }
  }, []);

  useEffect(() => { if (tab === 'files') void load(scope, cwd); }, [scope, cwd, tab, load]);

  const go = (dir: string) => { setCwd(dir); setSel(new Set()); setMenu(null); setViewManual(false); };
  const refresh = () => void load(scope, cwd);

  const doUpload = async (files: File[]) => {
    if (!files.length) return; setError('');
    try {
      const res = await api.upload(scope, cwd, Array.from(files));
      res.uploaded.forEach((r) => { if (!r.ok) setError(`Upload failed: ${r.name} ${r.error ?? ''}`); });
      refresh();
    } catch (ex) { setError((ex as Error).message); }
  };

  const mkdir = async () => {
    const name = prompt('Folder name'); if (!name) return;
    try { await api.mkdir(scope, joinDir(cwd, name)); refresh(); } catch (ex) { setError((ex as Error).message); }
  };
  const rename = async (it: Item) => {
    const name = prompt('New name', it.name);
    if (name && name !== it.name) { try { await api.rename(scope, it.full, name); refresh(); } catch (ex) { setError((ex as Error).message); } }
  };
  const canPreview = (n: string) => PREVIEW_IMAGE.test(n) || PREVIEW_VIDEO.test(n) || PREVIEW_AUDIO.test(n) || PREVIEW_PDF.test(n) || PREVIEW_TEXT.test(n);
  const openItem = (it: Item) => { if (it.is_dir) go(it.full); else setPreview(it); };

  const isImage = (n: string) => PREVIEW_IMAGE.test(n);
  const trail = cwd.split('/').filter(Boolean);
  const visible = showHidden ? items : items.filter((it) => !it.name.startsWith('.'));
  // Auto view: mostly files with >=80% images -> grid, otherwise list.
  const files = items.filter((it) => !it.is_dir);
  const fileRatio = items.length ? files.length / items.length : 0;
  const imageRatio = files.length ? files.filter((it) => PREVIEW_IMAGE.test(it.name)).length / files.length : 0;
  const autoView: 'list' | 'grid' = fileRatio >= 0.7 && imageRatio >= 0.8 ? 'grid' : 'list';
  useEffect(() => { if (tab === 'files' && !viewManual) setView(autoView); }, [autoView, tab, viewManual]);
  const parentPath = upPath(cwd, 1);
  const grandPath = upPath(cwd, 2);
  const showUp = parentPath !== cwd;
  const showUp2 = parentPath !== cwd && grandPath !== parentPath;
  // Grid layout: size columns/cards to use the full body width.
  const bodyW = vw.width || (typeof window !== 'undefined' ? window.innerWidth : 1200);
  const gridCols = Math.max(1, Math.floor((bodyW + GAP) / (CARD_W + GAP)));
  const cardW = Math.max(CARD_W, Math.floor((bodyW - (gridCols - 1) * GAP) / gridCols));

  interface NavItem { key: string; label: string; title: string; go: string }
  const nav: NavItem[] = [];
  if (showUp2) nav.push({ key: 'up2', label: '...', title: 'Go up two levels', go: grandPath });
  if (showUp) nav.push({ key: 'up1', label: '..', title: 'Go up one level', go: parentPath });

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
      const dest = prompt(`Copy ${sel.size} selected item(s) to directory (absolute path)`, '/');
      if (!dest) return;
      setError('');
      for (const p of sel) { try { await api.copy(scope, p, dest); } catch (e) { setError((e as Error).message); } }
      clearSel(); refresh();
    },
    move: async () => {
      if (!sel.size) return;
      const dest = prompt(`Move ${sel.size} selected item(s) to directory (absolute path)`, '/');
      if (!dest) return;
      setError('');
      for (const p of sel) { try { await api.move(scope, p, dest); } catch (e) { setError((e as Error).message); } }
      clearSel(); refresh();
    },
    del: async () => {
      if (!sel.size) return;
      if (!confirm(`Delete the ${sel.size} selected item(s)? (moved to trash)`)) return;
      setError('');
      for (const p of sel) { try { await api.remove(scope, p); } catch (e) { setError((e as Error).message); } }
      clearSel(); refresh();
    },
  };

  // ---- right-click context menu ----
  const openMenu = (e: React.MouseEvent, it: Item) => {
    e.preventDefault();
    e.stopPropagation();
    setMenu({ x: e.clientX, y: e.clientY, target: it });
  };
  // Right-click no longer changes selection: the menu acts on the clicked
  // item — or on the whole current selection when that item is part of it.
  const menuAct = (action: 'preview' | 'download' | 'share' | 'copy' | 'move' | 'rename' | 'delete') => {
    if (!menu) return;
    const target = menu.target;
    setMenu(null);
    const targets: Set<string> = sel.has(target.full) ? sel : new Set([target.full]);
    const multi = targets.size > 1;
    if (action === 'preview') { setPreview(target); return; }
    if (action === 'rename') { void rename(target); return; }
    if (action === 'share') {
      setError('');
      void api.share.create({ scope, path: target.full, allow_down: true })
        .then(({ token }) => setShareInfo(`${location.origin}/api/share/${token}/list`))
        .catch((e) => setError((e as Error).message));
      return;
    }
    if (action === 'download') {
      if (multi || target.is_dir) { void api.zipDownload(scope, [...targets]).catch((e) => setError((e as Error).message)); }
      else void api.downloadBlob(scope, target.full, target.name).catch(() => setError('Download failed'));
      return;
    }
    const mut = async (fn: (p: string) => Promise<unknown>) => {
      setError('');
      for (const p of targets) { try { await fn(p); } catch (e) { setError((e as Error).message); } }
      refresh();
    };
    if (action === 'copy') {
      const dest = prompt(`Copy ${targets.size} item(s) to directory (absolute path)`, '/');
      if (!dest) return;
      return void mut((p) => api.copy(scope, p, dest));
    }
    if (action === 'move') {
      const dest = prompt(`Move ${targets.size} item(s) to directory (absolute path)`, '/');
      if (!dest) return;
      return void mut((p) => api.move(scope, p, dest));
    }
    if (action === 'delete') {
      if (!confirm(`Delete the ${targets.size} selected item(s)? (moved to trash)`)) return;
      return void mut((p) => api.remove(scope, p));
    }
  };
  const rotateSel = async (angle: number) => {
    if (!menu) return;
    const target = menu.target;
    setMenu(null);
    const targets: Set<string> = sel.has(target.full) ? sel : new Set([target.full]);
    const paths = [...targets].filter((p) => PREVIEW_IMAGE.test(p));
    if (!paths.length) { setError('No image found among the selected items'); return; }
    setError('');
    try { await api.rotate(scope, paths, angle); bumpThumbs(); clearSel(); refresh(); }
    catch (e) { setError((e as Error).message); }
  };
  // Copy the share link, then close the modal.
  const shareCopy = async () => {
    try { await navigator.clipboard?.writeText(shareInfo); } catch { /* ignore */ }
    setShareInfo('');
  };

  return (
    <div className="sheet">
      <header className="topbar">
        <div className="brand">StarStack</div>
        <div className="scopes">
          <button className={tab === 'files' && scope === 'me' ? 'on' : ''} onClick={() => { setTab('files'); setScope('me'); setCwd('/'); setSel(new Set()); }}>My Space</button>
          <button className={tab === 'files' && scope === 'share' ? 'on' : ''} onClick={() => { setTab('files'); setScope('share'); setCwd('/'); setSel(new Set()); }}>Shared Drive</button>
        </div>
        <nav className="tabs">
          <button className={tab === 'trash' ? 'on' : ''} onClick={() => setTab('trash')}>Trash</button>
          <button className={tab === 'share' ? 'on' : ''} onClick={() => setTab('share')}>Shares</button>
          {user.is_admin && <button className={tab === 'admin' ? 'on' : ''} onClick={() => setTab('admin')}>Admin</button>}
        </nav>
        <div className="spacer" />
        <span className="muted">👤 {user.username}{user.is_admin ? ' (admin)' : ''}</span>
        <button className="ghost" onClick={onLogout}>Sign out</button>
      </header>

      {tab === 'files' && (
        <>
          <div className="toolbar">
            {sel.size > 0 ? (
              <>
                <span className="batch-count">{sel.size} selected</span>
                <button className="ghost ico" title="Download selected (zip)" onClick={() => void batch.download()}>⬇</button>
                <button className="ghost ico" title="Copy to…" onClick={() => void batch.copy()}>📋</button>
                <button className="ghost ico" title="Move to…" onClick={() => void batch.move()}>➜</button>
                <button className="ghost ico danger" title="Delete selected" onClick={() => void batch.del()}>🗑</button>
                <div className="spacer" />
                <button className="ghost" onClick={clearSel}>Cancel ✕</button>
              </>
            ) : (
              <>
                <button className="ghost ico" title="Upload" onClick={() => fileRef.current?.click()}>
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                    <path d="M12 16V4" /><path d="M6 10l6-6 6 6" /><path d="M4 20h16" />
                  </svg>
                </button>
                <button className="ghost ico" title="New folder" onClick={mkdir}>＋</button>
                <button className="ghost ico" title="Refresh" onClick={refresh}>↻</button>
                <input ref={fileRef} type="file" multiple hidden
                  onChange={(e) => { if (e.target.files) void doUpload(Array.from(e.target.files)); e.target.value = ''; }} />
                <div className="spacer" />
                <button className={`ghost toggle${showHidden ? ' on' : ''}`} title="Show/hide dotfiles"
                  onClick={() => setShowHidden((v) => !v)}>
                  <span className="hidden-ico">{showHidden ? '◉' : '◎'}</span>
                </button>
                <div className="view-toggle">
                  {view === 'list'
                    ? <button title="Auto view" onClick={() => { setView('grid'); setViewManual(true); }}>▦</button>
                    : <button title="Auto view" onClick={() => { setView('list'); setViewManual(true); }}>☰</button>}
                </div>
              </>
            )}
          </div>
          <div className="pathbar">
            <span className="path-scope">{scope === 'me' ? 'My Space' : 'Shared Drive'}</span>
            <span className="sep">/</span>
            <button className="link" onClick={() => go('/')} title="Root">root</button>
            {trail.map((seg, i) => (
              <span key={i}>
                <span className="sep">/</span>
                <button className="link" onClick={() => go('/' + trail.slice(0, i + 1).join('/'))}>{seg}</button>
              </span>
            ))}
          </div>
          {error && <div className="banner err">{error}</div>}
          {shareInfo && (
            <div className="modal-bg" onMouseDown={() => setShareInfo('')}>
              <div className="modal share-modal" onClick={(e) => e.stopPropagation()}>
                <div className="modal-actions">
                  <span className="modal-title">Share link created</span>
                  <button className="ghost" title="Close (Esc)" onClick={() => setShareInfo('')}>✕</button>
                </div>
                <div className="modal-body">
                  <p className="muted">Anyone with this link can view this folder:</p>
                  <input className="path-input" readOnly value={shareInfo} onFocus={(e) => e.currentTarget.select()} />
                  <div className="modal-btns">
                    <button className="ghost" onClick={() => void shareCopy()}>Copy</button>
                    <button className="ghost" onClick={() => setShareInfo('')}>Close</button>
                  </div>
                </div>
              </div>
            </div>
          )}
          <div ref={bodyRef} className={`body${drag ? ' drag' : ''}`}
            onDragOver={(e) => { e.preventDefault(); setDrag(true); }}
            onDragLeave={() => setDrag(false)}
            onDrop={(e) => { e.preventDefault(); setDrag(false); if (e.dataTransfer.files) void doUpload(Array.from(e.dataTransfer.files)); }}>
            {loading ? <p className="muted pad">Loading…</p> : visible.length === 0 ? <p className="muted pad">Empty folder — drag files here to upload{items.length ? ' (hidden files filtered, click "Hidden files" to show)' : ''}</p> : view === 'grid' ? <GridBulk
              items={visible} nav={nav}
              cols={gridCols} cardW={cardW}
              top={vw.top} height={vw.height}
              scope={scope} sel={sel} isImage={isImage}
              onToggle={toggleSel} onOpen={openItem} onMenu={openMenu} onNav={go} /> : (
              <ListBulk
                items={visible} nav={nav} top={vw.top} height={vw.height}
                scope={scope} sel={sel}
                selectAll={allSel} onSelectAll={toggleAll}
                onToggle={toggleSel} onOpen={openItem} onMenu={openMenu} onNav={go} />
            )}
          </div>
        </>
      )}

      {tab === 'trash' && <TrashPage />}
      {tab === 'share' && <SharesPage />}
      {tab === 'admin' && user.is_admin && <AdminPage />}

      {menu && (() => {
          const menuN = sel.has(menu.target.full) ? sel.size : 1;
          return (
        <>
          <div className="menu-bg" onMouseDown={() => setMenu(null)} onContextMenu={(e) => { e.preventDefault(); setMenu(null); }} />
          <div className="ctxmenu" style={{ left: menu.x, top: menu.y }}>
            <div className="ctx-title">{menu.target.is_dir ? '📁' : '📄'} {menu.target.name}{menuN > 1 ? `  (+${menuN - 1})` : ''}</div>
            <button disabled={menu.target.is_dir || menuN > 1 || !canPreview(menu.target.name)} onClick={() => menuAct('preview')}>👁 Preview</button>
            <button onClick={() => menuAct('download')}>⬇ {menuN > 1 ? `Download zip (${menuN})` : 'Download'}</button>
            <button disabled={menuN > 1 || !menu.target.is_dir} onClick={() => menuAct('share')}>🔗 Share folder</button>
            <div className="ctxsep" />
            <button onClick={() => menuAct('copy')}>📋 {menuN > 1 ? `Copy (${menuN})` : 'Copy to…'}</button>
            <button onClick={() => menuAct('move')}>➜ {menuN > 1 ? `Move (${menuN})` : 'Move to…'}</button>
            <button disabled={menuN > 1} onClick={() => menuAct('rename')}>✏️ Rename</button>
            {!menu.target.is_dir && PREVIEW_IMAGE.test(menu.target.name) && (
              <>
                <div className="ctxsep" />
                <div className="ctx-label">Rotate image</div>
                <button onClick={() => void rotateSel(90)}>⟳ Rotate 90° clockwise</button>
                <button onClick={() => void rotateSel(-90)}>⟲ Rotate 90° counter-clockwise</button>
                <button onClick={() => void rotateSel(180)}>⟲ Rotate 180°</button>
              </>
            )}
            <div className="ctxsep" />
            <button className="danger" onClick={() => menuAct('delete')}>🗑 {menuN > 1 ? `Delete (${menuN})` : 'Delete'}</button>
          </div>
        </>
          );
        })()}

      {preview && <PreviewModal item={preview} scope={scope} onClose={() => setPreview(null)} />}
    </div>
  );
}

const CARD_W = 172, GAP = 14, ROW_H = 196;
const ROW_LIST = 41;

/** Virtualized list: only mounts rows inside the viewport (+overscan). */
function ListBulk(props: {
  items: Item[]; nav: { key: string; label: string; title: string; go: string }[];
  top: number; height: number; scope: Scope; sel: Set<string>;
  selectAll: boolean; onSelectAll: () => void;
  onToggle: (full: string) => void; onOpen: (it: Item) => void;
  onMenu: (e: React.MouseEvent, it: Item) => void; onNav: (dir: string) => void;
}) {
  const { items, nav, top, height, sel, selectAll, onSelectAll, onToggle, onOpen, onMenu, onNav } = props;
  const total = nav.length + items.length;
  const startIdx = Math.max(0, Math.floor((top - ROW_LIST) / ROW_LIST));
  const endIdx = Math.min(total, Math.ceil((top + height + ROW_LIST) / ROW_LIST));
  const rows: number[] = [];
  for (let i = startIdx; i < endIdx; i++) rows.push(i);

  return (
    <div className="vlist">
      <div className="vlist-head">
        <div className="vchk" onClick={() => onSelectAll()}><input type="checkbox" title="Select/deselect all" checked={selectAll} onChange={onSelectAll} /></div>
        <div className="vname">Name</div><div className="vsize">Size</div><div className="vtime">Modified</div>
      </div>
      <div className="vlist-window" style={{ height: total * ROW_LIST + 6 }}>
        {rows.map((i) => {
          const style: React.CSSProperties = { top: i * ROW_LIST, height: ROW_LIST };
          if (i < nav.length) {
            const n = nav[i];
            return (
              <div key={`nav-${n.key}`} className="vlist-row nav" style={style} onClick={() => onNav(n.go)} title={n.title}>
                <div className="vchk">📁</div>
                <div className="vname"><button className="link">{n.label}</button></div>
                <div className="vsize">—</div><div className="vtime"></div>
              </div>
            );
          }
          const it = items[i - nav.length];
          return (
            <div key={it.full} style={style}
              className={`vlist-row${it.name.startsWith('.') ? ' ghidden' : ''}${sel.has(it.full) ? ' sel' : ''}`}
              onClick={() => onOpen(it)} onContextMenu={(e) => onMenu(e, it)}>
              <div className="vchk" onClick={(e) => { e.stopPropagation(); onToggle(it.full); }} title="Click to select/deselect">
                <input type="checkbox" checked={sel.has(it.full)} onChange={() => onToggle(it.full)} onClick={(e) => e.stopPropagation()} />
              </div>
              <div className="vname">
                <span className="type">{it.is_dir ? '📁' : '📄'}</span>
                <button className="link" onClick={(e) => { e.stopPropagation(); onOpen(it); }}>{it.name}</button>
              </div>
              <div className="vsize">{it.is_dir ? '—' : fmtSize(it.size)}</div>
              <div className="vtime">{new Date(it.mtime).toLocaleString()}</div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** Virtualized grid: only renders cells within the viewport (+overscan). */
function GridBulk(props: {
  items: Item[]; nav: { key: string; label: string; title: string; go: string }[];
  cols: number; cardW: number; top: number; height: number; scope: Scope; sel: Set<string>;
  isImage: (n: string) => boolean;
  onToggle: (full: string) => void; onOpen: (it: Item) => void;
  onMenu: (e: React.MouseEvent, it: Item) => void; onNav: (dir: string) => void;
}) {
  const { items, nav, cols, cardW, top, height, scope, sel, isImage, onToggle, onOpen, onMenu, onNav } = props;
  const totCells = nav.length + items.length;
  const overscanRows = 2; // prefetch ahead while idle
  const startIdx = Math.max(0, Math.floor((top - overscanRows*ROW_H) / ROW_H) * cols);
  const endIdx = Math.min(totCells, Math.ceil((top + height + overscanRows*ROW_H) / ROW_H) * cols);
  const rows = Math.ceil(totCells / cols);
  const cells: number[] = [];
  for (let i = startIdx; i < endIdx; i++) cells.push(i);

  return (
    <div className="grid-v" style={{ height: rows * ROW_H + 12 }}>
      {cells.map((i) => {
        const r = Math.floor(i / cols), c = i % cols;
        const style: React.CSSProperties = { width: cardW, height: ROW_H - GAP, transform: `translate(${c * (cardW + GAP)}px, ${r * ROW_H}px)` };
        if (i < nav.length) {
          const n = nav[i];
          return (
            <div key={`nav-${n.key}`} className="gcard gnav" style={style} onClick={() => onNav(n.go)} title={n.title}>
              <div className="gthumb gnav-big">{n.label}</div>
            </div>
          );
        }
        const it = items[i - nav.length];
        const isImg = isImage(it.name);
        return (
          <div key={it.full} style={style}
            className={`gcard${it.name.startsWith('.') ? ' ghidden' : ''}${sel.has(it.full) ? ' gcsel' : ''}`}
            onClick={() => onOpen(it)} onContextMenu={(e) => onMenu(e, it)}>
            <span className="gcheck" onClick={(e) => { e.stopPropagation(); onToggle(it.full); }} title="Click to select/deselect">
              <input type="checkbox" checked={sel.has(it.full)} onChange={() => onToggle(it.full)} onClick={(e) => e.stopPropagation()} />
            </span>
            <div className="gthumb">
              {it.is_dir ? <span className="gico folder">📁</span>
                : isImg ? <img className="gimg" src={thumbSrc(scope, it.full, 200, 60)} loading="lazy" alt={it.name} />
                : <span className="gico file">📄</span>}
            </div>
            <div className="gname" title={it.full} onClick={(e) => { e.stopPropagation(); onOpen(it); }}>{it.name}</div>
            <div className="gmeta">{it.is_dir ? 'Folder' : fmtSize(it.size)}</div>
          </div>
        );
      })}
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
        <button className="ghost" onClick={() => act(() => api.trash.empty())}>Empty trash</button>
        <button className="ghost" onClick={() => void reload()}>↻ Refresh</button>
        {err && <span className="err">{err}</span>}
      </div>
      <div className="body">
        {list.length === 0 ? <p className="muted pad">Trash is empty</p> : (
          <table className="list">
            <thead><tr><th>Original location</th><th>Size</th><th>Deleted at</th><th>Actions</th></tr></thead>
            <tbody>
              {list.map((t) => (
                <tr key={t.id}>
                  <td>{t.is_dir ? '📁' : '📄'} {t.orig_path}</td>
                  <td>{fmtSize(t.size)}</td>
                  <td>{new Date(t.deleted_at).toLocaleString()}</td>
                  <td className="actions">
                    <button className="ghost sm" onClick={() => act(() => api.trash.restore(t.id))}>Restore</button>
                    <button className="ghost sm danger" onClick={() => confirm('Permanently delete?') && act(() => api.trash.purge(t.id))}>Delete forever</button>
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

  const reload = useCallback(async () => {
    try { setShares((await api.share.list()).shares); } catch (e) { setErr((e as Error).message); }
  }, []);
  useEffect(() => { void reload(); }, [reload]);

  const copyLink = async (token: string) => {
    const link = `${location.origin}/api/share/${token}/list`;
    try { await navigator.clipboard?.writeText(link); setErr(''); }
    catch { setErr('Copy failed'); }
  };
  const scopeName = (sc: string) => (sc === 'me' ? 'My Space' : 'Shared Drive');

  return (
    <div className="tab-page">
      <div className="toolbar">
        <span>Shared folders: {shares.length}</span>
        <button className="ghost" onClick={() => void reload()}>↻ Refresh</button>
        <span className="muted">Right-click a folder in My Space or Shared Drive to create a share</span>
        {err && <span className="err">{err}</span>}
      </div>
      <div className="body">
        {shares.length === 0 ? <p className="muted pad">No shares yet — right-click a folder to create one</p> : (
          <table className="list">
            <thead><tr><th>Space</th><th>Path</th><th>Share link</th><th>Download</th><th>Uses</th><th>Status</th><th>Actions</th></tr></thead>
            <tbody>
              {shares.map((s) => {
                const et = s.expires_at ? new Date(s.expires_at).getTime() : 0;
                const never = !et || et <= 0 || new Date(s.expires_at).getUTCFullYear() < 2000;
                const expired = !never && et < Date.now();
                const link = `${location.origin}/api/share/${s.token}/list`;
                return (
                  <tr key={s.token}>
                    <td>{scopeName(s.scope)}</td>
                    <td>{s.path}</td>
                    <td><code title={link}>{s.token.slice(0, 8)}…</code>
                      <button className="ghost sm" onClick={() => void copyLink(s.token)}>Copy link</button>
                    </td>
                    <td>{s.allow_down ? '✓' : '—'}</td>
                    <td>{s.used}{s.max_uses ? `/${s.max_uses}` : ''}</td>
                    <td>{expired ? 'Expired' : 'Active'}</td>
                    <td className="actions"><button className="ghost sm danger" onClick={() => confirm('Delete this share?') && void api.share.remove(s.token).then(reload)}>Delete</button></td>
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
  const resetPw = async (id: number, name: string) => { const pw = prompt(`Set a new password for user ${name}`); if (pw) { try { await api.users.resetPassword(id, pw); } catch (e) { setErr((e as Error).message); } } };
  const del = async (id: number, name: string) => {
    if (!confirm(`Delete user "${name}"? This cannot be undone; the user's session and access will be revoked immediately.`)) return;
    try { await api.users.delete(id); void reload(); } catch (e) { setErr((e as Error).message); }
  };

  return (
    <div className="tab-page">
      <div className="toolbar">
        <input className="path-input" placeholder="New username" value={nu} onChange={(e) => setNu(e.target.value)} />
        <input className="path-input" placeholder="Initial password" type="password" value={np} onChange={(e) => setNp(e.target.value)} />
        <label className="chk"><input type="checkbox" checked={admin} onChange={(e) => setAdmin(e.target.checked)} /> Admin</label>
        <button className="ghost" onClick={create}>＋ Create user</button>
        {err && <span className="err">{err}</span>}
      </div>
      <div className="body">
        <table className="list">
          <thead><tr><th>ID</th><th>Username</th><th>Role</th><th>Status</th><th>Actions</th></tr></thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>{u.id}</td><td>{u.username}</td><td>{u.is_admin ? 'Admin' : 'User'}</td><td>{u.disabled ? 'Disabled' : 'Active'}</td>
                <td className="actions">
                  <button className="ghost sm" onClick={() => resetPw(u.id, u.username ?? String(u.id))}>Reset password</button>
                  <button className="ghost sm" onClick={() => void api.users.setState(u.id, !u.disabled).then(reload)}>{u.disabled ? 'Enable' : 'Disable'}</button>
                  <button className="ghost sm danger" title="Delete user" onClick={() => void del(u.id, u.username ?? String(u.id))}>Delete</button>
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
    if (PREVIEW_TEXT.test(item.name)) { setText(null); api.preview.text(scope, item.full).then(setText).catch(() => setText('(unreadable)')); }
  }, [item, scope]);
  const raw = api.preview.rawURL(scope, item.full);

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-actions">
          <button className="ghost" title="Download" onClick={() => void api.downloadBlob(scope, item.full, item.name)}>⬇</button>
          <button className="ghost" title="Close (Esc)" onClick={onClose}>✕</button>
        </div>
        <div className="modal-body">
          {PREVIEW_IMAGE.test(item.name) ? <img className="preview-img" src={raw} alt={item.name} />
            : PREVIEW_VIDEO.test(item.name) ? <video className="preview-media" src={raw} controls autoPlay />
            : PREVIEW_AUDIO.test(item.name) ? <audio src={raw} controls autoPlay />
            : PREVIEW_PDF.test(item.name) ? <iframe className="preview-iframe" src={raw} title={item.name} />
            : PREVIEW_TEXT.test(item.name) ? <pre className="preview-text">{text ?? 'Loading…'}</pre>
            : <p className="muted pad">This file cannot be previewed — please download it to view.</p>}
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