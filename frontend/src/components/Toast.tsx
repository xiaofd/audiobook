import { useEffect, useRef, useState } from 'react';

// 轻量全局 Toast：模块级单例，任意组件调用 toast() 即可，无需 context 传递。
// 替代各页面分散的 inline 成功/失败文本（成功失败同色、位置不一致、需手动消失）。

type ToastType = 'success' | 'error' | 'info';
interface ToastItem { id: number; msg: string; type: ToastType }

let pushToast: ((msg: string, type: ToastType) => void) | null = null;
let seq = 0;

export function toast(msg: string, type: ToastType = 'info') {
  pushToast?.(msg, type);
}

export function ToastHost() {
  const [items, setItems] = useState<ToastItem[]>([]);
  const timersRef = useRef<Map<number, number>>(new Map());

  useEffect(() => {
    pushToast = (msg, type) => {
      const id = ++seq;
      setItems(prev => [...prev.slice(-3), { id, msg, type }]); // 最多同时 4 条
      const timer = window.setTimeout(() => {
        setItems(prev => prev.filter(t => t.id !== id));
        timersRef.current.delete(id);
      }, 3200);
      timersRef.current.set(id, timer);
    };
    return () => {
      pushToast = null;
      timersRef.current.forEach(t => clearTimeout(t));
      timersRef.current.clear();
    };
  }, []);

  const dismiss = (id: number) => {
    const timer = timersRef.current.get(id);
    if (timer) { clearTimeout(timer); timersRef.current.delete(id); }
    setItems(prev => prev.filter(t => t.id !== id));
  };

  if (items.length === 0) return null;

  const styleOf = (type: ToastType) => type === 'success'
    ? 'bg-emerald-600/95 shadow-emerald-600/30'
    : type === 'error'
      ? 'bg-rose-600/95 shadow-rose-600/30'
      : 'bg-slate-800/95 shadow-slate-800/30';

  return (
    <div className="fixed top-16 right-4 z-[60] flex flex-col gap-2 items-end pointer-events-none">
      {items.map(t => (
        <div key={t.id}
          onClick={() => dismiss(t.id)}
          className={`pointer-events-auto cursor-pointer max-w-xs w-fit px-4 py-2.5 rounded-xl text-white text-sm font-medium shadow-lg backdrop-blur-sm animate-[toast-in_.25s_ease-out] ${styleOf(t.type)}`}>
          {t.msg}
        </div>
      ))}
    </div>
  );
}
