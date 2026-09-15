import { useEffect, useState, useRef, useMemo } from 'react';
import { type Book, type Episode, getProgress, updateBook, rescanBook, listEpisodes, coverUrl, uploadBookCover } from '../services/api';
import { BookCoverFallback, PlayIcon } from '../components/icons';
import { toast } from '../components/Toast';

const PAGE_SIZE = 50; // 目录分页：长书（千集级）按页管理，避免无限滚动难定位

export default function BookDetail({ book, episodes, onPlay, onBack, onBookUpdated, onEpisodesUpdated, canEdit }: {
  book: Book; episodes: Episode[]; onPlay: (ep: Episode, idx: number, pos: number) => void; onBack: () => void; onBookUpdated?: (b: Book) => void;
  onEpisodesUpdated?: (eps: Episode[]) => void; canEdit?: boolean;
}) {
  const [progressMap, setProgressMap] = useState<Record<string, { position: number; duration: number; isFinished: boolean; updatedAt?: string }>>({});
  const [showEdit, setShowEdit] = useState(false);
  const [eTitle, setETitle] = useState(book.title);
  const [eAuthor, setEAuthor] = useState(book.author);
  const [eDesc, setEDesc] = useState(book.description);
  const [uploading, setUploading] = useState(false);
  const [coverVersion, setCoverVersion] = useState(0);
  const [page, setPage] = useState(0); // 目录分页（0 起）
  const [jumpPage, setJumpPage] = useState(''); // 跳页输入
  const fileInputRef = useRef<HTMLInputElement>(null);
  const listTopRef = useRef<HTMLDivElement>(null);

  const totalPages = Math.max(1, Math.ceil(episodes.length / PAGE_SIZE));

  useEffect(() => {
    setPage(0); // 切书重置分页
    getProgress(undefined, book.id).then(data => {
      const map: Record<string, { position: number; duration: number; isFinished: boolean; updatedAt?: string }> = {};
      if (Array.isArray(data)) for (const p of data as any[]) map[p.episodeId] = { position: p.position || 0, duration: p.duration || 0, isFinished: !!p.isFinished, updatedAt: p.updatedAt };
      setProgressMap(map);
    }).catch(() => {});
  }, [book.id]);

  // 继续收听横幅：取"最近实际收听"的那一集（按 updatedAt 最新），
  // 而非"第一个未听完"——用户可能中途跳集，部分收听的旧集不应被误当作当前进度。
  // 最近一集已听完：有下一集则从下一集开头继续；已是最后一集则不显示（全书完）。
  const resumeInfo = useMemo(() => {
    // 数值化比较 updatedAt：RFC3339 带时区偏移，字符串字典序在跨时区数据下不可靠
    let last: { epId: string; at: number } | null = null;
    for (const [epId, prog] of Object.entries(progressMap)) {
      if (prog.position <= 0 && !prog.isFinished) continue;
      const at = Date.parse(prog.updatedAt || '') || 0;
      if (!last || at > last.at) last = { epId, at };
    }
    if (!last) return null;
    const idx = episodes.findIndex(e => e.id === last!.epId);
    if (idx < 0) return null;
    const prog = progressMap[last.epId];
    if (prog.isFinished) {
      if (idx + 1 < episodes.length) return { ep: episodes[idx + 1], idx: idx + 1, pos: 0 };
      return null;
    }
    return { ep: episodes[idx], idx, pos: prog.position };
  }, [episodes, progressMap]);

  const handlePlay = (ep: Episode, idx: number) => onPlay(ep, idx, progressMap[ep.id]?.position || 0);

  const handleSave = async () => {
    try {
      await updateBook(book.id, { title: eTitle, author: eAuthor, description: eDesc });
      toast('已保存书籍元数据', 'success'); setShowEdit(false);
      onBookUpdated && onBookUpdated({ ...book, title: eTitle, author: eAuthor, description: eDesc });
    } catch (e: any) { toast(e.message, 'error'); }
  };

  const handleCoverUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const res = await uploadBookCover(book.id, file);
      toast('封面已上传并保存到存储目录', 'success');
      setCoverVersion(v => v + 1);
      onBookUpdated && onBookUpdated(res.book);
    } catch (err: any) {
      toast(err.message || '封面上传失败', 'error');
    } finally {
      setUploading(false);
      if (fileInputRef.current) fileInputRef.current.value = '';
    }
  };

  const handleRescan = async () => {
    try {
      const r: any = await rescanBook(book.id);
      toast(`已重新刮削：${r.episodes} 集`, 'success');
      // 重新拉取分集并更新 App 层 state，用户看到的列表立即刷新
      try {
        const eps = await listEpisodes(book.id);
        onEpisodesUpdated?.(eps);
      } catch { /* 刷新失败不影响提示 */ }
    } catch (e: any) { toast(e.message, 'error'); }
  };

  const fmt = (s: number) => `${Math.floor(s / 60)}:${(Math.round(s) % 60).toString().padStart(2, '0')}`;
  // 当前页分集（分页管理：翻页可控、可跳转，替代无限加载）
  const pageEpisodes = episodes.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE);

  // 翻页：clamp 后滚动到目录顶部，便于连续浏览
  const goToPage = (p: number) => {
    const np = Math.max(0, Math.min(totalPages - 1, p));
    if (np === page) return;
    setPage(np);
    setJumpPage('');
    listTopRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  };

  return (
    <div className="mx-auto w-full max-w-6xl px-4 sm:px-6 lg:px-8 py-6 sm:py-8 pb-28">
      <button onClick={onBack} className="text-sm font-medium text-indigo-600 dark:text-indigo-400 hover:text-indigo-500 mb-6 transition flex items-center gap-1.5">
        ← 返回书库
      </button>

      <div className="flex flex-col sm:flex-row gap-6 mb-8 bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 p-5 rounded-2xl shadow-sm">
        <div className="relative group self-center sm:self-start shrink-0">
          <BookCover key={coverVersion} bookId={book.id} title={book.title} className="w-32 h-44 sm:w-40 sm:h-52 md:w-48 md:h-64 rounded-xl shadow-md object-cover" />
          {canEdit && (
            <>
              <input ref={fileInputRef} type="file" accept="image/*" className="hidden" onChange={handleCoverUpload} />
              <button
                onClick={() => fileInputRef.current?.click()}
                disabled={uploading}
                className="absolute inset-0 bg-black/50 opacity-0 group-hover:opacity-100 flex flex-col items-center justify-center text-white text-xs font-semibold rounded-xl transition duration-200 backdrop-blur-xs cursor-pointer"
              >
                <span>{uploading ? '上传中...' : '更换封面'}</span>
                <span className="text-[10px] text-slate-300 mt-1">保存至网盘目录</span>
              </button>
            </>
          )}
        </div>
        <div className="flex-1 min-w-0">
          <h2 className="text-2xl font-bold leading-tight text-slate-900 dark:text-slate-100">{book.title}</h2>
          {book.author && <p className="text-sm text-slate-600 dark:text-slate-400 mt-2 font-medium">作者：{book.author}</p>}
          {book.storageName && <p className="text-xs text-slate-500 dark:text-slate-400 mt-1.5">存储源：{book.storageName}</p>}
          {book.description && <p className="text-sm text-slate-600 dark:text-slate-400 mt-3 leading-relaxed line-clamp-3">{book.description}</p>}
          {canEdit && (
            <div className="flex flex-wrap gap-2.5 mt-5">
              <button onClick={() => { setETitle(book.title); setEAuthor(book.author); setEDesc(book.description); setShowEdit(true); }}
                className="px-3.5 py-1.5 text-xs font-medium bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg transition">编辑信息</button>
              <button onClick={() => fileInputRef.current?.click()}
                className="px-3.5 py-1.5 text-xs font-medium bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg transition">上传封面</button>
              <button onClick={handleRescan} className="px-3.5 py-1.5 text-xs font-medium bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg transition">重新刮削</button>
            </div>
          )}
        </div>
      </div>

      {/* 继续收听横幅：缩短高频路径（数据来自已加载的进度） */}
      {resumeInfo && (
        <button onClick={() => handlePlay(resumeInfo.ep, resumeInfo.idx)}
          className="w-full mb-6 flex items-center gap-4 px-5 py-4 bg-gradient-to-r from-indigo-600 to-purple-600 hover:from-indigo-500 hover:to-purple-500 text-white rounded-2xl shadow-lg shadow-indigo-600/25 transition-all group">
          <span className="w-11 h-11 rounded-full bg-white/20 flex items-center justify-center shrink-0 group-hover:scale-105 transition-transform">
            <PlayIcon className="w-5 h-5 ml-0.5" />
          </span>
          <span className="flex-1 min-w-0 text-left">
            <span className="block text-sm font-bold truncate">{resumeInfo.ep.title}</span>
            <span className="block text-xs text-indigo-100/90 mt-0.5">
              继续收听 · 第 {resumeInfo.idx + 1} 集 · {resumeInfo.pos > 0 ? `已听 ${fmt(resumeInfo.pos)}` : '从头播放'}
              {resumeInfo.pos > 0 && progressMap[resumeInfo.ep.id]?.duration > 0 && ` / ${fmt(progressMap[resumeInfo.ep.id].duration)}`}
            </span>
          </span>
          <span className="text-xs font-semibold shrink-0 opacity-80">▶ 播放</span>
        </button>
      )}

      <div ref={listTopRef} className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-semibold text-slate-700 dark:text-slate-300">分集列表 · 共 {episodes.length} 集</h3>
      </div>
      <div className="space-y-2">
        {pageEpisodes.map((ep, idx) => {
          const absIdx = page * PAGE_SIZE + idx; // 全局序号（分页下仍显示真实集数）
          const prog = progressMap[ep.id];
          const pct = prog && prog.duration > 0 ? Math.min(100, Math.round((prog.position / prog.duration) * 100)) : 0;
          const finished = prog?.isFinished;
          // 分集时长：扫描入库时 duration 未知（0），优先用播放后上报的进度时长
          const epDuration = prog?.duration || ep.duration;
          return (
          <div key={ep.id} onClick={() => handlePlay(ep, absIdx)}
            className="group flex items-center gap-3.5 px-4 py-3 bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-xl cursor-pointer hover:border-indigo-500 hover:bg-indigo-50/30 dark:hover:bg-indigo-950/20 transition-all shadow-sm">
            {/* 序号徽章：默认显示全局序号，hover 切换为播放图标（长书导航） */}
            <div className="w-9 h-9 rounded-lg bg-slate-100 dark:bg-slate-800 flex items-center justify-center shrink-0 group-hover:bg-indigo-600 group-hover:text-white text-slate-500 dark:text-slate-400 transition-colors relative">
              <span className="text-xs font-bold tabular-nums group-hover:opacity-0">{absIdx + 1}</span>
              <PlayIcon className="w-4 h-4 ml-0.5 absolute opacity-0 group-hover:opacity-100" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-sm font-medium truncate text-slate-900 dark:text-slate-100 group-hover:text-indigo-600 dark:group-hover:text-indigo-400">{ep.title}</div>
              {epDuration > 0 && <div className="text-xs text-slate-500 dark:text-slate-400 mt-0.5 font-medium">{fmt(epDuration)}</div>}
              {prog && prog.position > 0 && (
                <div className="mt-1.5 flex items-center gap-2 max-w-xs">
                  <div className="h-1.5 flex-1 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden">
                    <div className="h-full bg-gradient-to-r from-indigo-500 to-purple-600 rounded-full transition-all duration-300" style={{ width: `${pct}%` }} />
                  </div>
                  <span className="text-[10px] text-slate-500 dark:text-slate-400 font-medium shrink-0">{pct}%</span>
                </div>
              )}
            </div>
            {finished ? (
              <div className="text-xs font-medium text-emerald-600 dark:text-emerald-400 whitespace-nowrap shrink-0 px-2 py-0.5 rounded bg-emerald-50 dark:bg-emerald-950/40 border border-emerald-200 dark:border-emerald-800/60">已听完</div>
            ) : prog && prog.position > 0 ? (
              <div className="text-xs font-medium text-indigo-600 dark:text-indigo-400 whitespace-nowrap shrink-0">已听 {fmt(prog.position)}</div>
            ) : null}
          </div>
          );
        })}
      </div>

      {/* 分页控件：长书目录按页管理（50 集/页），支持跳页 */}
      {totalPages > 1 && (
        <div className="flex flex-wrap items-center justify-center gap-1.5 mt-5 select-none">
          <button onClick={() => goToPage(0)} disabled={page === 0} title="首页"
            className="px-2.5 py-1.5 text-xs font-medium rounded-lg bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 disabled:opacity-40 transition">«</button>
          <button onClick={() => goToPage(page - 1)} disabled={page === 0}
            className="px-3 py-1.5 text-xs font-medium rounded-lg bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 disabled:opacity-40 transition">上一页</button>
          <span className="text-xs text-slate-500 dark:text-slate-400 font-medium tabular-nums px-1">
            第 {page + 1} / {totalPages} 页
          </span>
          <button onClick={() => goToPage(page + 1)} disabled={page >= totalPages - 1}
            className="px-3 py-1.5 text-xs font-medium rounded-lg bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 disabled:opacity-40 transition">下一页</button>
          <button onClick={() => goToPage(totalPages - 1)} disabled={page >= totalPages - 1} title="末页"
            className="px-2.5 py-1.5 text-xs font-medium rounded-lg bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-200 hover:bg-slate-200 dark:hover:bg-slate-700 disabled:opacity-40 transition">»</button>
          {/* 跳页：输入页码回车直达（千集书快速定位） */}
          <form className="flex items-center gap-1 ml-2" onSubmit={e => {
            e.preventDefault();
            const n = parseInt(jumpPage, 10);
            if (!isNaN(n)) goToPage(n - 1);
          }}>
            <input
              type="number" min={1} max={totalPages} value={jumpPage}
              onChange={e => setJumpPage(e.target.value)}
              placeholder="页码"
              className="w-14 px-2 py-1 text-xs bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-lg text-center text-slate-900 dark:text-slate-100 focus:outline-none focus:border-indigo-500 transition" />
            <button type="submit"
              className="px-2.5 py-1 text-xs font-medium rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white transition">跳转</button>
          </form>
        </div>
      )}

      {showEdit && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl p-6 w-full max-w-md shadow-2xl text-slate-900 dark:text-slate-100">
            <h3 className="text-lg font-bold mb-4">编辑书籍信息</h3>
            <div className="space-y-3.5">
              <div>
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400 block mb-1">书名</label>
                <input className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-white focus:outline-none focus:border-indigo-500 transition" value={eTitle} onChange={e => setETitle(e.target.value)} />
              </div>
              <div>
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400 block mb-1">作者</label>
                <input className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-white focus:outline-none focus:border-indigo-500 transition" value={eAuthor} onChange={e => setEAuthor(e.target.value)} />
              </div>
              <div>
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400 block mb-1">简介</label>
                <textarea className="w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-white focus:outline-none focus:border-indigo-500 transition" rows={3} value={eDesc} onChange={e => setEDesc(e.target.value)} />
              </div>
            </div>
            <div className="flex gap-2 mt-6">
              <button onClick={handleSave} className="flex-1 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition shadow-md shadow-indigo-600/20">保存</button>
              <button onClick={() => setShowEdit(false)} className="flex-1 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg text-sm font-medium transition">取消</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function BookCover({ bookId, title, className }: { bookId: string; title: string; className?: string }) {
  const [err, setErr] = useState(false);
  const [loaded, setLoaded] = useState(false);
  if (err) return <div className={`${className} bg-gradient-to-br from-gray-800 via-gray-800 to-indigo-900/40 flex items-center justify-center select-none`}><BookCoverFallback className="w-10 h-10 text-gray-600" /></div>;
  return (
    <div className={`${className} relative overflow-hidden bg-slate-200 dark:bg-slate-800`}>
      {!loaded && <div className="absolute inset-0 animate-pulse" />}
      <img src={coverUrl(bookId)} alt={title} onError={() => setErr(true)} onLoad={() => setLoaded(true)}
        className={`w-full h-full object-cover transition-opacity duration-500 ${loaded ? 'opacity-100' : 'opacity-0'}`} />
    </div>
  );
}
