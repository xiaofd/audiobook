// Service Worker：静态资源离线缓存（网络优先，失败回退缓存）。
// 注意：/api/*（含音频流与封面）一律不缓存，避免缓存鉴权内容与超大音频。
const CACHE_NAME = 'audiobook-static-v1';

self.addEventListener('install', () => self.skipWaiting());

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener('fetch', (e) => {
  const req = e.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== location.origin) return;
  if (url.pathname.startsWith('/api/')) return; // API 动态数据不缓存

  e.respondWith(
    fetch(req)
      .then((resp) => {
        if (resp.ok) {
          const clone = resp.clone();
          caches.open(CACHE_NAME).then((c) => c.put(req, clone));
        }
        return resp;
      })
      .catch(() => caches.match(req).then((r) => r || caches.match('/')))
  );
});
