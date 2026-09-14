import { useState } from 'react';
import { coverUrl } from '../services/api';
import { PlayIcon, PauseIcon, PrevIcon, NextIcon, BookCoverFallback } from '../components/icons';

interface PlayerHook {
  currentEpisode: { id: string; title: string } | null;
  playing: boolean;
  currentTime: number;
  duration: number;
  netState?: 'ok' | 'retrying' | 'failed';
  togglePlay: () => void;
}

// MiniPlayer 全局迷你播放条：浏览书库/详情/管理页时固定在底部，点击进入完整播放页。
export default function MiniPlayer({ player, bookTitle, coverBookId, onOpenPlayer, onPrev, onNext }: {
  player: PlayerHook; bookTitle: string; coverBookId: string;
  onOpenPlayer: () => void; onPrev: () => void; onNext: () => void;
}) {
  const { currentEpisode, playing, currentTime, duration, netState, togglePlay } = player;
  const [coverErr, setCoverErr] = useState(false);
  if (!currentEpisode) return null;

  const fmt = (s: number) => { const m = Math.floor(s / 60); const sec = Math.floor(s % 60); return `${m}:${sec.toString().padStart(2, '0')}`; };
  const pct = duration > 0 ? (currentTime / duration) * 100 : 0;

  return (
    <div className="relative border-t border-slate-200/80 dark:border-slate-800/80 bg-white/95 dark:bg-[#0b0f17]/95 backdrop-blur-xl px-3 sm:px-5 py-2.5 flex items-center gap-3 z-20">
      {/* 点击主体进入播放页 */}
      <div onClick={onOpenPlayer} className="flex items-center gap-3 flex-1 min-w-0 cursor-pointer group">
        {coverErr ? (
          <div className="w-11 h-11 rounded-lg bg-gradient-to-br from-gray-800 to-indigo-900/50 flex items-center justify-center shrink-0">
            <BookCoverFallback className="w-5 h-5 text-gray-600" />
          </div>
        ) : (
          <img src={coverUrl(coverBookId)} alt={bookTitle} onError={() => setCoverErr(true)}
            className="w-11 h-11 rounded-lg object-cover shrink-0 bg-gray-800" />
        )}
        <div className="min-w-0 flex-1">
          <div className="text-sm font-semibold truncate text-slate-900 dark:text-slate-100 group-hover:text-indigo-600 dark:group-hover:text-indigo-400 transition-colors">{currentEpisode.title}</div>
          <div className="text-xs text-slate-500 dark:text-slate-400 truncate flex items-center gap-1.5">
            {netState === 'retrying' && <span className="text-amber-600 dark:text-amber-400 font-medium">重连中…</span>}
            {netState === 'failed' && <span className="text-red-600 dark:text-red-400 font-medium">已断开</span>}
            {netState !== 'ok' ? '·' : ''}{bookTitle}
          </div>
        </div>
        <div className="text-[11px] text-slate-500 dark:text-slate-400 tabular-nums hidden sm:block shrink-0">{fmt(currentTime)} / {fmt(duration)}</div>
      </div>

      <div className="flex items-center gap-1 shrink-0">
        <button onClick={onPrev} title="上一集"
          className="w-9 h-9 flex items-center justify-center rounded-full text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition">
          <PrevIcon className="w-4 h-4" />
        </button>
        <button onClick={togglePlay} title={playing ? '暂停' : '播放'}
          className="w-10 h-10 flex items-center justify-center rounded-full bg-indigo-600 hover:bg-indigo-500 text-white shadow-md shadow-indigo-600/30 transition">
          {playing ? <PauseIcon className="w-5 h-5" /> : <PlayIcon className="w-5 h-5 ml-0.5" />}
        </button>
        <button onClick={onNext} title="下一集"
          className="w-9 h-9 flex items-center justify-center rounded-full text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition">
          <NextIcon className="w-4 h-4" />
        </button>
      </div>

      {/* 底部细进度条 */}
      <div className="absolute left-0 right-0 bottom-0 h-0.5 bg-slate-100 dark:bg-slate-800/60 pointer-events-none">
        <div className="h-full bg-gradient-to-r from-indigo-500 to-purple-600 transition-all duration-300" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}
