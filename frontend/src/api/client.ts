import axios, { AxiosError, InternalAxiosRequestConfig } from 'axios'

const API_BASE = import.meta.env.VITE_API_BASE || '/api'

const client = axios.create({ baseURL: API_BASE })
const refreshClient = axios.create({ baseURL: API_BASE })

type RetryableRequest = InternalAxiosRequestConfig & { _retry?: boolean }

const isPublicAuthRequest = (url?: string) =>
  Boolean(url && ['/auth/login', '/auth/register', '/auth/refresh', '/auth/email-code', '/auth/email-login'].some((path) => url.endsWith(path)))

const clearTokens = () => {
  localStorage.removeItem('access_token')
  localStorage.removeItem('refresh_token')
}

client.interceptors.request.use((config) => {
  const token = localStorage.getItem('access_token')
  if (token && !isPublicAuthRequest(config.url)) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

let refreshing: Promise<string> | null = null

client.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config as RetryableRequest | undefined
    const shouldRefresh =
      error.response?.status === 401 &&
      original &&
      !original._retry &&
      !isPublicAuthRequest(original.url)

    if (shouldRefresh) {
      original._retry = true
      const refreshToken = localStorage.getItem('refresh_token')
      if (!refreshToken) {
        clearTokens()
        if (window.location.pathname !== '/login') window.location.replace('/login')
        return Promise.reject(error)
      }
      try {
        if (!refreshing) {
          refreshing = refreshClient
            .post('/auth/refresh', { refresh_token: refreshToken })
            .then((r) => {
              localStorage.setItem('access_token', r.data.access_token)
              localStorage.setItem('refresh_token', r.data.refresh_token)
              return r.data.access_token as string
            })
            .finally(() => {
              refreshing = null
            })
        }
        const newToken = await refreshing
        original.headers.Authorization = `Bearer ${newToken}`
        return client(original)
      } catch (refreshError) {
        clearTokens()
        if (window.location.pathname !== '/login') window.location.replace('/login')
        return Promise.reject(refreshError)
      }
    }
    return Promise.reject(error)
  }
)

export function getApiErrorMessage(error: unknown, fallback = '操作失败，请稍后重试') {
  if (!axios.isAxiosError(error)) return fallback

  const apiError = error as AxiosError<{ detail?: string; message?: string; request_id?: string }>
  const detail = apiError.response?.data?.detail || apiError.response?.data?.message
  const requestId = apiError.response?.data?.request_id || apiError.response?.headers?.['x-request-id']

  if (apiError.response?.status === 401 && detail === 'Invalid credentials') {
    return '账号或密码错误'
  }
  if (detail === 'Email already registered') return '该邮箱已注册，请直接登录'
  if (!apiError.response) return '无法连接服务器，请检查服务是否已启动'
  if (detail && /[\u4e00-\u9fff]/.test(detail)) {
    return requestId && (apiError.response?.status || 0) >= 500 ? `${detail}（请求编号：${requestId}）` : detail
  }

  const statusMessages: Record<number, string> = {
    400: '请求内容不完整，请检查后重试',
    401: '登录状态已失效，请重新登录',
    403: '当前账号没有操作权限',
    404: '请求的数据不存在或已被删除',
    429: '操作过于频繁，请稍后重试',
    500: '服务暂时异常，请稍后重试',
    502: '智能分析服务暂时不可用',
    503: '服务正在恢复，请稍后重试',
  }
  const text = statusMessages[apiError.response.status] || fallback
  return requestId && apiError.response.status >= 500 ? `${text}（请求编号：${requestId}）` : text
}

export default client

export const authApi = {
  register: (email: string, password: string) =>
    client.post('/auth/register', { email, password }),
  login: (email: string, password: string) =>
    client.post('/auth/login', { email, password }),
  sendEmailCode: (email: string) => client.post('/auth/email-code', { email }),
  loginWithEmailCode: (email: string, code: string) =>
    client.post('/auth/email-login', { email, code }),
  me: () => client.get('/auth/me'),
  setPassword: (newPassword: string, currentPassword = '') =>
    client.post('/auth/password', { new_password: newPassword, current_password: currentPassword }),
  logout: () => {
    const refresh = localStorage.getItem('refresh_token')
    if (refresh) client.post('/auth/logout', { refresh_token: refresh })
    localStorage.removeItem('access_token')
    localStorage.removeItem('refresh_token')
  },
}

export const chatApi = {
  listConversations: () => client.get('/conversations'),
  createConversation: (title?: string, datasetId?: string) =>
    client.post('/conversations', { title: title || '新对话', dataset_id: datasetId }),
  listMessages: (id: string) => client.get(`/conversations/${id}/messages`),
  query: (query: string, conversationId?: string, datasetId?: string) =>
    client.post('/chat/query', { query, conversation_id: conversationId, dataset_id: datasetId }),
  exportExcel: (messageId: string) =>
    client.get(`/export/${messageId}/excel`, { responseType: 'blob' }),
}

export const agentRunApi = {
  submit: (query: string, conversationId?: string, datasetId?: string, idempotencyKey?: string) =>
    client.post('/agent-runs', {
      query,
      conversation_id: conversationId,
      dataset_id: datasetId,
    }, {
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
    }),
  get: (runId: string) => client.get(`/agent-runs/${runId}`),
  result: (runId: string) => client.get(`/agent-runs/${runId}/result`),
  cancel: (runId: string) => client.post(`/agent-runs/${runId}/cancel`),
  retry: (runId: string) => client.post(`/agent-runs/${runId}/retry`),
  stats: () => client.get('/agent-jobs/stats'),
  feedback: (runId: string, messageId: string, feedbackType: string, comment = '') =>
    client.post(`/agent-runs/${runId}/feedback`, {
      message_id: messageId,
      feedback_type: feedbackType,
      comment,
    }),
}

export const adminApi = {
  metrics: (hours = 24) => client.get('/admin/agent-metrics', { params: { hours } }),
  promoteFeedback: (feedbackId: string) => client.post(`/admin/feedback/${feedbackId}/promote`),
}

export const datasetApi = {
  list: () => client.get('/datasets'),
  preview: (file: File) => {
    const form = new FormData()
    form.append('file', file)
    return client.post('/datasets/preview', form, { headers: { 'Content-Type': 'multipart/form-data' } })
  },
  upload: (file: File, columnTypes: Record<string, string>) => {
    const form = new FormData()
    form.append('file', file)
    form.append('column_types', JSON.stringify(columnTypes))
    return client.post('/datasets/import', form, { headers: { 'Content-Type': 'multipart/form-data' } })
  },
  remove: (id: string) => client.delete(`/datasets/${id}`),
}
