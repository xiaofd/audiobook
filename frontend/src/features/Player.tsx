import { useState, useEffect, useRef, useCallback } from 'react';
import { Episode, coverUrl, getProgress } from '../services/api';
import { PlayIcon, PauseIcon, PrevIcon, NextIcon, BookCoverFallback, TimerIcon, RewindIcon, ForwardIcon, VolumeIcon, VolumeXIcon, QueueIcon, XIcon, BarsIcon } from '../components/icons';

const QUEUE_PAGE = 100; // 队列抽屉分页（长书）

interface PlayerHook {
  audioRef: React.RefObject<HTMLAudioElement | null>;
  currentEpisode: Episode | null;
  playing: boolean;
  currentTime: number;
  duration: number;
  rate: number;
  volume: number;
  muted: boolean;
  sleepEndsAt: number | null;
  sleepAfterEpisode: boolean;
  togglePlay: () => void;
  pause: () => void;
  seek: (t: number) => void;
  setPlaybackRate: (r: number) => void;
  setVolume: (v: number) => void;
  toggleMute: () => void;
  setSleepTimer: (minutes: number | null) => void;
  setSleepAfterEpisodeMode: (on: boolean) => void;
  netState: 'ok' | 'retrying' | 'failed';
  retryCount: number;
  retryNow: () => void;
}

export default function PlayerView({ player, episodes, epIndex, bookTitle, onPlay, onPrev, onNext, onBack, coverBookId, onRateChange, autoNext, onAutoNextChange, skipSec }: {
  player: PlayerHook; episodes: Episode[]; epIndex: number; bookTitle: string;
  onPlay: (ep: Episode, idx: number, pos: number) => void; onPrev: () => void; onNext: () => void; onBack: () => void; coverBookId: string;
  onRateChange?: (r: number) => void;
  autoNext?: boolean; onAutoNextChange?: (v: boolean) => void;
  skipSec?: number;
}) {
  const { audioRef, currentEpisode, playing, currentTime, duration, rate, volume, muted, sleepEndsAt, sleepAfterEpisode, netState, retryCount, retryNow, togglePlay, seek, setPlaybackRate, setVolume, toggleMute, setSleepTimer, setSleepAfterEpisodeMode } = player;
  const skip = skipSec || 15;

  // 睡眠定时器倒计时显示
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!sleepEndsAt) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [sleepEndsAt]);
  const sleepLeftSec = sleepEndsAt ? Math.max(0, Math.ceil((sleepEndsAt - now) / 1000)) : 0;

  const handleSetRate = (r: number) => {
    setPlaybackRate(r);
    if (onRateChange) onRateChange(r);
  };

  const skipBackward = () => seek(Math.max(0, currentTime - skip));
  const skipForward = () => seek(Math.min(duration || 0, currentTime + skip));

  const fmt = (s: number) => {
    if (!isFinite(s) || s < 0) s = 0;
    const m = Math.floor(s / 60);
    const sec = Math.floor(s % 60);
    if (m >= 60) {
      const h = Math.floor(m / 60);
      return `${h}:${(m % 60).toString().padStart(2, '0')}:${sec.toString().padStart(2, '0')}`;
    }
    return `${m}:${sec.toString().padStart(2, '0')}`;
  };

  // --- 章节队列抽屉 ---
  const [showQueue, setShowQueue] = useState(false);
  const [queueCount, setQueueCount] = useState(QUEUE_PAGE);
  const [queueProgress, setQueueProgress] = useState<Record<string, { position: number; duration: number; isFinished: boolean }>>({});
  const currentEpBtnRef = useRef<HTMLButtonElement>(null);

  // 打开抽屉时：初始分页直接覆盖当前集所在页（否则第 500 集打开只看到第 1 页），
  // 并拉取本书进度（用于显示每集进度/已听完）
  useEffect(() => {
    if (!showQueue || !currentEpisode) return;
    setQueueCount(Math.max(QUEUE_PAGE, (Math.floor(epIndex / QUEUE_PAGE) + 1) * QUEUE_PAGE));
    getProgress(undefined, currentEpisode.bookId).then(data => {
      const m: Record<string, { position: number; duration: number; isFinished: boolean }> = {};
      if (Array.isArray(data)) for (const p of data as any[]) m[p.episodeId] = { position: p.position || 0, duration: p.duration || 0, isFinished: !!p.isFinished };
      setQueueProgress(m);
    }).catch(() => {});
  }, [showQueue, currentEpisode?.bookId, epIndex]);

  // 抽屉渲染后把当前集滚动到可见区域中央
  useEffect(() => {
    if (showQueue && currentEpBtnRef.current) {
      currentEpBtnRef.current.scrollIntoView({ block: 'center' });
    }
  }, [showQueue, queueCount]);

  const playFromQueue = useCallback((ep: Episode, idx: number) => {
    onPlay(ep, idx, queueProgress[ep.id]?.position || 0);
    setShowQueue(false);
  }, [onPlay, queueProgress]);

  return (
    // 外层容器与首页/目录页同宽（max-w-6xl）：返回栏、标题与其他页面左右对齐；
    // 播放控件在容器内居中收窄（max-w-xl），宽屏不空旷、手机不溢出
    <div className="relative mx-auto w-full max-w-6xl px-4 sm:px-6 lg:px-8 min-h-full flex flex-col">
      {/* 氛围层：封面模糊放大作为全屏背景（fixed 不受内容列宽度限制，消除"细长框"割裂感） */}
      <div aria-hidden className="fixed inset-0 overflow-hidden pointer-events-none">
        <img src={coverUrl(coverBookId)} alt="" className="w-full h-full object-cover blur-3xl scale-125 opacity-15 dark:opacity-25" />
      </div>

      {/* 控件列：居中窄列（播放控件适合窄排布），背景由全屏氛围层承接 */}
      <div className="relative w-full max-w-xl mx-auto flex-1 flex flex-col items-center justify-center py-6 sm:py-8 text-slate-900 dark:text-slate-100">
        <div className="w-full flex items-center justify-between mb-6">
          <button onClick={onBack} className="text-sm font-medium text-indigo-600 dark:text-indigo-400 hover:text-indigo-500 transition flex items-center gap-1.5">
            ← 返回目录
          </button>
          <button onClick={() => setShowQueue(true)} title="章节列表"
            className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800/60 rounded-lg transition">
            <QueueIcon className="w-4 h-4" /> 章节
          </button>
        </div>

        <PlayerCover bookId={coverBookId} title={bookTitle} playing={playing} />

        <div className="text-center mt-8 w-full">
          <div className="text-xl font-bold truncate text-slate-900 dark:text-slate-100">{currentEpisode?.title || '未选择'}</div>
          <div className="text-sm font-medium text-slate-500 dark:text-slate-400 mt-1.5 truncate">{bookTitle}</div>
        </div>

        {/* 网络断流状态横幅：自动重连中（琥珀）/ 重连失败（红，可手动重试） */}
        {netState === 'retrying' && (
          <div className="mt-4 px-4 py-2 rounded-full bg-amber-500/15 text-amber-600 dark:text-amber-400 text-xs font-medium flex items-center gap-2">
            <span className="w-1.5 h-1.5 rounded-full bg-current animate-pulse" />
            网络中断，自动重连中…（第 {retryCount} 次）
          </div>
        )}
        {netState === 'failed' && (
          <div className="mt-4 px-4 py-2 rounded-full bg-red-500/15 text-red-600 dark:text-red-400 text-xs font-medium flex items-center gap-2">
            <span className="w-1.5 h-1.5 rounded-full bg-current" />
            连接已断开
            <button onClick={retryNow} className="underline underline-offset-2 hover:opacity-80">点击重试</button>
          </div>
        )}

        {/* 自绘进度条：缓冲进度 + 播放进度 + 悬停时间预览 + 拖拽 */}
        <SeekBar audioRef={audioRef} currentTime={currentTime} duration={duration} onSeek={seek} fmt={fmt} />

        <div className="flex items-center gap-4 sm:gap-6 mt-6">
          <button onClick={onPrev} disabled={epIndex === 0} title="上一集"
            className="w-11 h-11 flex items-center justify-center rounded-full text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800 disabled:opacity-30 transition">
            <PrevIcon className="w-5 h-5" />
          </button>
          <button onClick={skipBackward} title={`快退 ${skip} 秒`}
            className="w-11 h-11 flex items-center justify-center rounded-full text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800 transition relative">
            <RewindIcon className="w-6 h-6" />
            <span className="absolute -bottom-0.5 text-[9px] font-bold">{skip}</span>
          </button>
          <button onClick={togglePlay} className="w-16 h-16 flex items-center justify-center rounded-full bg-indigo-600 hover:bg-indigo-500 text-white shadow-xl shadow-indigo-600/30 hover:scale-105 transition-all">
            {playing ? <PauseIcon className="w-7 h-7" /> : <PlayIcon className="w-7 h-7 ml-1" />}
          </button>
          <button onClick={skipForward} title={`快进 ${skip} 秒`}
            className="w-11 h-11 flex items-center justify-center rounded-full text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800 transition relative">
            <ForwardIcon className="w-6 h-6" />
            <span className="absolute -bottom-0.5 text-[9px] font-bold">{skip}</span>
          </button>
          <button onClick={onNext} disabled={epIndex >= episodes.length - 1} title="下一集"
            className="w-11 h-11 flex items-center justify-center rounded-full text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800 disabled:opacity-30 transition">
            <NextIcon className="w-5 h-5" />
          </button>
        </div>

        {/* 音量控制 */}
        <div className="flex items-center gap-2.5 mt-6 w-full max-w-[240px]">
          <button onClick={toggleMute} title={muted ? '取消静音' : '静音'}
            className="text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200 transition shrink-0">
            {muted || volume === 0 ? <VolumeXIcon className="w-5 h-5" /> : <VolumeIcon className="w-5 h-5" />}
          </button>
          <input type="range" min={0} max={1} step={0.05} value={muted ? 0 : volume}
            onChange={e => setVolume(+e.target.value)} title={`音量 ${Math.round((muted ? 0 : volume) * 100)}%`}
            className="w-full h-1.5 bg-slate-200 dark:bg-slate-800 accent-indigo-600 cursor-pointer rounded-full transition" />
          <span className="text-[11px] text-slate-500 dark:text-slate-400 tabular-nums w-8 text-right shrink-0">{Math.round((muted ? 0 : volume) * 100)}%</span>
        </div>

        {/* 键盘快捷键提示（仅桌面端显示） */}
        {currentEpisode && (
          <div className="hidden sm:flex items-center gap-3 mt-4 text-[11px] text-slate-400 dark:text-slate-500 font-medium">
            <span><kbd className="px-1.5 py-0.5 rounded bg-slate-200/70 dark:bg-slate-800/70 font-sans">空格</kbd> 播放/暂停</span>
            <span><kbd className="px-1.5 py-0.5 rounded bg-slate-200/70 dark:bg-slate-800/70 font-sans">← →</kbd> 快退/快进</span>
            <span><kbd className="px-1.5 py-0.5 rounded bg-slate-200/70 dark:bg-slate-800/70 font-sans">↑ ↓</kbd> 音量</span>
          </div>
        )}

        <div className="flex flex-wrap justify-center gap-2 mt-6 max-w-xs">
          {[0.5, 0.75, 0.9, 1.0, 1.1, 1.25, 1.4, 1.5, 1.6, 1.75, 2.0, 2.5, 3.0].map(r => (
            <button key={r} onClick={() => handleSetRate(r)}
              className={`px-3 py-1 text-xs font-semibold rounded-full transition ${rate === r ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'bg-slate-200/60 dark:bg-slate-800 text-slate-600 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-slate-700'}`}>{r}x</button>
          ))}
        </div>

        {/* 连播开关 + 睡眠定时器 */}
        <div className="flex flex-wrap items-center justify-center gap-2 mt-6">
          <button onClick={() => onAutoNextChange?.(!autoNext)} title="播完当前集后自动播放下一集"
            className={`px-3 py-1 text-xs font-semibold rounded-full transition ${autoNext ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'bg-slate-200/60 dark:bg-slate-800 text-slate-600 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-slate-700'}`}>
            连播{autoNext ? '开' : '关'}
          </button>
          <span className="text-xs font-medium text-slate-500 dark:text-slate-400 flex items-center gap-1">
            <TimerIcon className="w-3.5 h-3.5" /> 睡眠定时
          </span>
          {[15, 30, 60, 90].map(m => (
            <button key={m} onClick={() => setSleepTimer(m)}
              className={`px-2.5 py-1 text-xs font-medium rounded-full transition ${sleepEndsAt ? 'bg-slate-200/60 dark:bg-slate-800 text-slate-600 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-slate-700' : 'bg-indigo-50 dark:bg-indigo-950/60 text-indigo-600 dark:text-indigo-400 hover:bg-indigo-100 dark:hover:bg-indigo-900/60'}`}>
              {m}分钟
            </button>
          ))}
          <button onClick={() => setSleepAfterEpisodeMode(!sleepAfterEpisode)} title="当前分集播放完毕后停止，不连播下一集"
            className={`px-2.5 py-1 text-xs font-medium rounded-full transition ${sleepAfterEpisode ? 'bg-indigo-600 text-white shadow-md shadow-indigo-600/30' : 'bg-indigo-50 dark:bg-indigo-950/60 text-indigo-600 dark:text-indigo-400 hover:bg-indigo-100 dark:hover:bg-indigo-900/60'}`}>
            本集播完
          </button>
          {(sleepEndsAt || sleepAfterEpisode) && (
            <>
              {sleepEndsAt && (
                <span className="text-xs font-semibold text-indigo-600 dark:text-indigo-400 tabular-nums">{Math.floor(sleepLeftSec / 60)}:{(sleepLeftSec % 60).toString().padStart(2, '0')} 后暂停</span>
              )}
              {sleepAfterEpisode && <span className="text-xs font-semibold text-indigo-600 dark:text-indigo-400">本集播完即停</span>}
              <button onClick={() => { setSleepTimer(null); setSleepAfterEpisodeMode(false); }}
                className="px-2.5 py-1 text-xs font-medium rounded-full bg-red-500/10 dark:bg-red-500/20 text-red-600 dark:text-red-400 hover:bg-red-500/20 transition">取消</button>
            </>
          )}
        </div>
      </div>

      {/* 章节队列抽屉：切集无需返回详情页。
          定位用 flex 居中而非 translate：入场动画的 transform 会覆盖 Tailwind 的
          translate 工具类，导致动画期间面板偏在右下、结束后才跳回中央 */}
      {showQueue && (
        <div className="fixed inset-0 z-50" onClick={() => setShowQueue(false)}>
          <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" />
          <div className="absolute inset-x-0 bottom-0 sm:inset-0 sm:flex sm:items-center sm:justify-center">
            <div className="max-h-[70vh] w-full sm:max-w-md bg-white dark:bg-slate-900 rounded-t-3xl sm:rounded-3xl shadow-2xl flex flex-col animate-[toast-in_.25s_ease-out]"
              onClick={e => e.stopPropagation()}>
            <div className="flex items-center justify-between px-5 py-4 border-b border-slate-200 dark:border-slate-800 shrink-0">
              <h3 className="text-sm font-bold text-slate-900 dark:text-slate-100">章节列表 · 共 {episodes.length} 集</h3>
              <button onClick={() => setShowQueue(false)} className="p-1.5 rounded-lg text-slate-500 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800 transition">
                <XIcon className="w-4 h-4" />
              </button>
            </div>
            <div className="overflow-y-auto flex-1 p-3 space-y-1.5">
              {episodes.slice(0, queueCount).map((ep, idx) => {
                const prog = queueProgress[ep.id];
                const isCurrent = idx === epIndex;
                return (
                  <button key={ep.id} onClick={() => playFromQueue(ep, idx)}
                    ref={isCurrent ? currentEpBtnRef : undefined}
                    className={`w-full flex items-center gap-3 px-3.5 py-2.5 rounded-xl text-left transition ${isCurrent
                      ? 'bg-indigo-50 dark:bg-indigo-950/40 border border-indigo-300 dark:border-indigo-700'
                      : 'hover:bg-slate-100 dark:hover:bg-slate-800/70 border border-transparent'}`}>
                    <span className={`w-8 h-8 rounded-lg flex items-center justify-center shrink-0 text-xs font-bold tabular-nums ${isCurrent
                      ? 'bg-indigo-600 text-white'
                      : 'bg-slate-100 dark:bg-slate-800 text-slate-500 dark:text-slate-400'}`}>
                      {isCurrent && playing ? <BarsIcon className="w-4 h-4" /> : idx + 1}
                    </span>
                    <span className="flex-1 min-w-0">
                      <span className={`block text-sm truncate ${isCurrent ? 'font-bold text-indigo-600 dark:text-indigo-400' : 'font-medium text-slate-900 dark:text-slate-100'}`}>{ep.title}</span>
                      {prog && prog.position > 0 && (
                        <span className="block text-[11px] text-slate-500 dark:text-slate-400 mt-0.5">
                          {prog.isFinished ? '已听完' : `已听 ${fmt(prog.position)}${prog.duration > 0 ? ` / ${fmt(prog.duration)}` : ''}`}
                        </span>
                      )}
                    </span>
                    {prog?.isFinished && !isCurrent && (
                      <span className="text-[10px] font-medium text-emerald-600 dark:text-emerald-400 shrink-0">✓</span>
                    )}
                  </button>
                );
              })}
              {queueCount < episodes.length && (
                <button onClick={() => setQueueCount(c => c + QUEUE_PAGE)}
                  className="w-full py-2.5 text-sm font-medium text-indigo-600 dark:text-indigo-400 hover:bg-indigo-50 dark:hover:bg-indigo-950/30 rounded-xl transition">
                  加载更多（已显示 {queueCount}/{episodes.length} 集）
                </button>
              )}
            </div>
          </div>
        </div>
        </div>
      )}
    </div>
  );
}

// SeekBar 自绘进度条：缓冲区（浅色）+ 已播放（渐变）+ 悬停时间气泡 + 拖拽把手。
// 替代原生 <input type=range>（各浏览器样式不一、无悬停预览、把手过小难以拖拽）。
function SeekBar({ audioRef, currentTime, duration, onSeek, fmt }: {
  audioRef: React.RefObject<HTMLAudioElement | null>;
  currentTime: number; duration: number;
  onSeek: (t: number) => void; fmt: (s: number) => string;
}) {
  const barRef = useRef<HTMLDivElement>(null);
  const [hoverX, setHoverX] = useState<number | null>(null); // 悬停位置比例 0-1
  const [dragging, setDragging] = useState(false);
  const [buffered, setBuffered] = useState(0); // 缓冲比例 0-1

  // 从 audio 元素读取缓冲区
  useEffect(() => {
    const audio = audioRef.current;
    if (!audio) return;
    const onProgress = () => {
      if (audio.buffered.length > 0 && audio.duration > 0) {
        setBuffered(audio.buffered.end(audio.buffered.length - 1) / audio.duration);
      }
    };
    audio.addEventListener('progress', onProgress);
    audio.addEventListener('loadedmetadata', onProgress);
    return () => {
      audio.removeEventListener('progress', onProgress);
      audio.removeEventListener('loadedmetadata', onProgress);
    };
  }, [audioRef, currentTime]);

  const ratioAt = useCallback((clientX: number) => {
    const el = barRef.current;
    if (!el || duration <= 0) return 0;
    const rect = el.getBoundingClientRect();
    return Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
  }, [duration]);

  const handlePointerDown = (e: React.PointerEvent) => {
    if (duration <= 0) return;
    setDragging(true);
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    onSeek(ratioAt(e.clientX) * duration);
  };
  const handlePointerMove = (e: React.PointerEvent) => {
    setHoverX(ratioAt(e.clientX));
    if (dragging) onSeek(ratioAt(e.clientX) * duration);
  };
  const handlePointerUp = () => setDragging(false);

  const pct = duration > 0 ? (currentTime / duration) * 100 : 0;
  const hoverPct = hoverX != null ? hoverX * 100 : null;

  return (
    <div className="w-full mt-8 select-none">
      <div
        ref={barRef}
        className="group relative h-6 flex items-center cursor-pointer touch-none"
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onPointerLeave={() => setHoverX(null)}
      >
        {/* 轨道 */}
        <div className="absolute inset-x-0 h-1.5 bg-slate-200 dark:bg-slate-800 rounded-full overflow-hidden">
          <div className="h-full bg-slate-300 dark:bg-slate-700 rounded-full" style={{ width: `${buffered * 100}%` }} />
        </div>
        {/* 已播放 */}
        <div className="absolute h-1.5 bg-gradient-to-r from-indigo-500 to-purple-600 rounded-full pointer-events-none" style={{ width: `${pct}%` }} />
        {/* 拖拽把手 */}
        <div className={`absolute w-3.5 h-3.5 rounded-full bg-white dark:bg-white shadow-md ring-2 ring-indigo-500 pointer-events-none transition-transform ${dragging ? 'scale-125' : 'scale-0 group-hover:scale-100'}`}
          style={{ left: `calc(${pct}% - 7px)` }} />
        {/* 悬停时间气泡 */}
        {hoverPct != null && !dragging && (
          <div className="absolute -top-7 px-1.5 py-0.5 rounded-md bg-slate-900 dark:bg-slate-700 text-white text-[11px] font-medium tabular-nums pointer-events-none shadow-lg"
            style={{ left: `calc(${hoverPct}% - 18px)` }}>
            {fmt((hoverX || 0) * duration)}
          </div>
        )}
      </div>
      <div className="flex justify-between w-full text-xs text-slate-500 dark:text-slate-400 mt-1.5 font-medium tabular-nums">
        <span>{fmt(currentTime)}</span>
        <span>{fmt(duration)}</span>
      </div>
    </div>
  );
}

function PlayerCover({ bookId, title, playing }: { bookId: string; title: string; playing: boolean }) {
  const [err, setErr] = useState(false);
  if (err) return (
    <div className={`w-44 h-44 sm:w-56 sm:h-56 md:w-64 md:h-64 rounded-3xl bg-gradient-to-br from-gray-800 via-gray-800 to-indigo-900/50 flex items-center justify-center shadow-2xl select-none transition-shadow duration-700 ${playing ? 'shadow-indigo-500/30' : ''}`}>
      <BookCoverFallback className="w-20 h-20 text-gray-600" />
    </div>
  );
  return (
    <div className={`relative w-44 h-44 sm:w-56 sm:h-56 md:w-64 md:h-64 rounded-3xl overflow-hidden shadow-2xl transition-all duration-700 ${playing ? 'shadow-indigo-500/40 scale-[1.02] ring-1 ring-indigo-400/30' : 'shadow-black/20'}`}>
      <img src={coverUrl(bookId)} alt={title} onError={() => setErr(true)}
        className="w-full h-full object-cover" />
      {/* 播放中：底部柔和光带呼吸动效 */}
      <div className={`absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-indigo-600/40 to-transparent transition-opacity duration-700 ${playing ? 'opacity-100 animate-pulse' : 'opacity-0'}`} />
    </div>
  );
}
