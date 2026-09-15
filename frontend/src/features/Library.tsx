import { useEffect, useState, useMemo } from 'react';
import { listBooks, listEpisodes, getLibrarySummary, coverUrl, type Book, type Episode, type BookSummary } from '../services/api';
import { BookCoverFallback, PlayIcon } from '../components/icons';
import { toast } from '../components/Toast';

interface Props {
  onOpen: (book: Book, episodes: Episode[]) => void;
  onResume: (book: Book, episodes: Episode[], epIndex: number, pos: number) => void;
}

type SortKey = 'recent' | 'title' | 'progress';

export default function Library({ onOpen, onResume }: Props) {
  const [books, setBooks] = useState<Book[]>([]);
  const [summary, setSummary] = useState<Record<string, BookSummary>>({});
  const [loading, setLoading] = useState(true);
  const [searchQuery, setSearchQuery] = useState('');
  const [sortBy, setSortBy] = useState<SortKey>('recent');
  const [openingId, setOpeningId] = useState(''); // 点击书籍拉取分集时显示 loading

  // 一次拉取书籍列表 + 聚合进度（替代逐书请求分集的 N+1 模式）
  useEffect(() => {
    (async () => {
      try {
        const [bs, sm] = await Promise.all([listBooks(), getLibrarySummary()]);
        setBooks(bs);
        setSummary(sm || {});
      } catch (e: any) { toast(e.message, 'error'); }
      finally { setLoading(false); }
    })();
  }, []);

  const bookProgress = (book: Book) => {
    const s = summary[book.id];
    const total = s?.total || book.episodeCount || 0;
    const doneCount = s?.done || 0;
    const startedCount = s?.started || 0;
    // 当前听到第几集（最近实际收听的分集序号，1 起）；无收听记录为 0
    const currentEp = s?.lastEpisodeIndex !== undefined && s.lastEpisodeIndex >= 0 ? s.lastEpisodeIndex + 1 : 0;
    return { pct: total > 0 ? Math.round((doneCount / total) * 100) : 0, doneCount, startedCount, total, currentEp };
  };

  const filteredBooks = useMemo(() => books.filter(b => {
    // 过滤掉分集数为 0 的空书壳
    const total = summary[b.id]?.total ?? b.episodeCount ?? 0;
    if (total === 0) {
      return false;
    }
    if (!searchQuery.trim()) return true;
    const q = searchQuery.toLowerCase();
    return b.title.toLowerCase().includes(q) || (b.author && b.author.toLowerCase().includes(q));
  }), [books, summary, searchQuery]);

  // 排序：最近收听（默认）/ 名称（中文拼音序）/ 完成度
  const sortedBooks = useMemo(() => {
    const arr = [...filteredBooks];
    if (sortBy === 'title') {
      arr.sort((a, b) => a.title.localeCompare(b.title, 'zh-Hans-CN'));
    } else if (sortBy === 'progress') {
      arr.sort((a, b) => {
        const pa = bookProgress(a), pb = bookProgress(b);
        return pb.pct - pa.pct || a.title.localeCompare(b.title, 'zh-Hans-CN');
      });
    } else {
      arr.sort((a, b) => {
        const ta = summary[a.id]?.lastUpdated || '';
        const tb = summary[b.id]?.lastUpdated || '';
        if (ta === tb) return a.title.localeCompare(b.title, 'zh-Hans-CN');
        if (!ta) return 1;  // 从未收听的排后面
        if (!tb) return -1;
        return tb < ta ? -1 : 1; // 字符串时间戳倒序
      });
    }
    return arr;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filteredBooks, sortBy, summary]);

  // "继续收听"横排：有进度且未听完的书，按最近收听排序，取前 10
  const continueBooks = useMemo(() => {
    return books
      .filter(b => {
        const s = summary[b.id];
        const total = s?.total ?? b.episodeCount ?? 0;
        return s && s.started > 0 && s.done < total && s.lastUpdated;
      })
      .sort((a, b) => (summary[b.id]?.lastUpdated || '') < (summary[a.id]?.lastUpdated || '') ? -1 : 1)
      .slice(0, 10);
  }, [books, summary]);

  const handleClick = async (book: Book) => {
    if (openingId) return; // 防重复点击
    setOpeningId(book.id);
    try {
      const eps = await listEpisodes(book.id);
      // 有收听记录则直接断点续播
      const s = summary[book.id];
      if (s?.lastEpisodeId) {
        const idx = eps.findIndex(e => e.id === s.lastEpisodeId);
        if (idx >= 0) { onResume(book, eps, idx, s.lastPosition || 0); return; }
      }
      onOpen(book, eps);
    } catch (e: any) {
      toast('加载章节失败：' + e.message, 'error');
    } finally {
      setOpeningId('');
    }
  };

  const sortBtn = (key: SortKey, label: string) =>
    `px-2.5 py-1 text-xs font-medium rounded-lg transition ${sortBy === key
      ? 'bg-white dark:bg-slate-700 text-slate-900 dark:text-white shadow-sm'
      : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200'}`;

  return (
    <div className="mx-auto w-full max-w-6xl px-4 sm:px-6 lg:px-8 py-6 sm:py-8">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6">
        <div>
          <h2 className="text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-100">我的书库</h2>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">共收录 {books.length} 部有声书作品</p>
        </div>
        <div className="flex items-center gap-3">
          {/* 排序：最近收听 / 名称 / 完成度 */}
          <div className="flex items-center p-1 bg-slate-100 dark:bg-slate-800/80 rounded-xl shrink-0">
            <button onClick={() => setSortBy('recent')} className={sortBtn('recent', '最近')}>最近</button>
            <button onClick={() => setSortBy('title')} className={sortBtn('title', '名称')}>名称</button>
            <button onClick={() => setSortBy('progress')} className={sortBtn('progress', '进度')}>进度</button>
          </div>
          <input
            type="text"
            placeholder="搜索书名或作者..."
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            className="w-full sm:w-64 px-3.5 py-1.5 text-sm bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl text-slate-900 dark:text-slate-100 placeholder-slate-400 focus:outline-none focus:border-indigo-500 shadow-xs transition"
          />
        </div>
      </div>

      {/* 骨架屏：与书籍卡片同构的脉冲占位（替代纯文本"加载中"） */}
      {loading && (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4 sm:gap-6">
          {Array.from({ length: 12 }).map((_, i) => (
            <div key={i} className="bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-2xl overflow-hidden animate-pulse">
              <div className="w-full aspect-[3/4] bg-slate-200 dark:bg-slate-800" />
              <div className="p-3.5 space-y-2">
                <div className="h-3.5 bg-slate-200 dark:bg-slate-800 rounded w-4/5" />
                <div className="h-2.5 bg-slate-200 dark:bg-slate-800 rounded w-1/2" />
              </div>
            </div>
          ))}
        </div>
      )}

      {/* 继续收听横排：最近在听且未读完的书（搜索时隐藏） */}
      {!loading && !searchQuery.trim() && continueBooks.length > 0 && (
        <section className="mb-8">
          <h3 className="text-sm font-semibold text-slate-700 dark:text-slate-300 mb-3 flex items-center gap-2">
            <span className="w-1.5 h-1.5 rounded-full bg-indigo-500 animate-pulse" /> 继续收听
          </h3>
          <div className="flex gap-4 overflow-x-auto pb-2 -mx-1 px-1 snap-x scrollbar-thin">
            {continueBooks.map(b => {
              const { pct, currentEp, total } = bookProgress(b);
              return (
                <div key={b.id} onClick={() => handleClick(b)}
                  className="snap-start shrink-0 w-32 sm:w-36 group cursor-pointer">
                  <div className="relative">
                    <BookCover bookId={b.id} title={b.title} />
                    <div className="absolute inset-0 rounded-none bg-black/0 group-hover:bg-black/30 flex items-center justify-center transition-colors">
                      <span className="w-10 h-10 rounded-full bg-indigo-600 text-white flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity shadow-lg">
                        <PlayIcon className="w-4 h-4 ml-0.5" />
                      </span>
                    </div>
                    {openingId === b.id && (
                      <div className="absolute inset-0 bg-black/40 flex items-center justify-center">
                        <svg className="w-7 h-7 text-white animate-spin" viewBox="0 0 24 24" fill="none">
                          <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                          <path className="opacity-90" d="M4 12a8 8 0 018-8" stroke="currentColor" strokeWidth="4" strokeLinecap="round" />
                        </svg>
                      </div>
                    )}
                  </div>
                  <div className="mt-2">
                    <div className="text-xs font-semibold truncate text-slate-900 dark:text-slate-100">{b.title}</div>
                    <div className="h-1 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden mt-1.5">
                      <div className="h-full bg-gradient-to-r from-indigo-500 to-purple-600 rounded-full" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="text-[10px] text-slate-500 dark:text-slate-400 mt-1 font-medium">听到第 {currentEp} 集 / 共 {total} 集</div>
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      )}

      {!loading && sortedBooks.length === 0 && (
        <div className="text-center py-20 text-slate-500 dark:text-slate-400 bg-white/50 dark:bg-slate-900/40 border border-slate-200 dark:border-slate-800/80 rounded-2xl p-8">
          <BookCoverFallback className="w-16 h-16 mx-auto mb-4 text-slate-400 dark:text-slate-600" />
          <p className="text-lg font-semibold text-slate-800 dark:text-slate-200">{searchQuery ? '未找到相关书籍' : '暂无书籍'}</p>
          <p className="text-sm mt-1 text-slate-500 dark:text-slate-400">
            {searchQuery ? '请尝试更换搜索关键字' : '请在「管理 → 书源存储」添加存储源并执行扫描'}
          </p>
        </div>
      )}
      <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6 gap-4 sm:gap-6">
        {sortedBooks.map(b => {
          const { pct, doneCount, startedCount, total, currentEp } = bookProgress(b);
          const hasProgress = startedCount > 0;
          const inProgress = hasProgress && doneCount < total;
          const opening = openingId === b.id;
          return (
            <div key={b.id} onClick={() => handleClick(b)}
              className={`group bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-2xl overflow-hidden cursor-pointer hover:border-indigo-500 hover:shadow-xl hover:shadow-indigo-500/10 hover:-translate-y-1 transition-all duration-300 ${opening ? 'opacity-60 pointer-events-none' : ''}`}>
              <div className="relative">
                <BookCover bookId={b.id} title={b.title} />
                {/* 点击加载分集时的转圈遮罩（慢网盘下提供反馈） */}
                {opening && (
                  <div className="absolute inset-0 bg-black/40 flex items-center justify-center">
                    <svg className="w-8 h-8 text-white animate-spin" viewBox="0 0 24 24" fill="none">
                      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                      <path className="opacity-90" d="M4 12a8 8 0 018-8" stroke="currentColor" strokeWidth="4" strokeLinecap="round" />
                    </svg>
                  </div>
                )}
              </div>
              <div className="p-3.5">
                <div className="text-sm font-semibold truncate text-slate-900 dark:text-slate-100 group-hover:text-indigo-600 dark:group-hover:text-indigo-400 transition-colors">{b.title}</div>
                {b.author && <div className="text-xs text-slate-500 dark:text-slate-400 truncate mt-0.5">{b.author}</div>}
                {inProgress && (
                  <div className="mt-2 inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full bg-indigo-50 dark:bg-indigo-950/60 border border-indigo-200 dark:border-indigo-800 text-[10px] text-indigo-600 dark:text-indigo-400 font-medium">
                    <span className="w-1.5 h-1.5 rounded-full bg-indigo-500 animate-pulse" /> 继续收听
                  </div>
                )}
                {hasProgress && (
                  <div className="mt-2.5">
                    <div className="h-1.5 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden">
                      <div className="h-full bg-gradient-to-r from-indigo-500 to-purple-600 rounded-full transition-all duration-500" style={{ width: `${pct}%` }} />
                    </div>
                    <div className="text-[11px] text-slate-500 dark:text-slate-400 mt-1 flex justify-between font-medium">
                      <span>{currentEp > 0 ? `听到第 ${currentEp} 集` : `已听完 ${doneCount}/${total} 集`}</span>
                      <span>{pct}%</span>
                    </div>
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// BookCover 封面加载完成后淡入（消除图片弹出生硬感），失败显示占位
function BookCover({ bookId, title }: { bookId: string; title: string }) {
  const [err, setErr] = useState(false);
  const [loaded, setLoaded] = useState(false);
  if (err) return (
    <div className="w-full aspect-[3/4] bg-gradient-to-br from-gray-800 via-gray-800 to-indigo-900/40 flex items-center justify-center select-none">
      <BookCoverFallback className="w-14 h-14 text-gray-600" />
    </div>
  );
  return (
    <div className="w-full aspect-[3/4] bg-slate-200 dark:bg-slate-800 relative overflow-hidden">
      {!loaded && <div className="absolute inset-0 animate-pulse" />}
      <img src={coverUrl(bookId)} alt={title}
        onError={() => setErr(true)} onLoad={() => setLoaded(true)}
        className={`w-full h-full object-cover transition-opacity duration-500 ${loaded ? 'opacity-100' : 'opacity-0'}`}
        loading="lazy" />
    </div>
  );
}
