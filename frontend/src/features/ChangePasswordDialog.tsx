import { useState } from 'react';
import { changePassword } from '../services/api';

// ChangePasswordDialog 修改当前用户密码弹窗（成功后建议重新登录）。
export default function ChangePasswordDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [oldPw, setOldPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [confirmPw, setConfirmPw] = useState('');
  const [err, setErr] = useState('');
  const [ok, setOk] = useState(false);
  const [loading, setLoading] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setErr('');
    if (newPw.length < 6) { setErr('新密码至少 6 位'); return; }
    if (newPw !== confirmPw) { setErr('两次输入的新密码不一致'); return; }
    setLoading(true);
    try {
      await changePassword(oldPw, newPw);
      setOk(true);
    } catch (e: any) {
      setErr(e.message || '修改失败');
    }
    setLoading(false);
  };

  const inputCls = "w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition";

  return (
    <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
      <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl p-6 w-full max-w-sm shadow-2xl text-slate-900 dark:text-slate-100">
        {ok ? (
          <div className="text-center py-4">
            <div className="w-12 h-12 rounded-full bg-emerald-500/10 border border-emerald-500/30 flex items-center justify-center mx-auto mb-4 text-emerald-500 font-bold text-xl">✓</div>
            <h3 className="text-lg font-bold mb-2">密码修改成功</h3>
            <p className="text-sm text-slate-500 dark:text-slate-400 mb-6">为安全起见，请使用新密码重新登录。</p>
            <button onClick={onDone}
              className="w-full py-2.5 bg-indigo-600 hover:bg-indigo-500 text-white rounded-xl text-sm font-medium transition shadow-md shadow-indigo-600/20">重新登录</button>
          </div>
        ) : (
          <form onSubmit={submit}>
            <h3 className="text-lg font-bold mb-4">修改密码</h3>
            {err && <div className="bg-red-500/10 border border-red-500/30 text-red-600 dark:text-red-400 px-3.5 py-2 rounded-xl mb-4 text-sm font-medium">{err}</div>}
            <div className="space-y-3.5">
              <div>
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400 block mb-1">原密码</label>
                <input className={inputCls} type="password" placeholder="请输入原密码" value={oldPw} onChange={e => setOldPw(e.target.value)} autoFocus />
              </div>
              <div>
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400 block mb-1">新密码</label>
                <input className={inputCls} type="password" placeholder="至少 6 位" value={newPw} onChange={e => setNewPw(e.target.value)} />
              </div>
              <div>
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400 block mb-1">确认新密码</label>
                <input className={inputCls} type="password" placeholder="再次输入新密码" value={confirmPw} onChange={e => setConfirmPw(e.target.value)} />
              </div>
            </div>
            <div className="flex gap-2.5 mt-6">
              <button type="submit" disabled={loading || !oldPw || !newPw}
                className="flex-1 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition shadow-md shadow-indigo-600/20 disabled:opacity-50">
                {loading ? '提交中...' : '确认修改'}
              </button>
              <button type="button" onClick={onClose}
                className="flex-1 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg text-sm font-medium transition">取消</button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
