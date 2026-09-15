// 双端通信说明：本项目桌面端（Wails）通过 AssetServer Middleware 将 /api/* 转发到
// 同一 HTTP handler（见 main.go），因此桌面端与 Web 端统一走 fetch('/api/...')，
// 无需 IPC 分支，天然双端一致。

function tk(): string | null { return localStorage.getItem('token'); }

// Token 过期/失效时的全局回调（由 App 注入：清 token → 回登录页）。
// 避免各页面 catch 里重复处理"未登录"，用户不再面对散落的 401 报错。
let onUnauthorized: (() => void) | null = null;
export function setOnUnauthorized(fn: () => void) { onUnauthorized = fn; }

function authHeaders(): Record<string, string> {
  const t = tk();
  return t ? { Authorization: `Bearer ${t}`, 'Content-Type': 'application/json' } : { 'Content-Type': 'application/json' };
}

async function req<T = any>(method: string, path: string, body?: any): Promise<T> {
  const res = await fetch(path, { method, headers: authHeaders(), body: body ? JSON.stringify(body) : undefined });
  if (res.status === 401) {
    localStorage.removeItem('token');
    onUnauthorized?.();
    throw new Error('登录已过期，请重新登录');
  }
  if (!res.ok) { const e = await res.json().catch(() => ({ error: res.statusText })); throw new Error(e.error || '请求失败'); }
  if (res.status === 204) return undefined as T;
  return res.json();
}

// ---- Auth ----
export async function login(username: string, password: string) { const d: any = await req('POST', '/api/auth/login', { username, password }); localStorage.setItem('token', d.token); return d.user; }
export async function register(username: string, password: string) { const d: any = await req('POST', '/api/auth/register', { username, password }); localStorage.setItem('token', d.token); return d.user; }
export async function me() { return req('GET', '/api/auth/me'); }
export function getToken() { return tk(); }
export function logout() { localStorage.removeItem('token'); }
export async function getAuthConfig(): Promise<{ allowRegistration: boolean }> { return req('GET', '/api/auth/config'); }
export async function changePassword(oldPassword: string, newPassword: string) { return req('PUT', '/api/auth/password', { oldPassword, newPassword }); }

// ---- User Settings ----
export interface UserSettings {
  userId?: string;
  playbackRate: number;
  autoNext: boolean;
  theme: string;
  skipForward: number;
  skipBackward: number;
}
export async function getUserSettings(): Promise<UserSettings> { return req('GET', '/api/user/settings'); }
export async function saveUserSettings(settings: Partial<UserSettings>): Promise<UserSettings> { return req('PUT', '/api/user/settings', settings); }

// ---- Books ----
export interface Book {
  id: string; storageId: string; title: string; author: string; cover: string;
  description: string; rootPath: string; storageName?: string; episodeCount?: number;
}
export interface Episode { id: string; bookId: string; storageId: string; title: string; filePath: string; size: number; order: number; duration: number; }
export async function listBooks(): Promise<Book[]> { return req('GET', '/api/books'); }
export async function getBook(bookId: string): Promise<Book> { return req('GET', `/api/books/${bookId}`); }
export async function listEpisodes(bookId: string): Promise<Episode[]> { return req('GET', `/api/books/${bookId}/episodes`); }
export function coverUrl(bookId: string): string { return `/api/books/${bookId}/cover?token=${tk() || ''}`; }
export function streamUrl(episodeId: string): string { return `/api/episodes/${episodeId}/stream?token=${tk() || ''}`; }
export async function updateBook(bookId: string, data: { title?: string; author?: string; cover?: string; description?: string }): Promise<Book> { return req('PUT', `/api/books/${bookId}`, data); }
export async function rescanBook(bookId: string): Promise<any> { return req('POST', `/api/books/${bookId}/rescan`); }
export async function uploadBookCover(bookId: string, file: File): Promise<{ message: string; book: Book; cover: string }> {
  const formData = new FormData();
  formData.append('cover', file);
  const t = tk();
  const res = await fetch(`/api/books/${bookId}/cover`, {
    method: 'POST',
    headers: t ? { Authorization: `Bearer ${t}` } : undefined,
    body: formData,
  });
  if (!res.ok) {
    const e = await res.json().catch(() => ({ error: res.statusText }));
    throw new Error(e.error || '封面上传失败');
  }
  return res.json();
}

// ---- Library Summary（书库聚合进度，一次拉取） ----
export interface BookSummary {
  bookId: string;
  total: number;
  done: number;
  started: number;
  lastEpisodeId?: string;
  lastEpisodeIndex?: number; // 最近收听分集序号（0 起，按目录顺序）
  lastPosition: number;
  lastUpdated?: string; // 最近收听时间（RFC3339），用于继续收听/排序
}
export async function getLibrarySummary(): Promise<Record<string, BookSummary>> { return req('GET', '/api/library/summary'); }

// ---- Progress ----
export interface Progress { id: string; userId: string; episodeId: string; position: number; duration: number; isFinished?: boolean; updatedAt: string; }
// 导出条目：附带定位信息，跨实例导入时按路径回退匹配
export interface ProgressExportItem extends Progress { bookTitle?: string; epTitle?: string; filePath?: string; }
export async function getProgress(episodeId?: string, bookId?: string): Promise<Progress | Progress[]> {
  const p = new URLSearchParams(); if (episodeId) p.set('episodeId', episodeId); if (bookId) p.set('bookId', bookId);
  return req('GET', '/api/progress?' + p.toString());
}
export async function saveProgress(episodeId: string, position: number, duration: number) { return req('POST', '/api/progress', { episodeId, position, duration }); }
export async function exportProgress(): Promise<ProgressExportItem[]> { return req('GET', '/api/progress/export'); }
export async function importProgress(data: ProgressExportItem[]): Promise<{ imported: number; skipped: number }> { return req('POST', '/api/progress/import', data); }

// ---- Storage (Admin) ----
export interface StorageInfo {
  id: string;
  name: string;
  type: string;
  enabled: boolean;
  streamMode: string;
  rootPath?: string;
  driverStatus?: 'ready' | 'error' | 'not_ready';
  driverError?: string;
}
export interface Field { name: string; label: string; type: string; required: boolean; options?: string[]; dependsOn?: string; help?: string; }
export async function listStorages(): Promise<StorageInfo[]> { return req('GET', '/api/storages'); }
export async function getStorage(id: string): Promise<any> { return req('GET', `/api/storages/${id}`); }
export async function getStorageFields(type: string): Promise<Field[]> { return req('GET', `/api/storages/fields?type=${type}`); }
export async function browseStorage(id: string, path?: string): Promise<any[]> { return req('GET', `/api/storages/${id}/browse?path=${encodeURIComponent(path || '/')}`); }
export async function upsertStorage(data: any) { return req('POST', '/api/storages', data); }
export async function deleteStorage(id: string) { return req('DELETE', `/api/storages/${id}`); }
export async function scanStorage(id: string) { return req('POST', `/api/storages/${id}/scan`); }
export async function cleanupEmptyBooks() { return req('POST', '/api/storages/cleanup-empty'); }
export interface ScanStatus { storageId: string; scanning: boolean; message: string; current: number; total: number; books: number; episodes: number; listFailures?: number; error?: string; }
export async function getScanStatus(id: string): Promise<ScanStatus> { return req('GET', `/api/storages/${id}/scan/status`); }

// ---- Users (Admin) ----
export async function listUsers(): Promise<any[]> { return req('GET', '/api/admin/users'); }
export async function createUser(username: string, password: string, role: string) { return req('POST', '/api/admin/users', { username, password, role }); }
export async function deleteUser(id: string) { return req('DELETE', `/api/admin/users/${id}`); }
export async function adminResetPassword(userId: string, password: string) { return req('POST', `/api/admin/users/${userId}/reset-password`, { password }); }
export async function getAdminSettings(): Promise<{ allowRegistration: boolean }> { return req('GET', '/api/admin/settings'); }
export async function saveAdminSettings(settings: { allowRegistration: boolean }) { return req('PUT', '/api/admin/settings', settings); }
