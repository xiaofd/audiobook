// Service Worker：静态资源离线缓存 + 音频流快速命中回退
const STATIC_CACHE = 'audiobook-static-v2';
const MEDIA_CACHE = 'audiobook-media-v1';

self.addEventListener('install', () => self.skipWaiting());

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((k) => k !== STATIC_CACHE && k !== MEDIA_CACHE)
          .map((k) => caches.delete(k))
      )
    ).then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);

  // 1. 音频流优先检查预加载 Cache，支持 Range 或分片命中
  if (url.pathname.includes('/stream')) {
    e.respondWith(
      caches.open(MEDIA_CACHE).then(async (cache) => {
        const cached = await cache.match(req);
        if (cached) {
          return cached;
        }
        return fetch(req);
      }).catch(() => fetch(req))
    );
    return;
  }

  // 2. 其它非本源或者普通 API 动态请求直接放行
  if (url.origin !== location.origin || url.pathname.startsWith('/api/')) {
    return;
  }

  // 3. 静态前端资源（网络优先，离线回退缓存）
  e.respondWith(
    fetch(req)
      .then((resp) => {
        if (resp.ok) {
          const clone = resp.clone();
          caches.open(STATIC_CACHE).then((c) => c.put(req, clone));
        }
        return resp;
      })
      .catch(() => caches.match(req).then((r) => r || caches.match('/')))
  );
});
