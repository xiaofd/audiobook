import React from 'react'
import {createRoot} from 'react-dom/client'
import './style.css'
import App from './App'

// 注册 Service Worker，启用 PWA 离线缓存和后台播放
if ('serviceWorker' in navigator) {
  navigator.serviceWorker.register('/sw.js').catch(() => {})
}

const container = document.getElementById('root')

const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <App/>
    </React.StrictMode>
)
