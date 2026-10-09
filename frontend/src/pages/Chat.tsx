import {
  AppstoreOutlined,
  ArrowRightOutlined,
  BulbOutlined,
  DatabaseOutlined,
  DeleteOutlined,
  FileExcelOutlined,
  LockOutlined,
  LineChartOutlined,
  DashboardOutlined,
  LogoutOutlined,
  PlusOutlined,
  SendOutlined,
  ShoppingOutlined,
  StopOutlined,
  TeamOutlined,
  UploadOutlined,
} from '@ant-design/icons'
import { Alert, Button, Divider, Form, Input, Layout, List, message, Modal, Popconfirm, Select, Spin, Table, Typography, Upload } from 'antd'
import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { agentRunApi, authApi, chatApi, datasetApi, getApiErrorMessage } from '../api/client'
import ReportView, { StructuredReport } from '../components/ReportView'
import { ErrorBoundary } from '../components/ErrorBoundary'
import { createIdempotencyKey } from '../utils/idempotency'

const { Sider, Content } = Layout
const { TextArea } = Input

interface Conversation {
  id: string
  title: string
  domain: string
  updated_at: string
  dataset_id?: string
  data_source_unavailable?: boolean
}

interface Dataset {
  id: string
  name: string
  source_file: string
  sheet_name: string
  columns: { name: string; label: string; type: string }[]
  row_count: number
  created_at: string
}

interface DatasetPreview {
  dataset: Dataset
  sample_rows: string[][]
  warnings?: string[]
}

interface ChatMessage {
  id: string
  role: string
  content: string
  sql?: string
  chart_config?: Record<string, unknown>
  result_preview?: Record<string, unknown>[]
  result_count?: number
  report?: StructuredReport
  created_at: string
  agent_run_id?: string
  isError?: boolean
}

interface ActiveRun {
  id: string
  status: string
  currentNode?: string
}

interface ChangePasswordValues {
  currentPassword: string
  newPassword: string
  confirmPassword: string
}

const runStatusLabels: Record<string, string> = {
  queued: '任务正在排队',
  running: '正在执行分析',
  retry_wait: '等待自动重试',
  interrupted: '正在恢复中断任务',
}

const nodeLabels: Record<string, string> = {
  generate_sql: '正在生成查询',
  validate_sql: '正在校验查询安全性',
  execute_sql: '正在查询数据',
  generate_report: '正在生成分析报告',
  generate_chart: '正在生成图表',
}

const wait = (milliseconds: number) => new Promise((resolve) => window.setTimeout(resolve, milliseconds))

const exampleQuestions = [
  { category: '经营概览', tone: 'blue', icon: <AppstoreOutlined />, question: '上月销售额、订单量和平均客单价分别是多少？' },
  { category: '商品分析', tone: 'green', icon: <ShoppingOutlined />, question: '最近30天销售额最高的10个商品是什么？' },
  { category: '广告分析', tone: 'amber', icon: <LineChartOutlined />, question: '各广告平台的投入、点击和ROI如何？' },
  { category: '客户分析', tone: 'coral', icon: <TeamOutlined />, question: '复购客户占比是多少，主要来自哪些渠道？' },
  { category: '流量分析', tone: 'teal', icon: <BulbOutlined />, question: '各流量来源的访问量和转化率是多少？' },
]

const columnTypeOptions = [
  { value: 'TEXT', label: '文本' },
  { value: 'BIGINT', label: '整数' },
  { value: 'DOUBLE PRECISION', label: '数值/金额' },
  { value: 'TIMESTAMPTZ', label: '日期时间' },
  { value: 'BOOLEAN', label: '是/否' },
]

function downloadName(contentDisposition?: string) {
  const matched = contentDisposition?.match(/filename\*=UTF-8''([^;]+)/i)
  if (matched?.[1]) {
    try {
      return decodeURIComponent(matched[1])
    } catch {
      // Use the Chinese fallback below when a proxy rewrites the header.
    }
  }
  return `数据分析报告_${new Date().toISOString().slice(0, 10)}.xlsx`
}

export default function Chat() {
  const navigate = useNavigate()
  const [conversations, setConversations] = useState<Conversation[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [activeRun, setActiveRun] = useState<ActiveRun | null>(null)
  const [datasets, setDatasets] = useState<Dataset[]>([])
  const [selectedDatasetId, setSelectedDatasetId] = useState<string>()
  const [dataSourceUnavailable, setDataSourceUnavailable] = useState(false)
  const [datasetModalOpen, setDatasetModalOpen] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  const [datasetPreview, setDatasetPreview] = useState<DatasetPreview | null>(null)
  const [pendingUploadFile, setPendingUploadFile] = useState<File | null>(null)
  const [columnTypes, setColumnTypes] = useState<Record<string, string>>({})
  const [passwordModalOpen, setPasswordModalOpen] = useState(false)
  const [passwordSaving, setPasswordSaving] = useState(false)
  const [passwordError, setPasswordError] = useState('')
  const [passwordForm] = Form.useForm<ChangePasswordValues>()
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const init = async () => {
      try {
        await authApi.me()
        const [conversationResponse, datasetResponse] = await Promise.all([chatApi.listConversations(), datasetApi.list()])
        setConversations(Array.isArray(conversationResponse.data) ? conversationResponse.data : [])
        setDatasets(Array.isArray(datasetResponse.data) ? datasetResponse.data : [])
      } catch (error) {
        message.error(getApiErrorMessage(error, '会话加载失败，请重新登录'))
      }
    }
    init()
  }, [])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, loading])

  const loadConversations = async () => {
    try {
      const response = await chatApi.listConversations()
      setConversations(Array.isArray(response.data) ? response.data : [])
    } catch (error) {
      message.error(getApiErrorMessage(error, '会话列表加载失败'))
    }
  }

  const loadDatasets = async () => {
    const response = await datasetApi.list()
    setDatasets(Array.isArray(response.data) ? response.data : [])
  }

  const loadMessages = async (id: string) => {
    try {
      setActiveId(id)
      const conversation = conversations.find((item) => item.id === id)
      setSelectedDatasetId(conversation?.dataset_id)
      setDataSourceUnavailable(Boolean(conversation?.data_source_unavailable))
      const response = await chatApi.listMessages(id)
      setMessages(Array.isArray(response.data) ? response.data : [])
    } catch (error) {
      message.error(getApiErrorMessage(error, '历史消息加载失败'))
    }
  }

  const handleNewChat = async () => {
    try {
      const response = await chatApi.createConversation(undefined, selectedDatasetId)
      await loadConversations()
      setActiveId(response.data.id)
      setMessages([])
      setDataSourceUnavailable(false)
    } catch (error) {
      message.error(getApiErrorMessage(error, '新建会话失败'))
    }
  }

  const handleSend = async (suggestedQuery?: string) => {
    const userQuery = (suggestedQuery ?? query).trim()
    if (!userQuery || loading) return

    setLoading(true)
    setQuery('')
    setMessages((current) => [
      ...current,
      { id: `temp-user-${Date.now()}`, role: 'user', content: userQuery, created_at: new Date().toISOString() },
    ])
    try {
      let conversationId = activeId
      if (!conversationId) {
        const conversationResponse = await chatApi.createConversation(undefined, selectedDatasetId)
        conversationId = conversationResponse.data.id
        setActiveId(conversationId)
        await loadConversations()
      }
      const idempotencyKey = createIdempotencyKey()
      const response = await agentRunApi.submit(userQuery, conversationId || undefined, selectedDatasetId, idempotencyKey)
      const runId = response.data.run_id as string
      setActiveRun({ id: runId, status: response.data.status || 'queued' })

      let terminalStatus = ''
      for (let poll = 0; poll < 600; poll += 1) {
        const runResponse = await agentRunApi.get(runId)
        const status = runResponse.data.status as string
        setActiveRun({ id: runId, status, currentNode: runResponse.data.current_node })
        if (['completed', 'failed', 'cancelled'].includes(status)) {
          terminalStatus = status
          break
        }
        await wait(1000)
      }
      if (!terminalStatus) throw new Error('Agent run polling timed out')

      if (terminalStatus === 'cancelled') {
        setMessages((current) => [
          ...current,
          { id: `cancelled-${runId}`, role: 'assistant', content: '任务已取消。', created_at: new Date().toISOString() },
        ])
      } else if (conversationId) {
        for (let retry = 0; retry < 5; retry += 1) {
          const history = await chatApi.listMessages(conversationId)
          const historyMessages = Array.isArray(history.data) ? history.data : []
          if (historyMessages.some((item: ChatMessage) => item.agent_run_id === runId) || retry === 4) {
            setMessages(historyMessages)
            break
          }
          await wait(300)
        }
      }
    } catch (error) {
      const errorText = getApiErrorMessage(error, '本次分析失败，请稍后重试')
      setMessages((current) => [
        ...current,
        { id: `temp-error-${Date.now()}`, role: 'assistant', content: errorText, created_at: new Date().toISOString(), isError: true },
      ])
    } finally {
      setActiveRun(null)
      setLoading(false)
    }
  }

  const handleCancelRun = async () => {
    if (!activeRun) return
    try {
      await agentRunApi.cancel(activeRun.id)
      setActiveRun((current) => current ? { ...current, status: 'cancelled' } : current)
    } catch (error) {
      message.error(getApiErrorMessage(error, '取消任务失败'))
    }
  }

  const handleExport = async (messageId: string) => {
    try {
      const response = await chatApi.exportExcel(messageId)
      const url = window.URL.createObjectURL(new Blob([response.data]))
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = downloadName(response.headers['content-disposition'])
      anchor.click()
      window.URL.revokeObjectURL(url)
      message.success('Excel 已生成')
    } catch (error) {
      message.error(getApiErrorMessage(error, 'Excel 导出失败'))
    }
  }

  const handleFeedback = async (item: ChatMessage, feedbackType: string) => {
    if (!item.agent_run_id) return
    try {
      await agentRunApi.feedback(item.agent_run_id, item.id, feedbackType)
      message.success(feedbackType === 'helpful' ? '感谢反馈' : '已记录，我们会用于改进评测')
    } catch (error) {
      message.error(getApiErrorMessage(error, '反馈提交失败'))
      throw error
    }
  }

  const handleLogout = () => {
    authApi.logout()
    navigate('/login')
  }

  const closePasswordModal = () => {
    setPasswordModalOpen(false)
    setPasswordError('')
    passwordForm.resetFields()
  }

  const handleChangePassword = async (values: ChangePasswordValues) => {
    setPasswordSaving(true)
    setPasswordError('')
    try {
      await authApi.setPassword(values.newPassword, values.currentPassword)
      closePasswordModal()
      message.success('密码修改成功，下次可以使用新密码登录')
    } catch (error) {
      setPasswordError(getApiErrorMessage(error, '密码修改失败，请稍后重试'))
    } finally {
      setPasswordSaving(false)
    }
  }

  const handleDatasetChange = (value: string) => {
    setSelectedDatasetId(value === 'builtin' ? undefined : value)
    setActiveId(null)
    setMessages([])
    setDataSourceUnavailable(false)
  }

  const resetDatasetImport = () => {
    setDatasetPreview(null)
    setPendingUploadFile(null)
    setColumnTypes({})
    setPreviewing(false)
  }

  const handlePreview = async (file: File) => {
    setPreviewing(true)
    try {
      const response = await datasetApi.preview(file)
      const preview = response.data as DatasetPreview
      setDatasetPreview(preview)
      setPendingUploadFile(file)
      setColumnTypes(Object.fromEntries(preview.dataset.columns.map((column) => [column.name, column.type])))
    } catch (error) {
      message.error(getApiErrorMessage(error, 'Excel 预检失败，请检查文件后重试'))
    } finally {
      setPreviewing(false)
    }
    return Upload.LIST_IGNORE
  }

  const handleUpload = async () => {
    if (!pendingUploadFile || !datasetPreview) return
    setUploading(true)
    try {
      const response = await datasetApi.upload(pendingUploadFile, columnTypes)
      await loadDatasets()
      setSelectedDatasetId(response.data.id)
      setActiveId(null)
      setMessages([])
      setDataSourceUnavailable(false)
      message.success(`已导入 ${response.data.row_count} 行数据`)
      setDatasetModalOpen(false)
      resetDatasetImport()
    } catch (error) {
      message.error(getApiErrorMessage(error, 'Excel 导入失败'))
    } finally {
      setUploading(false)
    }
  }

  const handleDeleteDataset = async (id: string) => {
    try {
      await datasetApi.remove(id)
      if (selectedDatasetId === id) {
        setSelectedDatasetId(undefined)
        setActiveId(null)
        setMessages([])
        setDataSourceUnavailable(false)
      }
      await Promise.all([loadDatasets(), loadConversations()])
      message.success('数据集已删除')
    } catch (error) {
      message.error(getApiErrorMessage(error, '删除数据集失败'))
    }
  }

  return (
    <Layout className="app-shell">
      <Sider width={264} theme="light" breakpoint="lg" collapsedWidth="0" className="conversation-sider">
        <div className="brand-block">
          <span className="brand-symbol"><LineChartOutlined /></span>
          <span className="brand-copy">
            <Typography.Title level={5}>经营分析助手</Typography.Title>
            <Typography.Text type="secondary">跨境电商数据中心</Typography.Text>
          </span>
        </div>
        <div className="new-chat-action">
          <Button type="primary" icon={<PlusOutlined />} block onClick={handleNewChat}>新建分析</Button>
        </div>
        <List
          className="conversation-list"
          header={<span className="sider-section-label">历史分析</span>}
          locale={{ emptyText: '暂无历史分析' }}
          dataSource={conversations}
          renderItem={(item) => (
            <List.Item
              className={activeId === item.id ? 'conversation-item active' : 'conversation-item'}
              onClick={() => loadMessages(item.id)}
            >
              <span className="conversation-marker" />
              <List.Item.Meta
                title={<Typography.Text ellipsis>{item.title}</Typography.Text>}
                description={new Date(item.updated_at).toLocaleString('zh-CN', { hour12: false })}
              />
            </List.Item>
          )}
        />
        <div className="sider-footer">
          <Button icon={<LogoutOutlined />} block onClick={handleLogout}>退出登录</Button>
        </div>
      </Sider>

      <Layout>
        <header className="workspace-header">
          <div className="workspace-title">
            <strong>智能报表</strong>
            <span>电商业务域 · 默认统计时间以问题为准</span>
          </div>
          <div className="data-source-actions">
            <Button icon={<DashboardOutlined />} onClick={() => navigate('/admin')}>运行看板</Button>
            <Button icon={<LockOutlined />} onClick={() => setPasswordModalOpen(true)}>修改密码</Button>
            <div className="source-control">
              <DatabaseOutlined />
              <Select
                aria-label="当前数据源"
                value={selectedDatasetId || 'builtin'}
                onChange={handleDatasetChange}
                options={[
                  { value: 'builtin', label: '示例电商数据' },
                  ...datasets.map((dataset) => ({ value: dataset.id, label: dataset.name })),
                ]}
              />
            </div>
            <Button icon={<UploadOutlined />} onClick={() => setDatasetModalOpen(true)}>导入 Excel</Button>
          </div>
        </header>
        <Content className="workspace-content">
          <div className="message-stream">
            {messages.length === 0 && !loading && (
              <section className="empty-workspace">
                <div className="empty-kicker"><LineChartOutlined /> 数据洞察工作台</div>
                <Typography.Title level={2}>今天想了解哪项经营数据？</Typography.Title>
                <Typography.Paragraph>选择一个常用问题，或在下方直接输入你的业务问题。</Typography.Paragraph>
                <div className="example-grid">
                  {exampleQuestions.map((example) => (
                    <button type="button" key={example.category} className={`example-item tone-${example.tone}`} onClick={() => handleSend(example.question)}>
                      <span className="example-icon">{example.icon}</span>
                      <span className="example-copy">
                        <strong>{example.category}</strong>
                        <small>{example.question}</small>
                      </span>
                      <ArrowRightOutlined className="example-arrow" />
                    </button>
                  ))}
                </div>
              </section>
            )}

            {messages.map((item) => (
              item.role === 'user' ? (
                <div className="user-message" key={item.id}>{item.content}</div>
              ) : item.isError || (!item.report && !item.result_preview?.length) ? (
                <div className={item.isError ? 'assistant-notice error' : 'assistant-notice'} key={item.id}>{item.content}</div>
              ) : (
                <ErrorBoundary
                  key={item.id}
                  fallback={(_error, retry) => (
                    <div className="assistant-notice error">
                      <strong>这条历史报告的格式异常，暂时无法完整展示。</strong>
                      <span>你仍可继续查看其他对话，或重新提交这次分析。</span>
                      <Button size="small" onClick={retry}>重试显示</Button>
                    </div>
                  )}
                >
                  <ReportView
                    report={item.report}
                    fallbackContent={item.content}
                    chartConfig={item.chart_config}
                    rows={item.result_preview}
                    resultCount={item.result_count}
                    sql={item.sql}
                    onExport={() => handleExport(item.id)}
                    onFeedback={item.agent_run_id ? (feedbackType) => handleFeedback(item, feedbackType) : undefined}
                  />
                </ErrorBoundary>
              )
            ))}

            {loading && (
              <div className="analysis-progress">
                <Spin size="small" />
                <div>
                  <strong>{activeRun ? (nodeLabels[activeRun.currentNode || ''] || runStatusLabels[activeRun.status] || '正在处理任务') : '正在提交任务'}</strong>
                  <span>{activeRun ? `运行编号：${activeRun.id}` : '正在创建后台分析任务...'}</span>
                </div>
                {activeRun && !['completed', 'failed', 'cancelled'].includes(activeRun.status) && (
                  <Button size="small" danger icon={<StopOutlined />} onClick={handleCancelRun}>取消</Button>
                )}
              </div>
            )}
            <div ref={bottomRef} />
          </div>

          <div className="query-composer">
            <div className="query-input-wrap">
              <TextArea
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                disabled={dataSourceUnavailable}
                placeholder={dataSourceUnavailable ? '该会话的数据源已删除，请切换数据源后新建分析' : '输入业务问题，例如：最近30天各商品品类的销售额是多少？'}
                autoSize={{ minRows: 1, maxRows: 5 }}
                onPressEnter={(event) => {
                  if (!event.shiftKey) {
                    event.preventDefault()
                    handleSend()
                  }
                }}
              />
              <Button type="primary" icon={<SendOutlined />} loading={loading} disabled={dataSourceUnavailable} onClick={() => handleSend()} aria-label="发送问题" />
            </div>
          </div>
        </Content>
      </Layout>

      <Modal
        title="修改登录密码"
        open={passwordModalOpen}
        onCancel={closePasswordModal}
        footer={null}
        destroyOnHidden
      >
        <Form<ChangePasswordValues> form={passwordForm} layout="vertical" requiredMark={false} onFinish={handleChangePassword}>
          <Form.Item name="currentPassword" label="当前密码" rules={[{ required: true, message: '请输入当前密码' }]}> 
            <Input.Password prefix={<LockOutlined />} autoComplete="current-password" placeholder="请输入当前密码" />
          </Form.Item>
          <Form.Item
            name="newPassword"
            label="新密码"
            dependencies={['currentPassword']}
            rules={[
              { required: true, min: 6, max: 72, message: '新密码需要 6 至 72 位' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  return !value || value !== getFieldValue('currentPassword')
                    ? Promise.resolve()
                    : Promise.reject(new Error('新密码不能与当前密码相同'))
                },
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} autoComplete="new-password" placeholder="请输入新密码" />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label="确认新密码"
            dependencies={['newPassword']}
            rules={[
              { required: true, message: '请再次输入新密码' },
              ({ getFieldValue }) => ({
                validator(_, value) {
                  return !value || value === getFieldValue('newPassword')
                    ? Promise.resolve()
                    : Promise.reject(new Error('两次输入的新密码不一致'))
                },
              }),
            ]}
          >
            <Input.Password prefix={<LockOutlined />} autoComplete="new-password" placeholder="请再次输入新密码" />
          </Form.Item>
          {passwordError && <Alert type="error" message={passwordError} showIcon style={{ marginBottom: 16 }} />}
          <Button type="primary" htmlType="submit" block loading={passwordSaving}>确认修改密码</Button>
        </Form>
      </Modal>

      <Modal title="导入 Excel 数据" open={datasetModalOpen} onCancel={() => { setDatasetModalOpen(false); resetDatasetImport() }} footer={null} width={760}>
        {!datasetPreview ? (
          <Upload.Dragger
            accept=".xlsx"
            showUploadList={false}
            disabled={previewing || uploading}
            beforeUpload={(file) => handlePreview(file as File)}
          >
            <p className="ant-upload-drag-icon"><FileExcelOutlined /></p>
            <p className="ant-upload-text">点击或拖入 Excel 文件</p>
            <p className="ant-upload-hint">先预检字段和前 10 行数据，再确认导入。支持 .xlsx，首行为表头，最多 10MB、50,000 行</p>
          </Upload.Dragger>
        ) : (
          <section className="dataset-preview">
            <Alert
              type="info"
              showIcon
              message={`已读取 ${datasetPreview.dataset.name}：${datasetPreview.dataset.row_count.toLocaleString('zh-CN')} 行、${datasetPreview.dataset.columns.length} 列`}
              description="请确认字段类型。订单号、手机号等带前导零的编号建议设为“文本”。"
            />
            {datasetPreview.warnings?.map((warning) => <Alert key={warning} className="dataset-warning" type="warning" showIcon message={warning} />)}
            <Divider orientation="left">字段类型</Divider>
            <Table
              size="small"
              pagination={false}
              rowKey="name"
              dataSource={datasetPreview.dataset.columns}
              columns={[
                { title: '字段名称', dataIndex: 'label', key: 'label' },
                { title: '识别类型', key: 'type', render: (_, column: Dataset['columns'][number]) => (
                  <Select
                    value={columnTypes[column.name]}
                    options={columnTypeOptions}
                    onChange={(value) => setColumnTypes((current) => ({ ...current, [column.name]: value }))}
                    style={{ width: 140 }}
                  />
                ) },
              ]}
            />
            <Divider orientation="left">数据预览（前 {datasetPreview.sample_rows.length} 行）</Divider>
            <Table
              size="small"
              pagination={false}
              scroll={{ x: true }}
              rowKey="key"
              dataSource={datasetPreview.sample_rows.map((row, index) => ({
                key: String(index),
                ...Object.fromEntries(datasetPreview.dataset.columns.map((column, columnIndex) => [column.name, row[columnIndex] || '']))
              }))}
              columns={datasetPreview.dataset.columns.map((column) => ({ title: column.label, dataIndex: column.name, key: column.name, ellipsis: true }))}
            />
            <div className="dataset-preview-actions">
              <Button onClick={resetDatasetImport}>重新选择文件</Button>
              <Button type="primary" loading={uploading} onClick={handleUpload}>确认导入</Button>
            </div>
          </section>
        )}
        <List
          className="dataset-list"
          header={<strong>已导入的数据</strong>}
          locale={{ emptyText: '尚未导入数据' }}
          dataSource={datasets}
          renderItem={(dataset) => (
            <List.Item actions={[
              <Button key="use" type="link" onClick={() => { handleDatasetChange(dataset.id); setDatasetModalOpen(false) }}>使用</Button>,
              <Popconfirm key="delete" title="删除这份数据及相关分析绑定？" onConfirm={() => handleDeleteDataset(dataset.id)}>
                <Button type="text" danger icon={<DeleteOutlined />} aria-label={`删除 ${dataset.name}`} />
              </Popconfirm>,
            ]}>
              <List.Item.Meta
                avatar={<FileExcelOutlined className="dataset-icon" />}
                title={dataset.name}
                description={`${dataset.sheet_name} · ${dataset.row_count.toLocaleString('zh-CN')} 行 · ${dataset.columns.length} 列`}
              />
            </List.Item>
          )}
        />
      </Modal>
    </Layout>
  )
}
