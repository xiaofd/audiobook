import { useEffect, useRef } from 'react';
import { Episode, coverUrl } from '../services/api';

export function useMediaSession(
  episode: Episode | null,
  bookTitle: string,
  playing: boolean,
  onPlay: () => void,
  onPause: () => void,
  onPrev: () => void,
  onNext: () => void,
  onSeek: (t: number) => void,
  position: number,
  duration: number,
  bookId?: string,
  skipSec = 15
) {
  // 用 ref 读取最新的回调与位置，避免主 effect 依赖 position/duration，
  // 否则 timeupdate 每 ~250ms 触发一次 MediaMetadata/handler 全量重建。
  const handlersRef = useRef({ onPlay, onPause, onPrev, onNext, onSeek });
  handlersRef.current = { onPlay, onPause, onPrev, onNext, onSeek };
  const posRef = useRef(position);
  posRef.current = position;
  const durRef = useRef(duration);
  durRef.current = duration;
  // 快进快退秒数 latest-ref：偏好变化后锁屏控制立即生效，无需重建 handler
  const skipRef = useRef(skipSec);
  skipRef.current = skipSec;

  // 元数据与动作处理器：仅在切换分集/书籍时重建
  useEffect(() => {
    if (!('mediaSession' in navigator)) return;
    if (!episode) {
      navigator.mediaSession.metadata = null;
      return;
    }

    const artwork = bookId
      ? [
          { src: coverUrl(bookId), sizes: '96x96', type: 'image/jpeg' },
          { src: coverUrl(bookId), sizes: '256x256', type: 'image/jpeg' },
          { src: coverUrl(bookId), sizes: '512x512', type: 'image/jpeg' },
        ]
      : [];

    navigator.mediaSession.metadata = new MediaMetadata({
      title: episode.title,
      artist: bookTitle,
      album: bookTitle,
      artwork,
    });

    navigator.mediaSession.setActionHandler('play', () => handlersRef.current.onPlay());
    navigator.mediaSession.setActionHandler('pause', () => handlersRef.current.onPause());
    navigator.mediaSession.setActionHandler('previoustrack', () => handlersRef.current.onPrev());
    navigator.mediaSession.setActionHandler('nexttrack', () => handlersRef.current.onNext());
    navigator.mediaSession.setActionHandler('seekto', (d) => {
      if (d.seekTime !== undefined) handlersRef.current.onSeek(d.seekTime);
    });

    // 支持快进快退动作（针对手机锁屏/车载系统）
    const onSeekBy = (t: number) => handlersRef.current.onSeek(t);
    try {
      navigator.mediaSession.setActionHandler('seekforward', (d) => {
        onSeekBy(Math.min(durRef.current || 0, (posRef.current || 0) + (d.seekOffset || skipRef.current)));
      });
      navigator.mediaSession.setActionHandler('seekbackward', (d) => {
        onSeekBy(Math.max(0, (posRef.current || 0) - (d.seekOffset || skipRef.current)));
      });
    } catch {}

    return () => {
      navigator.mediaSession.setActionHandler('play', null);
      navigator.mediaSession.setActionHandler('pause', null);
      navigator.mediaSession.setActionHandler('previoustrack', null);
      navigator.mediaSession.setActionHandler('nexttrack', null);
      navigator.mediaSession.setActionHandler('seekto', null);
      try {
        navigator.mediaSession.setActionHandler('seekforward', null);
        navigator.mediaSession.setActionHandler('seekbackward', null);
      } catch {}
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [episode, bookTitle, bookId]);

  // 播放位置状态：随 position/duration 更新
  useEffect(() => {
    if (!episode) return;
    if ('setPositionState' in navigator.mediaSession) {
      try {
        navigator.mediaSession.setPositionState({
          duration: Math.max(0, duration || 0),
          playbackRate: 1,
          position: Math.max(0, Math.min(position || 0, duration || 0)),
        } as MediaPositionState);
      } catch {}
    }
  }, [position, duration, episode]);

  // 播放/暂停状态
  useEffect(() => {
    if (!episode) return;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (navigator.mediaSession as any).playbackState = playing ? 'playing' : 'paused';
  }, [playing, episode]);
}
