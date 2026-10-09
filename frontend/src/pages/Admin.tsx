import { ArrowLeftOutlined, CheckCircleOutlined, ClockCircleOutlined, CloudServerOutlined, DollarOutlined, ReloadOutlined, WarningOutlined } from '@ant-design/icons'
import { Alert, Button, Card, Col, Layout, Progress, Row, Space, Spin, Statistic, Table, Tag, Typography, message } from 'antd'
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { adminApi, getApiErrorMessage } from '../api/client'

interface Overview {
  window_hours: number
  total_runs: number
  completed_runs: number
  failed_runs: number
  success_rate: number
  p95_duration_ms: number
  average_queue_ms: number
  input_tokens: number
  output_tokens: number
  llm_calls: number
  estimated_cost_yuan: number
  queued_jobs: number
  running_jobs: number
  retry_wait_jobs: number
}

interface Failure {
  run_id: string
  error_type: string
  graph_version: string
  duration_ms: number
  started_at: string
}

interface Feedback {
  id: string
  run_id: string
  feedback_type: string
  comment: string
  promoted_to_eval: boolean
  created_at: string
}

interface DashboardData {
  overview: Overview
  recent_failures: Failure[]
  feedback: Feedback[]
}

export default function Admin() {
  const navigate = useNavigate()
  const [data, setData] = useState<DashboardData>()
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const response = await adminApi.metrics(24)
      setData(response.data)
    } catch (requestError) {
      setError(getApiErrorMessage(requestError, '运行指标加载失败'))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => { load() }, [load])

  const promote = async (feedback: Feedback) => {
    try {
      await adminApi.promoteFeedback(feedback.id)
      message.success('已转为候选评测样本')
      await load()
    } catch (requestError) {
      message.error(getApiErrorMessage(requestError, '转评测样本失败'))
    }
  }

  if (loading && !data) return <div className="admin-loading"><Spin size="large" /></div>

  return (
    <Layout className="admin-page">
      <header className="admin-header">
        <Space>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/chat')}>返回分析</Button>
          <div>
            <Typography.Title level={3}>Agent 运行看板</Typography.Title>
            <Typography.Text type="secondary">最近 24 小时 · 自动刷新与评测闭环</Typography.Text>
          </div>
        </Space>
        <Button icon={<ReloadOutlined />} onClick={load}>刷新</Button>
      </header>
      <main className="admin-content">
        {error && <Alert type="error" showIcon message={error} />}
        {data && (
          <>
            <Row gutter={[16, 16]}>
              <Col xs={24} md={8}><Card><Statistic title="运行总数" value={data.overview.total_runs} prefix={<CloudServerOutlined />} /></Card></Col>
              <Col xs={24} md={8}><Card><Statistic title="P95 耗时" value={data.overview.p95_duration_ms / 1000} precision={2} suffix="秒" prefix={<ClockCircleOutlined />} /></Card></Col>
              <Col xs={24} md={8}><Card><Statistic title="估算成本" value={data.overview.estimated_cost_yuan} precision={4} prefix={<DollarOutlined />} suffix="元" /></Card></Col>
              <Col xs={24} md={8}><Card><Statistic title="LLM 调用" value={data.overview.llm_calls} prefix={<CheckCircleOutlined />} /></Card></Col>
              <Col xs={24} md={8}><Card><Statistic title="Token 总量" value={data.overview.input_tokens + data.overview.output_tokens} /></Card></Col>
              <Col xs={24} md={8}><Card><Statistic title="队列任务" value={data.overview.queued_jobs + data.overview.running_jobs + data.overview.retry_wait_jobs} prefix={<WarningOutlined />} /></Card></Col>
            </Row>
            <Card title="成功率" className="admin-section">
              <Progress percent={Number((data.overview.success_rate * 100).toFixed(1))} status={data.overview.success_rate >= 0.9 ? 'success' : 'exception'} />
            </Card>
            <Card title="最近失败" className="admin-section">
              <Table rowKey="run_id" size="small" dataSource={data.recent_failures} pagination={{ pageSize: 10 }} columns={[
                { title: '运行编号', dataIndex: 'run_id', ellipsis: true },
                { title: '错误类型', dataIndex: 'error_type', render: (value) => <Tag color="red">{value}</Tag> },
                { title: '版本', dataIndex: 'graph_version' },
                { title: '耗时', dataIndex: 'duration_ms', render: (value) => `${(value / 1000).toFixed(2)} 秒` },
                { title: '时间', dataIndex: 'started_at', render: (value) => new Date(value).toLocaleString('zh-CN', { hour12: false }) },
              ]} />
            </Card>
            <Card title="用户反馈" className="admin-section">
              <Table rowKey="id" size="small" dataSource={data.feedback} pagination={{ pageSize: 10 }} columns={[
                { title: '反馈', dataIndex: 'feedback_type', render: (value) => <Tag color={value === 'helpful' ? 'green' : 'orange'}>{value}</Tag> },
                { title: '备注', dataIndex: 'comment', ellipsis: true, render: (value) => value || '-' },
                { title: '时间', dataIndex: 'created_at', render: (value) => new Date(value).toLocaleString('zh-CN', { hour12: false }) },
                { title: '评测集', render: (_, item) => item.promoted_to_eval ? <Tag color="blue">候选样本</Tag> : <Button size="small" onClick={() => promote(item)}>转为样本</Button> },
              ]} />
            </Card>
          </>
        )}
      </main>
    </Layout>
  )
}
