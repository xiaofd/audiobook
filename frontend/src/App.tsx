import { useState, useEffect, useCallback, useRef, Suspense, lazy } from 'react';
import { me, logout as apiLogout, getUserSettings, saveUserSettings, getProgress, setOnUnauthorized, type Book, type Episode, type UserSettings } from './services/api';
import { usePlayer } from './hooks/usePlayer';
import { useMediaSession } from './hooks/useMediaSession';
import Login from './features/Login';
import Library from './features/Library';
import BookDetail from './features/BookDetail';
import PlayerView from './features/Player';
import MiniPlayer from './features/MiniPlayer';
import ChangePasswordDialog from './features/ChangePasswordDialog';
import { ToastHost } from './components/Toast';
import { BookOpenIcon, SettingsIcon, LogOutIcon, HeadphonesIcon, SunIcon, MoonIcon, KeyIcon } from './components/icons';

// 管理页懒加载：普通用户永远不需要加载管理端代码（约 60KB）
const Admin = lazy(() => import('./features/Admin'));

type View = 'library' | 'book' | 'player' | 'admin' | 'login';

const VALID_VIEWS: View[] = ['library', 'book', 'player', 'admin', 'login'];

export default function App() {
  // authing: 启动时校验登录态的中间态，避免已登录用户看到登录页闪烁
  const [authing, setAuthing] = useState(true);
  const [user, setUser] = useState<any>(null);
  const [view, setView] = useState<View>('library');
  // user 的 latest-ref：popstate 回调读取最新登录态做守卫，避免闭包过期
  const userRef = useRef<any>(null);
  userRef.current = user;
  const [selectedBook, setSelectedBook] = useState<Book | null>(null);
  const [episodes, setEpisodes] = useState<Episode[]>([]);
  const [epIndex, setEpIndex] = useState(0);
  const [showChangePw, setShowChangePw] = useState(false);
  const [isDark, setIsDark] = useState<boolean>(() => {
    const saved = localStorage.getItem('theme');
    if (saved) return saved === 'dark';
    return true; // 默认深色
  });
  // 快退/快进秒数（用户偏好，供播放器按钮与锁屏控制使用）
  const [skipSec, setSkipSec] = useState(15);
  const skipRef = useRef(15);
  skipRef.current = skipSec;

  // 应用用户偏好（倍速/主题/连播/快进快退），登录与刷新后共用。
  // playerRef 用于打破 handlePlay → player → handleAutoNext 的循环依赖（latest-ref 模式）
  const playerRef = useRef<any>(null);
  // 连播开关等偏好的 latest-ref：连播回调触发时读取最新值，避免闭包过期
  const autoNextRef = useRef(true);
  const [autoNext, setAutoNext] = useState(true);
  autoNextRef.current = autoNext;
  const applyUserSettings = useCallback((st: UserSettings) => {
    if (!st) return;
    setAutoNext(st.autoNext !== false);
    if (st.skipForward > 0) setSkipSec(st.skipForward);
    if (st.playbackRate > 0) {
      playerRef.current?.setPlaybackRate(st.playbackRate);
    }
    if (st.theme === 'light') {
      setIsDark(false);
    } else if (st.theme === 'dark') {
      setIsDark(true);
    }
  }, []);

  // 切换连播并持久化到用户偏好
  const handleAutoNextChange = useCallback((v: boolean) => {
    setAutoNext(v);
    saveUserSettings({ autoNext: v }).catch(() => {});
  }, []);

  // --- 浏览器历史集成：视图切换压入历史栈 ---
  // 手机 PWA/浏览器的返回键 = history.back()，若不压栈会直接跳出应用。
  // 前进导航用 navigate（pushState），应用内返回按钮用 goBack（真实回退，与返回键行为一致）。
  const navigate = useCallback((v: View) => {
    setView(v);
    history.pushState({ view: v }, '');
  }, []);

  const goBack = useCallback(() => history.back(), []);

  // popstate：返回键回退时恢复目标视图（带登录态与管理员权限守卫）
  useEffect(() => {
    const onPop = (e: PopStateEvent) => {
      const v = (e.state as { view?: View } | null)?.view;
      let next: View = v && VALID_VIEWS.includes(v) ? v : 'library';
      if (!userRef.current) next = 'login'; // 未登录时任何回退都落在登录页
      else if (next === 'admin' && userRef.current.role !== 'admin') next = 'library';
      setView(next);
    };
    window.addEventListener('popstate', onPop);
    history.replaceState({ view: 'library' }, '');
    return () => window.removeEventListener('popstate', onPop);
  }, []);

  const handlePlay = useCallback((ep: Episode, idx: number, startPos = 0) => {
    setEpIndex(idx); playerRef.current?.play(ep, startPos); navigate('player');
  }, [navigate]);

  // 连播回调：当前集播放结束时，如果开启连播且有下一集，自动切至下一集并播放
  const handleAutoNext = useCallback(() => {
    if (!autoNextRef.current) return;
    if (epIndex < episodes.length - 1) {
      const nextIdx = epIndex + 1;
      const nextEp = episodes[nextIdx];
      if (nextEp) {
        // 下一集从 0 开始连续播放
        handlePlay(nextEp, nextIdx, 0);
      }
    }
  }, [epIndex, episodes, handlePlay]);

  const player = usePlayer(handleAutoNext);
  playerRef.current = player;

  useEffect(() => {
    if (isDark) {
      document.documentElement.classList.add('dark');
      localStorage.setItem('theme', 'dark');
    } else {
      document.documentElement.classList.remove('dark');
      localStorage.setItem('theme', 'light');
    }
  }, [isDark]);

  const handleLogout = useCallback(() => {
    apiLogout(); setUser(null); navigate('login');
    document.title = '有声书播放器';
  }, [navigate]);

  // Token 过期全局处理：任意请求收到 401 → 清登录态回登录页
  useEffect(() => { setOnUnauthorized(handleLogout); }, [handleLogout]);

  // 启动时校验登录态并加载偏好
  useEffect(() => {
    me().then(u => {
      setUser(u);
      setAuthing(false);
      getUserSettings().then(applyUserSettings).catch(() => {});
    }).catch(() => { setAuthing(false); setView('login'); });
  }, [applyUserSettings]);

  // 标签页标题跟随播放内容（PWA 切后台/多标签时可见当前章节）
  useEffect(() => {
    if (player.currentEpisode && selectedBook) {
      document.title = `${player.currentEpisode.title} - ${selectedBook.title}`;
    } else {
      document.title = '有声书播放器';
    }
  }, [player.currentEpisode, selectedBook]);

  const handleRateChange = useCallback((newRate: number) => {
    player.setPlaybackRate(newRate);
    saveUserSettings({ playbackRate: newRate }).catch(() => {});
  }, [player]);

  const handleLoginSuccess = useCallback((u: any) => {
    setUser(u); navigate('library');
    // 登录后立即拉取偏好（倍速/主题/连播/快进快退），避免不同步
    getUserSettings().then(applyUserSettings).catch(() => {});
  }, [applyUserSettings, navigate]);

  const handleOpen = useCallback((book: Book, eps: Episode[]) => {
    setSelectedBook(book); setEpisodes(eps); navigate('book');
  }, [navigate]);

  const handleResume = useCallback((book: Book, eps: Episode[], idx: number, pos: number) => {
    setSelectedBook(book); setEpisodes(eps); setEpIndex(idx);
    // 模拟 library→book→player 的浏览路径：多压一层 book 历史，
    // 使播放页返回键（history.back）回到书籍目录而非书库
    history.pushState({ view: 'book' }, '');
    player.play(eps[idx], pos); navigate('player');
  }, [player, navigate]);

  const switchEpisode = useCallback(async (targetIdx: number) => {
    const targetEp = episodes[targetIdx];
    if (!targetEp) return;
    // 先保存当前集最后播放位置
    player.saveNow();
    let pos = 0;
    try {
      const p: any = await getProgress(targetEp.id);
      if (p && p.position) {
        pos = p.position;
      }
    } catch {
      // 获取失败则从0开始
    }
    handlePlay(targetEp, targetIdx, pos);
  }, [episodes, handlePlay, player]);

  const handlePrev = useCallback(() => {
    if (epIndex > 0) switchEpisode(epIndex - 1);
  }, [epIndex, switchEpisode]);

  const handleNext = useCallback(() => {
    if (epIndex < episodes.length - 1) switchEpisode(epIndex + 1);
  }, [epIndex, episodes.length, switchEpisode]);

  // 键盘快捷键：空格 播放/暂停；←/→ 快退/快进；↑/↓ 音量（输入框聚焦时忽略）
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!playerRef.current?.currentEpisode) return;
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
      const audio = playerRef.current.audioRef?.current as HTMLAudioElement | undefined;
      if (e.code === 'Space') { e.preventDefault(); playerRef.current.togglePlay(); }
      else if (e.code === 'ArrowLeft' && audio) { e.preventDefault(); audio.currentTime = Math.max(0, audio.currentTime - skipRef.current); }
      else if (e.code === 'ArrowRight' && audio) { e.preventDefault(); audio.currentTime = Math.min(audio.duration || 0, audio.currentTime + skipRef.current); }
      else if (e.code === 'ArrowUp' && audio) { e.preventDefault(); playerRef.current.setVolume(Math.min(1, audio.volume + 0.1)); }
      else if (e.code === 'ArrowDown' && audio) { e.preventDefault(); playerRef.current.setVolume(Math.max(0, audio.volume - 0.1)); }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  useMediaSession(
    player.currentEpisode, selectedBook?.title || '', player.playing,
    player.togglePlay, player.pause, handlePrev, handleNext,
    (t) => player.seek(t), player.currentTime, player.duration,
    selectedBook?.id, skipSec
  );

  // 鉴权校验中：显示启动画面而非登录页（避免已登录用户看到登录页闪烁）
  if (authing) return (
    <>
      <div className="min-h-screen bg-slate-50 dark:bg-[#0b0f17] flex items-center justify-center">
        <div className="flex flex-col items-center gap-4">
          <div className="w-12 h-12 rounded-2xl bg-gradient-to-br from-indigo-500 via-indigo-600 to-purple-600 flex items-center justify-center shadow-lg shadow-indigo-500/20 text-white animate-pulse">
            <HeadphonesIcon className="w-6 h-6" />
          </div>
          <span className="text-sm text-slate-500 dark:text-slate-400 font-medium">加载中...</span>
        </div>
      </div>
      <ToastHost />
    </>
  );

  if (!user) return (
    <>
      <Login onLogin={handleLoginSuccess} />
      <ToastHost />
    </>
  );

  const isAdmin = user?.role === 'admin';
  const navBtn = (active: boolean) =>
    `flex items-center gap-1.5 px-3.5 py-2 text-sm rounded-lg font-medium transition-all duration-200 ${
      active
        ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30'
        : 'text-slate-600 dark:text-slate-300 hover:text-slate-900 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-slate-800/60'
    }`;

  return (
    <div className="h-dvh bg-slate-50 dark:bg-[#0b0f17] text-slate-900 dark:text-slate-100 flex flex-col transition-colors duration-200">
      {/* 常驻 audio 元素：不随视图切换卸载，保证后台持续播放 */}
      <audio ref={player.audioRef} />

      <nav className="flex items-center justify-between px-4 sm:px-6 py-3 bg-white/80 dark:bg-[#0b0f17]/80 backdrop-blur-xl border-b border-slate-200/80 dark:border-slate-800/80 z-20">
        <div className="flex items-center gap-2 sm:gap-4">
          <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-indigo-500 via-indigo-600 to-purple-600 flex items-center justify-center shadow-md shadow-indigo-500/20 text-white">
            <HeadphonesIcon className="w-5 h-5" />
          </div>
          <span className="font-bold text-lg tracking-tight hidden sm:block bg-gradient-to-r from-indigo-600 to-purple-600 bg-clip-text text-transparent dark:from-indigo-400 dark:to-purple-400">
            有声书
          </span>
          <div className="flex items-center gap-1.5 ml-2">
            <button onClick={() => navigate('library')} className={navBtn(view === 'library')}>
              <BookOpenIcon className="w-4 h-4" /> 书库
            </button>
            {/* 管理入口仅管理员可见，避免普通用户误入看到 403 报错 */}
            {isAdmin && (
              <button onClick={() => navigate('admin')} className={navBtn(view === 'admin')}>
                <SettingsIcon className="w-4 h-4" /> 管理
              </button>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2 sm:gap-3 text-sm">
          <button
            onClick={() => setIsDark(d => !d)}
            title={isDark ? '切换到亮色模式' : '切换到暗色模式'}
            className="p-2 rounded-lg text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition flex items-center justify-center"
          >
            {isDark ? <SunIcon className="w-4 h-4 text-amber-400" /> : <MoonIcon className="w-4 h-4 text-slate-600" />}
          </button>
          <button
            onClick={() => setShowChangePw(true)}
            title="修改密码"
            className="p-2 rounded-lg text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition flex items-center justify-center"
          >
            <KeyIcon className="w-4 h-4" />
          </button>
          <span className="text-slate-500 dark:text-slate-400 hidden sm:block font-medium">{user.username}</span>
          <button onClick={handleLogout} className="flex items-center gap-1.5 text-slate-500 dark:text-slate-400 hover:text-red-500 dark:hover:text-red-400 transition px-2.5 py-1.5 rounded-lg hover:bg-red-50 dark:hover:bg-red-500/10">
            <LogOutIcon className="w-4 h-4" /> 退出
          </button>
        </div>
      </nav>

      {/* 内容区内部滚动：MiniPlayer 始终固定在视口底部，不随内容长度被推到文档底 */}
      <div className="flex-1 overflow-y-auto min-h-0">
        {view === 'library' && <Library onOpen={handleOpen} onResume={handleResume} />}
        {view === 'book' && selectedBook && <BookDetail book={selectedBook} episodes={episodes} onPlay={handlePlay} onBack={goBack} onBookUpdated={(b) => setSelectedBook(b)} onEpisodesUpdated={setEpisodes} canEdit={isAdmin} />}
        {view === 'player' && <PlayerView player={player} episodes={episodes} epIndex={epIndex} bookTitle={selectedBook?.title || ''} onPlay={handlePlay} onPrev={handlePrev} onNext={handleNext} onBack={goBack} coverBookId={selectedBook?.id || ''} onRateChange={handleRateChange} autoNext={autoNext} onAutoNextChange={handleAutoNextChange} skipSec={skipSec} />}
        {view === 'admin' && isAdmin && (
          <Suspense fallback={<div className="text-center py-20 text-slate-500 dark:text-slate-400 text-sm">加载管理页...</div>}>
            <Admin />
          </Suspense>
        )}
      </div>

      {/* 全局迷你播放条：非播放页且有正在播放的分集时显示 */}
      {view !== 'player' && player.currentEpisode && (
        <MiniPlayer
          player={player}
          bookTitle={selectedBook?.title || ''}
          coverBookId={selectedBook?.id || ''}
          onOpenPlayer={() => navigate('player')}
          onPrev={handlePrev}
          onNext={handleNext}
        />
      )}

      {showChangePw && <ChangePasswordDialog onClose={() => setShowChangePw(false)} onDone={handleLogout} />}

      {/* 全局 Toast 通知 */}
      <ToastHost />
    </div>
  );
}
