// IndexedDB + CacheStorage 音频与元数据离线/预加载管理器
// 1. 预加载下一集（智能静默加载，连播零等待）
// 2. 本地持久化保存最新收听状态（即使断网或数月后打开也能丝滑秒级续播）
// 3. LRU 缓存自动清理（保持磁盘健康，最多保留最近 5 集音频缓存）

const DB_NAME = 'audiobook_cache_db';
const DB_VERSION = 1;
const STORE_PLAYBACK = 'playback_state';
const AUDIO_CACHE_NAME = 'audiobook-media-v1';
const MAX_CACHED_EPISODES = 5;

let dbPromise: Promise<IDBDatabase> | null = null;

function getDB(): Promise<IDBDatabase> {
  if (dbPromise) return dbPromise;
  dbPromise = new Promise((resolve, reject) => {
    if (!('indexedDB' in window)) {
      reject(new Error('IndexedDB not supported'));
      return;
    }
    const req = indexedDB.open(DB_NAME, DB_VERSION);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(STORE_PLAYBACK)) {
        db.createObjectStore(STORE_PLAYBACK, { keyPath: 'key' });
      }
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

export interface CachedPlaybackState {
  book: any;
  episodes: any[];
  epIndex: number;
  position: number;
  updatedAt: number;
}

// 保存本地续播状态（即使离线/几个月后打开也能立即定位恢复）
export async function saveLocalPlaybackState(state: CachedPlaybackState): Promise<void> {
  try {
    const db = await getDB();
    const tx = db.transaction(STORE_PLAYBACK, 'readwrite');
    tx.objectStore(STORE_PLAYBACK).put({ key: 'last_playback', ...state, updatedAt: Date.now() });
  } catch {
    // 降级使用 localStorage 备份
    try {
      localStorage.setItem('audiobook_last_playback', JSON.stringify({ ...state, updatedAt: Date.now() }));
    } catch {}
  }
}

// 读取本地持久化续播状态
export async function getLocalPlaybackState(): Promise<CachedPlaybackState | null> {
  try {
    const db = await getDB();
    const tx = db.transaction(STORE_PLAYBACK, 'readonly');
    const store = tx.objectStore(STORE_PLAYBACK);
    const result = await new Promise<any>((resolve) => {
      const req = store.get('last_playback');
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
    });
    if (result) return result;
  } catch {}

  try {
    const backup = localStorage.getItem('audiobook_last_playback');
    if (backup) return JSON.parse(backup);
  } catch {}
  return null;
}

// 缓存清理队列管理
let cachedUrlsList: string[] = [];

// 静默预加载下一集（利用 CacheStorage 或 Link prefetch 预热）
export async function preloadNextEpisode(streamUrl: string): Promise<void> {
  if (!streamUrl) return;

  // 1. 如果支持 CacheStorage，预拉取前 2MB~5MB 或完整文件以支持秒开
  if ('caches' in window) {
    try {
      const cache = await caches.open(AUDIO_CACHE_NAME);
      const match = await cache.match(streamUrl);
      if (match) return; // 已缓存

      // 控制缓存数量，清理过期音频 (LRU)
      cachedUrlsList.push(streamUrl);
      if (cachedUrlsList.length > MAX_CACHED_EPISODES) {
        const toRemove = cachedUrlsList.shift();
        if (toRemove) cache.delete(toRemove).catch(() => {});
      }

      // 静默拉取
      const resp = await fetch(streamUrl, {
        headers: { Range: 'bytes=0-2097151' }, // 预加载前 2MB 数据足以实现秒播
        mode: 'cors',
      });
      if (resp.ok || resp.status === 206) {
        await cache.put(streamUrl, resp.clone());
      }
      return;
    } catch {}
  }

  // 2. 降级：使用 <link rel="prefetch">
  try {
    let link = document.querySelector(`link[data-preload-stream]`) as HTMLLinkElement | null;
    if (!link) {
      link = document.createElement('link');
      link.rel = 'prefetch';
      link.as = 'fetch';
      link.setAttribute('data-preload-stream', '1');
      document.head.appendChild(link);
    }
    link.href = streamUrl;
  } catch {}
}
