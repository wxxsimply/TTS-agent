import React from 'react'
import ReactDOM from 'react-dom/client'
import { ConfigProvider } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      theme={{
        token: {
          colorPrimary: '#1769aa',
          colorSuccess: '#238a63',
          colorWarning: '#c7841d',
          colorError: '#c6534b',
          borderRadius: 6,
          colorText: '#26313d',
          colorBgLayout: '#f4f7f9',
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>,
)
