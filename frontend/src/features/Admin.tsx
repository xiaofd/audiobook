import { useEffect, useState, useRef } from 'react';
import { listStorages, getStorage, getStorageFields, upsertStorage, deleteStorage, scanStorage, getScanStatus, browseStorage, cleanupEmptyBooks, listUsers, createUser, deleteUser, adminResetPassword, getAdminSettings, saveAdminSettings, type Field, type StorageInfo, type ScanStatus } from '../services/api';
import { FolderIcon, KeyIcon } from '../components/icons';
import { toast } from '../components/Toast';

export default function Admin() {
  const [tab, setTab] = useState<'storages' | 'users'>('storages');
  const [storages, setStorages] = useState<StorageInfo[]>([]);
  const [users, setUsers] = useState<any[]>([]);
  const [allowRegistration, setAllowRegistration] = useState(false);

  const load = async () => {
    try {
      const [ss, us, settings] = await Promise.all([listStorages(), listUsers(), getAdminSettings()]);
      setStorages(ss); setUsers(us);
      setAllowRegistration(!!settings.allowRegistration);
    } catch (e: any) { toast(e.message, 'error'); }
  };
  useEffect(() => { load(); }, []);

  const toggleRegistration = async () => {
    
    try {
      await saveAdminSettings({ allowRegistration: !allowRegistration });
      setAllowRegistration(!allowRegistration);
      toast(!allowRegistration ? '已开放自助注册' : '已关闭自助注册', 'success');
    } catch (e: any) { toast(e.message, 'error'); }
  };

  const [showAdd, setShowAdd] = useState(false);
  const [editId, setEditId] = useState('');
  const [sName, setSName] = useState('');
  const [sType, setSType] = useState('local');
  const [sMode, setSMode] = useState('auto');
  const [sFields, setSFields] = useState<Field[]>([]);
  const [sConfig, setSConfig] = useState<Record<string, string>>({});
  const [loadingFields, setLoadingFields] = useState(false);

  const [showBrowse, setShowBrowse] = useState(false);
  const [browsePath, setBrowsePath] = useState('/');
  const [browseItems, setBrowseItems] = useState<any[]>([]);
  const [browseLoading, setBrowseLoading] = useState(false);
  const [selectedRoot, setSelectedRoot] = useState('/');
  const [scanningId, setScanningId] = useState('');
  const [scanInfo, setScanInfo] = useState<ScanStatus | null>(null);
  const scanTimerRef = useRef<number | null>(null);

  // 组件卸载时清理扫描轮询定时器，避免离开管理页后仍持续请求
  useEffect(() => {
    return () => {
      if (scanTimerRef.current) clearInterval(scanTimerRef.current);
    };
  }, []);

  const loadFields = async (type: string) => {
    setLoadingFields(true);
    try {
      const f = await getStorageFields(type);
      setSFields(f);
      // select 类型字段默认选中第一个选项
      const init: Record<string, string> = {};
      for (const field of f) {
        if (field.type === 'select' && field.options && field.options.length > 0) {
          init[field.name] = field.options[0];
        } else {
          init[field.name] = '';
        }
      }
      setSConfig(init);
    } catch { setSFields([]); }
    setLoadingFields(false);
  };

  const openAdd = () => { setEditId(''); setSName(''); setSType('local'); setSMode('auto'); setSelectedRoot('/'); loadFields('local'); setShowAdd(true); };

  const openEdit = async (id: string) => {
    try {
      const s = await getStorage(id);
      setEditId(s.id); setSName(s.name); setSType(s.type); setSMode(s.streamMode || 'auto');
      setSelectedRoot(s.rootPath || '/');
      // 先加载字段定义，再合并已有配置（敏感字段设为空，placeholder 提示已配置）
      const f = await getStorageFields(s.type);
      setSFields(f);
      const merged: Record<string, string> = {};
      for (const field of f) {
        if (field.type === 'select' && field.options && field.options.length > 0) {
          merged[field.name] = s.config?.[field.name] || field.options[0];
        } else if (field.type === 'password') {
          merged[field.name] = ''; // 敏感字段不显示明文
        } else {
          merged[field.name] = s.config?.[field.name] || '';
        }
      }
      setSConfig(merged);
      setShowAdd(true);
    } catch (e: any) { toast(e.message, 'error'); }
  };

  const handleTypeChange = (t: string) => { setSType(t); loadFields(t); };

  const saveStorage = async (): Promise<string> => {
    const res: any = await upsertStorage({ id: editId, name: sName, type: sType, enabled: true, streamMode: sMode, rootPath: selectedRoot, config: buildFinalConfig(sFields, sConfig, editId) });
    return res.id;
  };

  const handleAddClick = async () => {
    
    try { const id = await saveStorage(); setEditId(id); toast('存储已保存，请选择书库根目录', 'success'); await openBrowse(id, '/'); }
    catch (e: any) { toast(e.message, 'error'); }
  };

  const openBrowse = async (storageId: string, path: string) => {
    setShowBrowse(true); setBrowsePath(path); setBrowseLoading(true);
    try { const items = await browseStorage(storageId, path); setBrowseItems(items); } catch (e: any) { toast(e.message, 'error'); setBrowseItems([]); }
    setBrowseLoading(false);
  };

  const enterDir = async (item: any) => { await openBrowse(editId, item.path); };

  const goUp = async () => {
    const parent = browsePath === '/' || browsePath === '' ? '/' : browsePath.split('/').slice(0, -1).join('/') || '/';
    await openBrowse(editId, parent);
  };

  const confirmRoot = async () => {
    
    try {
      await upsertStorage({ id: editId, name: sName, type: sType, enabled: true, streamMode: sMode, rootPath: browsePath, config: buildFinalConfig(sFields, sConfig, editId) });
      setSelectedRoot(browsePath); setShowBrowse(false); setShowAdd(false);
      toast('书库根目录已设置，可点击扫描', 'success'); await load();
    } catch (e: any) { toast(e.message, 'error'); }
  };

  const handleScan = async (id: string) => {
     setScanningId(id);
    setScanInfo({ storageId: id, scanning: true, message: '后台扫描任务启动中...', current: 0, total: 1, books: 0, episodes: 0 });

    try {
      await scanStorage(id);
    } catch (e: any) {
      toast(`启动扫描失败：${e.message}`, 'error');
      setScanningId('');
      return;
    }

    // 开启轮询状态定时器，由后端异步 Worker 推进
    if (scanTimerRef.current) clearInterval(scanTimerRef.current);
    scanTimerRef.current = window.setInterval(async () => {
      try {
        const st = await getScanStatus(id);
        setScanInfo(st);
        if (!st.scanning) {
          if (scanTimerRef.current) clearInterval(scanTimerRef.current);
          if (st.error) {
            toast(`扫描异常：${st.error}`, 'error');
          } else {
            toast(st.message || `扫描完成：共入库 ${st.books} 本书，${st.episodes} 集`, 'success');
            await load();
          }
          setTimeout(() => setScanningId(''), 3000);
        }
      } catch {
        // ignore
      }
    }, 1000);
  };

  const handleCleanup = async () => {
    
    try {
      const res: any = await cleanupEmptyBooks();
      toast(`清理完成：已清除 ${res.deleted} 条无效空书籍`, 'success');
      await load();
    } catch (e: any) {
      toast(`清理失败：${e.message}`, 'error');
    }
  };
  const handleDelStorage = async (id: string) => {
    try { await deleteStorage(id); await load(); } catch (e: any) { toast(e.message, 'error'); }
  };

  const [uName, setUName] = useState('');
  const [uPass, setUPass] = useState('');
  const [uRole, setURole] = useState('user');
  const addUser = async () => {
    
    try { await createUser(uName, uPass, uRole); setUName(''); setUPass(''); toast('用户已创建', 'success'); await load(); } catch (e: any) { toast(e.message, 'error'); }
  };
  const handleDelUser = async (id: string) => {
    try { await deleteUser(id); await load(); } catch (e: any) { toast(e.message, 'error'); }
  };

  // 重置密码弹窗
  const [resetUser, setResetUser] = useState<{ id: string; name: string } | null>(null);
  const [resetPass, setResetPass] = useState('');
  const [resetting, setResetting] = useState(false);
  const handleResetPassword = async () => {
    if (!resetUser) return;
    
    setResetting(true);
    try {
      await adminResetPassword(resetUser.id, resetPass);
      toast(`已重置 ${resetUser.name} 的密码`, 'success');
      setResetUser(null); setResetPass('');
    } catch (e: any) { toast(e.message, 'error'); }
    setResetting(false);
  };

  const inputCls = "w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:border-indigo-500 transition";
  const selectCls = "w-full bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-3 py-2 text-sm text-slate-900 dark:text-white focus:outline-none focus:border-indigo-500 transition";

  return (
    <div className="mx-auto w-full max-w-6xl px-4 sm:px-6 lg:px-8 py-6 sm:py-8 text-slate-900 dark:text-slate-100">
      {/* 顶栏与标签导航 */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 mb-6">
        <div>
          <h2 className="text-2xl font-bold tracking-tight">系统管理</h2>
          <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">管理网盘书源、多账号及数据库维护</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex p-1 bg-slate-100 dark:bg-slate-800/80 rounded-xl">
            <button onClick={() => setTab('storages')} className={`px-3.5 py-1.5 text-xs font-semibold rounded-lg transition ${tab === 'storages' ? 'bg-white dark:bg-slate-700 text-slate-900 dark:text-white shadow-xs' : 'text-slate-600 dark:text-slate-300'}`}>书源存储</button>
            <button onClick={() => setTab('users')} className={`px-3.5 py-1.5 text-xs font-semibold rounded-lg transition ${tab === 'users' ? 'bg-white dark:bg-slate-700 text-slate-900 dark:text-white shadow-xs' : 'text-slate-600 dark:text-slate-300'}`}>用户管理</button>
          </div>
          {tab === 'storages' && (
            <div className="flex items-center gap-2">
              <button onClick={handleCleanup} className="px-3 py-1.5 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg text-xs font-medium transition border border-slate-200 dark:border-slate-700">
                清理空书籍
              </button>
              <button onClick={openAdd} className="px-3.5 py-1.5 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-xs font-semibold transition shadow-md shadow-indigo-600/20">
                + 添加存储
              </button>
            </div>
          )}
        </div>
      </div>

      {tab === 'storages' && (
        <div>
          {showAdd && !showBrowse && (
            <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
              <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl p-6 w-full max-w-md max-h-[90vh] overflow-y-auto shadow-2xl text-slate-900 dark:text-slate-100">
                <h3 className="text-lg font-bold mb-4">{editId ? '编辑存储' : '添加存储'}</h3>
                <div className="space-y-3.5">
                  <input className={inputCls} placeholder="存储名称" value={sName} onChange={e => setSName(e.target.value)} />
                  <select className={selectCls} value={sType} onChange={e => handleTypeChange(e.target.value)}>
                    <option value="local">本地存储</option>
                    <option value="webdav">WebDAV</option>
                    <option value="onedrive">OneDrive</option>
                    <option value="mobilecloud">中国移动云盘</option>
                  </select>
                  {loadingFields && <p className="text-xs text-slate-500 dark:text-slate-400">加载配置字段...</p>}
                  {!loadingFields && sType === 'onedrive' && (
                    <div className="text-xs text-slate-600 dark:text-slate-400 bg-slate-50 dark:bg-slate-800/60 border border-slate-200 dark:border-slate-700/80 rounded-xl px-3.5 py-3 leading-relaxed">
                      可前往 <a href="https://api.oplist.org/" target="_blank" rel="noreferrer" className="text-indigo-600 dark:text-indigo-400 hover:underline font-medium">https://api.oplist.org/</a> 网页获取配置信息。
                      <br /><span className="text-slate-700 dark:text-slate-300 font-medium">提示：</span>只需填写必填的 <span className="font-semibold text-slate-900 dark:text-white">Redirect URI</span> 与 <span className="font-semibold text-slate-900 dark:text-white">Refresh Token</span> 即可直接挂载使用（Client ID与Secret为可选）。
                    </div>
                  )}
                  {!loadingFields && sType === 'webdav' && (
                    <div className="text-xs text-slate-600 dark:text-slate-400 bg-slate-50 dark:bg-slate-800/60 border border-slate-200 dark:border-slate-700/80 rounded-xl px-3.5 py-3 leading-relaxed">
                      支持 <span className="font-semibold text-slate-900 dark:text-white">坚果云 / Nextcloud / 群晖 / AList</span> 等标准 WebDAV 服务。
                      <br /><span className="text-slate-700 dark:text-slate-300 font-medium">提示：</span>坚果云请使用「账户信息 → 安全选项 → 应用密码」生成的专用密码；服务器地址通常形如 <span className="font-mono">https://dav.jianguoyun.com/dav/</span>。
                    </div>
                  )}
                  {!loadingFields && sFields
                    .filter(f => !f.dependsOn || fieldVisible(f.dependsOn, sConfig))
                    .map(f => (
                    <div key={f.name}>
                      <label className="text-xs font-medium text-slate-600 dark:text-slate-400 mb-1 block">{f.label} {f.required && <span className="text-red-500">*</span>}</label>
                      {f.type === 'select' ? (
                        <select className={selectCls} value={sConfig[f.name] || ''} onChange={e => setSConfig({ ...sConfig, [f.name]: e.target.value })}>
                          {(f.options || []).map(opt => (
                            <option key={opt} value={opt}>{fieldOptionLabel(f.name, opt)}</option>
                          ))}
                        </select>
                      ) : (
                        <input className={inputCls} placeholder={f.type === 'password' && editId ? '已配置，留空保持不变' : f.label} type={f.type === 'password' ? 'password' : 'text'}
                          value={sConfig[f.name] || ''} onChange={e => setSConfig({ ...sConfig, [f.name]: e.target.value })} />
                      )}
                      {f.help && <p className="text-[11px] text-slate-500 dark:text-slate-400 mt-1">{f.help}</p>}
                    </div>
                  ))}
                  <select className={selectCls} value={sMode} onChange={e => setSMode(e.target.value)}>
                    <option value="auto">流式模式: auto</option>
                    <option value="proxy">proxy（服务器中转）</option>
                    <option value="redirect">redirect（302 直链）</option>
                  </select>
                  {selectedRoot && selectedRoot !== '/' && <div className="text-xs font-medium text-slate-500 dark:text-slate-400">📁 书库根目录: <span className="text-indigo-600 dark:text-indigo-400">{selectedRoot}</span></div>}
                </div>
                <div className="flex gap-2.5 mt-6">
                  <button onClick={handleAddClick} className="flex-1 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition shadow-md shadow-indigo-600/20">保存并选择目录</button>
                  <button onClick={() => setShowAdd(false)} className="flex-1 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg text-sm font-medium transition">取消</button>
                </div>
              </div>
            </div>
          )}

          {showBrowse && (
            <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
              <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl p-6 w-full max-w-lg max-h-[90vh] overflow-y-auto shadow-2xl text-slate-900 dark:text-slate-100">
                <h3 className="text-lg font-bold mb-1">选择书库根目录</h3>
                <p className="text-xs text-slate-500 dark:text-slate-400 mb-3">浏览存储目录，选择存放有声书的根目录</p>
                <div className="flex items-center gap-2 mb-3">
                  <button onClick={goUp} className="px-3 py-1.5 text-xs font-medium bg-slate-100 dark:bg-slate-800 text-slate-700 dark:text-slate-300 hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg transition">⬆ 上级</button>
                  <span className="text-xs font-medium text-indigo-600 dark:text-indigo-400 truncate flex-1">{browsePath}</span>
                </div>
                {browseLoading && <p className="text-xs text-slate-500 dark:text-slate-400 py-6 text-center">加载中...</p>}
                {!browseLoading && (
                  <div className="max-h-64 overflow-y-auto border border-slate-200 dark:border-slate-800 rounded-xl divide-y divide-slate-100 dark:divide-slate-800">
                    {browseItems.filter(i => i.isDir).map(item => (
                      <div key={item.path} onClick={() => enterDir(item)}
                        className="flex items-center gap-2.5 px-3.5 py-2.5 text-sm cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800/60 transition">
                        <FolderIcon className="w-4 h-4 text-indigo-500 shrink-0" />
                        <span className="flex-1 truncate font-medium text-slate-800 dark:text-slate-200">{item.name}</span>
                        <span className="text-slate-400 dark:text-slate-500 text-xs">进入 →</span>
                      </div>
                    ))}
                    {browseItems.filter(i => i.isDir).length === 0 && <p className="text-xs text-slate-500 dark:text-slate-400 px-3 py-6 text-center">此目录下没有子文件夹</p>}
                  </div>
                )}
                <div className="flex gap-2.5 mt-5">
                  <button onClick={confirmRoot} className="flex-1 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition shadow-md shadow-indigo-600/20">选择当前目录为书库根</button>
                  <button onClick={() => setShowBrowse(false)} className="flex-1 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg text-sm font-medium transition">取消</button>
                </div>
              </div>
            </div>
          )}

          {storages.map(s => {
            const isScanningThis = scanningId === s.id;
            const pct = scanInfo && scanInfo.total > 0 && scanInfo.storageId === s.id
              ? Math.min(100, Math.round((scanInfo.current / scanInfo.total) * 100))
              : 0;
            const isReady = s.driverStatus === 'ready';
            const isError = s.driverStatus === 'error';
            return (
            <div key={s.id} className="bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-2xl p-4 sm:p-5 mb-3.5 shadow-sm hover:border-slate-300 dark:hover:border-slate-700 transition">
              <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-bold text-slate-900 dark:text-slate-100 truncate">{s.name}</span>
                    {/* 驱动健康度状态徽标 */}
                    {isReady ? (
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-emerald-50 text-emerald-600 dark:bg-emerald-950/40 dark:text-emerald-400 border border-emerald-200 dark:border-emerald-800/60">
                        <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" /> 驱动正常
                      </span>
                    ) : isError ? (
                      <span title={s.driverError || '驱动初始化失败'} className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-rose-50 text-rose-600 dark:bg-rose-950/40 dark:text-rose-400 border border-rose-200 dark:border-rose-800/60">
                        <span className="w-1.5 h-1.5 rounded-full bg-rose-500" /> 初始化异常
                      </span>
                    ) : (
                      <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[10px] font-semibold bg-amber-50 text-amber-600 dark:bg-amber-950/40 dark:text-amber-400 border border-amber-200 dark:border-amber-800/60">
                        <span className="w-1.5 h-1.5 rounded-full bg-amber-500" /> 未就绪/待连接
                      </span>
                    )}
                  </div>
                  <div className="text-xs text-slate-500 dark:text-slate-400 mt-1 font-medium flex flex-wrap items-center gap-1.5">
                    <span>{s.type === 'local' ? '本地存储' : s.type === 'webdav' ? 'WebDAV' : s.type === 'onedrive' ? 'OneDrive' : '中国移动云盘'}</span>
                    <span>·</span>
                    <span className="px-1.5 py-0.5 rounded bg-slate-100 dark:bg-slate-800 text-[10px] text-slate-600 dark:text-slate-300 font-mono">{s.streamMode}</span>
                    {s.rootPath && s.rootPath !== '/' && (
                      <>
                        <span>·</span>
                        <span className="truncate max-w-[200px]" title={s.rootPath}>📁 {s.rootPath}</span>
                      </>
                    )}
                  </div>
                  {isError && s.driverError && (
                    <div className="text-[11px] text-rose-600 dark:text-rose-400 mt-1.5 bg-rose-50 dark:bg-rose-950/30 px-2.5 py-1 rounded-lg border border-rose-100 dark:border-rose-900/50 break-all">
                      ⚠️ 驱动故障：{s.driverError}
                    </div>
                  )}
                </div>
                <div className="flex items-center gap-2 self-start sm:self-center shrink-0 pt-1 sm:pt-0">
                  <button onClick={() => openEdit(s.id)} className="px-3 py-1.5 text-xs font-medium bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg transition">编辑</button>
                  <button onClick={() => handleScan(s.id)} disabled={scanningId !== ''} className="px-3 py-1.5 text-xs font-medium bg-emerald-600 hover:bg-emerald-500 text-white rounded-lg transition disabled:opacity-50 disabled:cursor-not-allowed shadow-sm">
                    {isScanningThis ? '扫描中...' : '扫描'}
                  </button>
                  <button onClick={() => handleDelStorage(s.id)} className="px-3 py-1.5 text-xs font-medium bg-red-500/10 dark:bg-red-500/20 hover:bg-red-500/20 text-red-600 dark:text-red-400 rounded-lg transition">删除</button>
                </div>
              </div>

              {/* 扫描实时进度条与状态提示 */}
              {isScanningThis && scanInfo && (
                <div className="mt-4 pt-3.5 border-t border-slate-100 dark:border-slate-800/80">
                  <div className="flex items-center justify-between text-xs text-slate-600 dark:text-slate-300 mb-1.5 font-medium">
                    <span className="flex items-center gap-1.5">
                      <span className="w-2 h-2 rounded-full bg-emerald-500 animate-ping" />
                      {scanInfo.message || '正在扫描刮削网盘有声书...'}
                    </span>
                    <span>{pct}% ({scanInfo.current}/{scanInfo.total})</span>
                  </div>
                  <div className="h-1.5 bg-slate-100 dark:bg-slate-800 rounded-full overflow-hidden">
                    <div className="h-full bg-gradient-to-r from-emerald-500 via-indigo-500 to-purple-600 rounded-full transition-all duration-300" style={{ width: `${pct || 15}%` }} />
                  </div>
                  <div className="flex justify-between text-[11px] text-slate-400 dark:text-slate-500 mt-1 font-medium">
                    <span>已发现并录入图书: {scanInfo.books} 本</span>
                    <span>累计解析音频: {scanInfo.episodes} 集</span>
                  </div>
                  {scanInfo.listFailures && scanInfo.listFailures > 0 && (
                    <div className="text-[11px] text-amber-600 dark:text-amber-400 mt-1.5 font-medium">
                      ⚠️ {scanInfo.listFailures} 次目录读取失败（认证过期或被网盘限流），本次结果可能不完整
                    </div>
                  )}
                </div>
              )}
            </div>
            );
          })}
        </div>
      )}

      {tab === 'users' && (
        <div>
          {/* 注册开关 */}
          <div className="bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-2xl p-5 mb-5 shadow-sm flex items-center justify-between gap-4">
            <div>
              <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">开放自助注册</h3>
              <p className="text-xs text-slate-500 dark:text-slate-400 mt-1">关闭后登录页不显示注册入口，账户仅由管理员创建（公网部署建议关闭）</p>
            </div>
            <button
              role="switch"
              aria-checked={allowRegistration}
              onClick={toggleRegistration}
              className={`relative w-11 h-6 rounded-full transition shrink-0 ${allowRegistration ? 'bg-indigo-600' : 'bg-slate-300 dark:bg-slate-700'}`}
            >
              <span className={`absolute top-0.5 left-0.5 w-5 h-5 rounded-full bg-white shadow transition-transform ${allowRegistration ? 'translate-x-5' : ''}`} />
            </button>
          </div>

          <div className="bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-2xl p-5 mb-5 shadow-sm space-y-3">
            <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">创建新用户</h3>
            <div className="flex gap-2.5 flex-wrap">
              <input className={`${inputCls} w-32`} placeholder="用户名" value={uName} onChange={e => setUName(e.target.value)} />
              <input className={`${inputCls} w-32`} placeholder="密码" type="password" value={uPass} onChange={e => setUPass(e.target.value)} />
              <select className={`${selectCls} w-32`} value={uRole} onChange={e => setURole(e.target.value)}>
                <option value="user">普通用户</option>
                <option value="admin">管理员</option>
              </select>
              <button onClick={addUser} className="px-4 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition shadow-md shadow-indigo-600/20">创建</button>
            </div>
          </div>
          {users.map(u => (
            <div key={u.id} className="bg-white dark:bg-slate-900/60 border border-slate-200/80 dark:border-slate-800/80 rounded-xl p-4 mb-2.5 flex items-center justify-between shadow-sm">
              <div>
                <span className="text-sm font-semibold text-slate-900 dark:text-slate-100">{u.username}</span>
                <span className="text-xs font-medium text-slate-500 dark:text-slate-400 ml-2">[{u.role}]</span>
              </div>
              <div className="flex items-center gap-2">
                <button onClick={() => { setResetUser({ id: u.id, name: u.username }); setResetPass(''); }}
                  className="px-3 py-1.5 text-xs font-medium bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg transition flex items-center gap-1.5">
                  <KeyIcon className="w-3.5 h-3.5" /> 重置密码
                </button>
                <button onClick={() => handleDelUser(u.id)} className="px-3 py-1.5 text-xs font-medium bg-red-500/10 dark:bg-red-500/20 hover:bg-red-500/20 text-red-600 dark:text-red-400 rounded-lg transition">删除</button>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* 重置密码弹窗 */}
      {resetUser && (
        <div className="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-50 p-4">
          <div className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-2xl p-6 w-full max-w-sm shadow-2xl text-slate-900 dark:text-slate-100">
            <h3 className="text-lg font-bold mb-1">重置密码</h3>
            <p className="text-xs text-slate-500 dark:text-slate-400 mb-4">为用户 <span className="font-semibold text-indigo-600 dark:text-indigo-400">{resetUser.name}</span> 设置新密码</p>
            <input className={inputCls} type="password" placeholder="新密码（至少 6 位）" value={resetPass} onChange={e => setResetPass(e.target.value)} autoFocus />
            <div className="flex gap-2.5 mt-5">
              <button onClick={handleResetPassword} disabled={resetting || resetPass.length < 6}
                className="flex-1 py-2 bg-indigo-600 hover:bg-indigo-500 text-white rounded-lg text-sm font-medium transition shadow-md shadow-indigo-600/20 disabled:opacity-50">
                {resetting ? '提交中...' : '确认重置'}
              </button>
              <button onClick={() => { setResetUser(null); setResetPass(''); }}
                className="flex-1 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg text-sm font-medium transition">取消</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

// 下拉选项的中文显示标签
function fieldOptionLabel(fieldName: string, value: string): string {
  if (fieldName === 'authType') {
    switch (value) {
      case 'authorization': return 'Authorization（Basic 令牌）';
      case 'password': return '账号密码登录（兼容性差，不推荐）';
      case 'cookie': return '邮箱 Cookie 快速登录';
    }
  }
  return value;
}

// 判断字段是否可见（dependsOn 格式: "fieldName=value1|value2"）
function fieldVisible(dependsOn: string, config: Record<string, string>): boolean {
  const [fieldName, values] = dependsOn.split('=');
  const allowed = (values || '').split('|');
  return allowed.includes(config[fieldName] || '');
}

// 构建最终提交的配置：密码字段为空时提交 "******"（编辑时表示未修改，后端保留原值）
function buildFinalConfig(fields: Field[], config: Record<string, string>, editId: string): Record<string, string> {
  const final: Record<string, string> = {};
  for (const field of fields) {
    const val = config[field.name] || '';
    if (field.type === 'password' && val === '') {
      final[field.name] = editId ? '******' : '';
    } else {
      final[field.name] = val;
    }
  }
  return final;
}