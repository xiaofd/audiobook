import { useEffect, useState } from 'react';
import { login, register, getAuthConfig } from '../services/api';
import { HeadphonesIcon } from '../components/icons';

export default function Login({ onLogin }: { onLogin: (user: any) => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [allowRegister, setAllowRegister] = useState(false);

  // 注册入口按后端开关动态显示（默认关闭，管理员可在管理页开启）
  useEffect(() => {
    getAuthConfig().then(c => setAllowRegister(!!c.allowRegistration)).catch(() => {});
  }, []);

  const handleSubmit = async (e: React.FormEvent, isRegister: boolean) => {
    e.preventDefault();
    setError(''); setLoading(true);
    try {
      const fn = isRegister ? register : login;
      const user = await fn(username, password);
      onLogin(user);
    } catch (err: any) { setError(err.message); }
    setLoading(false);
  };

  const inputCls = "w-full px-4 py-2.5 bg-slate-50 dark:bg-slate-800/80 border border-slate-200 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white text-sm placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:border-indigo-500 focus:ring-1 focus:ring-indigo-500 transition";

  return (
    <div className="min-h-screen bg-slate-50 dark:bg-[#0b0f17] flex items-center justify-center px-4 transition-colors">
      <div className="w-full max-w-sm">
        <div className="flex flex-col items-center mb-8">
          <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-indigo-500 via-indigo-600 to-purple-600 flex items-center justify-center shadow-xl shadow-indigo-500/25 mb-4 text-white">
            <HeadphonesIcon className="w-8 h-8" />
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-100">有声书播放器</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400 mt-1.5 font-medium">随时随地，听你想听</p>
        </div>

        <form onSubmit={(e) => handleSubmit(e, false)} className="bg-white dark:bg-slate-900/70 border border-slate-200/80 dark:border-slate-800 rounded-2xl p-6 sm:p-8 shadow-xl shadow-slate-200/50 dark:shadow-none backdrop-blur-xl">
          {error && <div className="bg-red-500/10 border border-red-500/30 text-red-600 dark:text-red-400 px-3.5 py-2 rounded-xl mb-4 text-sm font-medium">{error}</div>}
          <div className="space-y-3.5">
            <div>
              <label className="text-xs font-semibold text-slate-600 dark:text-slate-400 block mb-1">用户名</label>
              <input className={inputCls} placeholder="请输入用户名" value={username} onChange={e => setUsername(e.target.value)} autoFocus />
            </div>
            <div>
              <label className="text-xs font-semibold text-slate-600 dark:text-slate-400 block mb-1">密码</label>
              <input className={inputCls} placeholder="请输入密码" type="password" value={password} onChange={e => setPassword(e.target.value)} />
            </div>
          </div>
          <div className="flex gap-3 mt-6">
            <button type="submit" disabled={loading} className="flex-1 py-2.5 bg-indigo-600 hover:bg-indigo-500 text-white rounded-xl text-sm font-medium disabled:opacity-50 transition shadow-md shadow-indigo-600/20">
              {loading ? '登录中...' : '登录'}
            </button>
            {allowRegister && (
              <button type="button" disabled={loading} onClick={(e) => handleSubmit(e as any, true)} className="flex-1 py-2.5 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-xl text-sm font-medium transition">
                注册
              </button>
            )}
          </div>
          {!allowRegister && (
            <p className="text-[11px] text-slate-400 dark:text-slate-500 mt-3 text-center">注册已关闭，如需账户请联系管理员</p>
          )}
        </form>
      </div>
    </div>
  );
}