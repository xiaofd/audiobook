import { useState, useRef, useEffect, useCallback } from 'react';
import { Episode, saveProgress, streamUrl } from '../services/api';

export function usePlayer(onEndedCallback?: () => void) {
  // audio 元素由 App 层常驻渲染（不随视图切换卸载），此处仅持有引用，
  // 保证浏览书库/管理页时音频持续播放、返回播放页不假死。
  const audioRef = useRef<HTMLAudioElement>(null);
  const [currentEpisode, setCurrentEpisode] = useState<Episode | null>(null);
  const [playing, setPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(0);
  const [rate, setRate] = useState(1);
  const startPosRef = useRef(0);
  const onEndedRef = useRef(onEndedCallback);
  onEndedRef.current = onEndedCallback;
  // 每次 play 调用递增，强制 effect 重新设置音频 src（即使 currentEpisode 引用相同）
  const [playId, setPlayId] = useState(0);
  // currentEpisode 的 ref 镜像：供事件监听等闭包读取最新值而不重建监听
  const currentEpisodeRef = useRef<Episode | null>(null);
  currentEpisodeRef.current = currentEpisode;

  // --- 网络断流自愈（移动端 WiFi/4G 切换、电梯断网等场景）---
  // audio 进入 error 态后不会自行恢复：记录断流前进度，指数退避重连续播。
  const MAX_RETRIES = 5;
  const [netState, setNetState] = useState<'ok' | 'retrying' | 'failed'>('ok');
  const [retryCount, setRetryCount] = useState(0);
  const lastGoodTimeRef = useRef(0); // 断流前最后已知进度（timeupdate 持续刷新）
  const retryCountRef = useRef(0);
  const retryTimerRef = useRef<number | null>(null);

  const clearRetryTimer = useCallback(() => {
    if (retryTimerRef.current !== null) {
      clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
  }, []);

  // 重连核心：重设 src，加载后跳回断流前进度并续播
  const reloadAtLastPos = useCallback(() => {
    const audio = audioRef.current;
    const ep = currentEpisodeRef.current;
    if (!audio || !ep) return;
    const t = lastGoodTimeRef.current;
    audio.src = streamUrl(ep.id);
    audio.load();
    audio.playbackRate = rateRef.current;
    const onLoaded = () => {
      if (t > 0) audio.currentTime = t;
      audio.play().catch(() => {});
      audio.removeEventListener('loadeddata', onLoaded);
    };
    audio.addEventListener('loadeddata', onLoaded);
    // 兜底：loadeddata 未及时触发时 3s 后清理监听
    setTimeout(() => audio.removeEventListener('loadeddata', onLoaded), 3000);
  }, []);

  // 指数退避重试（1s/2s/4s/8s/16s），超限进入 failed 态等用户手动重试
  const attemptRecovery = useCallback(() => {
    retryCountRef.current += 1;
    setRetryCount(retryCountRef.current);
    if (retryCountRef.current > MAX_RETRIES) {
      setNetState('failed');
      return;
    }
    setNetState('retrying');
    clearRetryTimer();
    const wait = Math.min(1000 * Math.pow(2, retryCountRef.current - 1), 16000);
    retryTimerRef.current = window.setTimeout(reloadAtLastPos, wait);
  }, [clearRetryTimer, reloadAtLastPos]);

  // 用户手动重试（failed 横幅按钮）：清零计数重新开始
  const retryNow = useCallback(() => {
    retryCountRef.current = 0;
    setRetryCount(0);
    setNetState('retrying');
    clearRetryTimer();
    reloadAtLastPos();
  }, [clearRetryTimer, reloadAtLastPos]);

  // --- 外部打断自动恢复（来电、系统提示音等瞬时打断）---
  // 被打断时 audio 触发 pause 但并非用户操作；打断结束无事件通知，只能轮询续播。
  // Web 无法区分"瞬时打断"与"用户主动转向其他音频"，用可见性/焦点做代理信号，
  // 宁可少恢复也不与其他应用叠加出声：
  //  - 仅当打断发生时页面可见且窗口有焦点，才进入恢复轮询；
  //  - 轮询期间页面切后台或失去焦点 → 立即放弃（用户已在用别的应用）；
  //  - 恢复成功后 10s 内再次被打断 → 指数退避（对方仍占用音频焦点），5 级后放弃。
  const userPausedRef = useRef(false);   // 用户主动暂停标记
  const resumeTimerRef = useRef<number | null>(null);
  const resumeBackoffRef = useRef(0);    // 恢复退避等级
  const lastResumeAtRef = useRef(0);     // 上次恢复成功时间

  const stopResumeRetry = useCallback(() => {
    if (resumeTimerRef.current !== null) {
      clearTimeout(resumeTimerRef.current);
      resumeTimerRef.current = null;
    }
  }, []);

  // 尝试恢复一次；返回 false 表示应放弃（用户已离开本应用/已主动暂停）
  const tryResume = useCallback((): boolean => {
    const audio = audioRef.current;
    if (!audio || userPausedRef.current) return false;
    if (document.hidden || !document.hasFocus()) return false;
    audio.play().then(() => { lastResumeAtRef.current = Date.now(); }).catch(() => {});
    return true; // 无论成败继续下一轮（成功后 onPlay 会 stopResumeRetry）
  }, []);

  const scheduleResumeRetry = useCallback(() => {
    if (resumeTimerRef.current !== null) return; // 已在等待
    const wait = 3000 * Math.pow(2, resumeBackoffRef.current); // 3s/6s/12s/24s/48s
    resumeTimerRef.current = window.setTimeout(() => {
      resumeTimerRef.current = null;
      if (tryResume()) scheduleResumeRetry();
    }, wait);
  }, [tryResume]);

  // 打断发生（onPause 检测到非用户暂停）时调用
  const onInterrupted = useCallback(() => {
    // 页面在后台/无焦点时被打断：用户已主动切到其他应用，不抢音频焦点
    if (document.hidden || !document.hasFocus()) return;
    // 刚恢复成功 10s 内又被打断：对方仍在占用焦点，退避加级
    if (lastResumeAtRef.current && Date.now() - lastResumeAtRef.current < 10000) {
      resumeBackoffRef.current += 1;
      if (resumeBackoffRef.current >= 5) return; // 放弃，等用户手动播放
    } else {
      resumeBackoffRef.current = 0;
    }
    scheduleResumeRetry();
  }, [scheduleResumeRetry]);

  const play = useCallback((ep: Episode, startPos = 0) => {
    startPosRef.current = startPos;
    userPausedRef.current = false; // 新播放请求：解除暂停标记并停止恢复轮询
    stopResumeRetry();
    setCurrentEpisode(ep);
    setPlaying(true);
    setPlayId(id => id + 1);
  }, [stopResumeRetry]);

  // 当 currentEpisode 变化或 playId 变化时，设置 src 并播放。
  useEffect(() => {
    const audio = audioRef.current;
    if (!audio || !currentEpisode) return;
    // 新播放请求：取消挂起的重连并复位网络状态
    clearRetryTimer();
    retryCountRef.current = 0;
    setRetryCount(0);
    setNetState('ok');
    audio.src = streamUrl(currentEpisode.id);
    audio.load();
    audio.playbackRate = rate;
    const startPos = startPosRef.current;
    const onLoaded = () => {
      if (startPos > 0) audio.currentTime = startPos;
      audio.play().catch(() => {});
      audio.removeEventListener('loadeddata', onLoaded);
    };
    audio.addEventListener('loadeddata', onLoaded);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentEpisode, playId]);

  const togglePlay = useCallback(() => {
    const audio = audioRef.current;
    if (!audio) return;
    if (audio.paused) { userPausedRef.current = false; audio.play().catch(() => {}); setPlaying(true); }
    else { userPausedRef.current = true; audio.pause(); setPlaying(false); }
  }, []);

  const pause = useCallback(() => {
    const audio = audioRef.current;
    if (!audio) return;
    userPausedRef.current = true;
    audio.pause();
    setPlaying(false);
  }, []);

  const seek = useCallback((t: number) => {
    const audio = audioRef.current;
    if (audio) audio.currentTime = t;
  }, []);

  const setPlaybackRate = useCallback((r: number) => {
    setRate(r);
    if (audioRef.current) audioRef.current.playbackRate = r;
  }, []);
  // rate 的 ref 镜像：重连时恢复倍速用
  const rateRef = useRef(1);
  rateRef.current = rate;

  // 音量控制（0-1），静音记忆：取消静音时恢复之前的音量
  const [volume, setVolumeState] = useState(0.9);
  const [muted, setMuted] = useState(false);
  const lastVolumeRef = useRef(0.9);

  const setVolume = useCallback((v: number) => {
    const nv = Math.max(0, Math.min(1, v));
    setVolumeState(nv);
    setMuted(nv === 0);
    if (nv > 0) lastVolumeRef.current = nv;
    if (audioRef.current) audioRef.current.volume = nv;
  }, []);

  const toggleMute = useCallback(() => {
    if (muted || volume === 0) {
      const restore = lastVolumeRef.current || 0.5;
      setVolumeState(restore);
      setMuted(false);
      if (audioRef.current) audioRef.current.volume = restore;
    } else {
      lastVolumeRef.current = volume;
      setMuted(true);
      if (audioRef.current) audioRef.current.volume = 0;
    }
  }, [muted, volume]);

  // 新分集加载时保持音量设置
  useEffect(() => {
    if (audioRef.current) audioRef.current.volume = muted ? 0 : volume;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentEpisode, playId]);

  useEffect(() => {
    const audio = audioRef.current;
    if (!audio) return;
    const onTime = () => {
      setCurrentTime(audio.currentTime);
      // 持续记录"最后已知进度"：断流后 currentTime 不可信，用此值续播
      if (audio.currentTime > 0) lastGoodTimeRef.current = audio.currentTime;
    };
    const onDur = () => setDuration(audio.duration);
    const onEnd = () => {
      setPlaying(false);
      // 标记当前集播放完毕并保存
      if (currentEpisode && audio.duration > 0) {
        saveProgress(currentEpisode.id, audio.duration, audio.duration).catch(() => {});
      }
      // "播完本集即停"模式：不触发连播，直接停在此处
      if (sleepAfterEpRef.current) {
        sleepAfterEpRef.current = false;
        setSleepAfterEpisode(false);
        return;
      }
      if (onEndedRef.current) {
        onEndedRef.current();
      }
    };
    const onPlay = () => {
      setPlaying(true);
      // 播放成功（含重连/打断恢复成功）：复位暂停标记与网络自愈状态
      userPausedRef.current = false;
      stopResumeRetry();
      retryCountRef.current = 0;
      setRetryCount(0);
      setNetState('ok');
    };
    const onPause = () => {
      setPlaying(false);
      // 非用户暂停、非自然播完 → 外部打断（来电/提示音抢音频焦点），尝试自动恢复。
      // onInterrupted 内部判定：打断时页面在后台/无焦点则直接放弃（用户已转向其他应用）
      if (!userPausedRef.current && !audio.ended && currentEpisodeRef.current) {
        onInterrupted();
      }
    };
    // 断流自愈：audio error 态不会自行恢复（WiFi 切 4G、电梯断网等），
    // 记录进度后按指数退避自动重连；重连成功由 onPlay 复位状态
    const onError = () => {
      if (!currentEpisodeRef.current) return;
      // 空 src（audio.src=''）也会触发 error，忽略
      if (!audio.currentSrc && !audio.getAttribute('src')) return;
      attemptRecovery();
    };
    audio.addEventListener('timeupdate', onTime);
    audio.addEventListener('loadedmetadata', onDur);
    audio.addEventListener('durationchange', onDur);
    audio.addEventListener('ended', onEnd);
    audio.addEventListener('play', onPlay);
    audio.addEventListener('pause', onPause);
    audio.addEventListener('error', onError);
    // 网络恢复（如切网完成）时：若正处于退避等待，跳过等待立即重连
    const onOnline = () => {
      if (retryTimerRef.current !== null) {
        clearRetryTimer();
        reloadAtLastPos();
      }
    };
    window.addEventListener('online', onOnline);
    // 页面进入后台或窗口失焦：立即放弃打断恢复（用户已转向其他应用/窗口，避免音频叠加）。
    // 回到前台不自动恢复——显示暂停态，由用户手动续播。
    const onAway = () => stopResumeRetry();
    const onVisibility = () => { if (document.hidden) onAway(); };
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('blur', onAway);
    return () => {
      audio.removeEventListener('timeupdate', onTime);
      audio.removeEventListener('loadedmetadata', onDur);
      audio.removeEventListener('durationchange', onDur);
      audio.removeEventListener('ended', onEnd);
      audio.removeEventListener('play', onPlay);
      audio.removeEventListener('pause', onPause);
      audio.removeEventListener('error', onError);
      window.removeEventListener('online', onOnline);
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('blur', onAway);
    };
    // audio 元素在 App 挂载后即存在，依赖 currentEpisode/playId 以在就绪后注册监听
  }, [currentEpisode, playId, attemptRecovery, onInterrupted, stopResumeRetry]);

  // 每 5s 保存进度 + 播放看门狗（用 ref 读取最新时间，避免因 currentTime 频繁变化导致 interval 重建）
  const currentTimeRef = useRef(0);
  const durationRef = useRef(0);
  currentTimeRef.current = currentTime;
  durationRef.current = duration;
  // 看门狗状态：上次检查时的进度与停滞计数
  const stallRef = useRef({ time: 0, count: 0 });
  useEffect(() => {
    if (!playing || !currentEpisode) return;
    stallRef.current = { time: currentTimeRef.current, count: 0 };
    const id = window.setInterval(() => {
      const audio = audioRef.current;
      if (!audio) return;
      // 状态脱钩：界面在播但 audio 实际已暂停（非自然结束）→ 直接续播
      if (audio.paused && !audio.ended) { audio.play().catch(() => {}); return; }
      const ct = currentTimeRef.current;
      // 播放看门狗：playing 态但进度连续 ~10s 停滞（移动端偶发"假播放"：
      // play() 成功但实际卡住且不触发 error 事件）→ 复用网络自愈重载恢复。
      // 网络自愈进行中（retryCount>0）时跳过，避免打断退避节奏。
      if (Math.abs(ct - stallRef.current.time) < 0.3) {
        if (retryCountRef.current === 0) {
          stallRef.current.count += 1;
          if (stallRef.current.count >= 2) {
            stallRef.current.count = 0;
            attemptRecovery();
          }
        }
      } else {
        stallRef.current.count = 0;
      }
      stallRef.current.time = ct;
      // 保存进度
      const du = durationRef.current;
      if (ct > 0 && du > 0) {
        saveProgress(currentEpisode.id, ct, du).catch(() => {});
      }
    }, 5000);
    return () => clearInterval(id);
  }, [playing, currentEpisode, attemptRecovery]);

  // 暂停时也保存
  const saveNow = useCallback(() => {
    if (currentEpisode && currentTime > 0) {
      saveProgress(currentEpisode.id, currentTime, duration || currentTime + 1).catch(() => {});
    }
  }, [currentEpisode, currentTime, duration]);

  // 睡眠定时器：minutes 分钟后自动暂停（null/0 取消）。
  // 另有"播完本集即停"模式（sleepAfterEpisode），与分钟定时互斥。
  const [sleepEndsAt, setSleepEndsAt] = useState<number | null>(null);
  const sleepTimerRef = useRef<number | null>(null);
  const [sleepAfterEpisode, setSleepAfterEpisode] = useState(false);
  const sleepAfterEpRef = useRef(false);
  sleepAfterEpRef.current = sleepAfterEpisode;

  const clearSleepTimer = useCallback(() => {
    if (sleepTimerRef.current !== null) {
      window.clearTimeout(sleepTimerRef.current);
      sleepTimerRef.current = null;
    }
    setSleepEndsAt(null);
  }, []);

  const setSleepTimer = useCallback((minutes: number | null) => {
    clearSleepTimer();
    setSleepAfterEpisode(false); // 常规分钟定时取消"本集播完"模式
    if (!minutes || minutes <= 0) {
      return;
    }
    setSleepEndsAt(Date.now() + minutes * 60_000);
    sleepTimerRef.current = window.setTimeout(() => {
      pause();
      setSleepEndsAt(null);
      sleepTimerRef.current = null;
    }, minutes * 60_000);
  }, [pause, clearSleepTimer]);

  // setSleepAfterEpisodeMode 开启/关闭"播完本集即停"（与分钟定时互斥）
  const setSleepAfterEpisodeMode = useCallback((on: boolean) => {
    if (on) {
      clearSleepTimer();
    }
    setSleepAfterEpisode(on);
  }, [clearSleepTimer]);

  // 卸载时清理挂起的重连/恢复定时器
  useEffect(() => () => { clearRetryTimer(); stopResumeRetry(); }, [clearRetryTimer, stopResumeRetry]);

  return { audioRef, currentEpisode, playing, currentTime, duration, rate,
    play, togglePlay, pause, seek, setPlaybackRate, saveNow,
    volume, muted, setVolume, toggleMute,
    sleepEndsAt, setSleepTimer, sleepAfterEpisode, setSleepAfterEpisodeMode,
    netState, retryCount, retryNow };
}
