import {
  BarChartOutlined,
  CodeOutlined,
  DatabaseOutlined,
  DownloadOutlined,
  LineChartOutlined,
  LikeOutlined,
  DislikeOutlined,
} from '@ant-design/icons'
import { Button, Collapse, Empty, Table, Typography } from 'antd'
import { lazy, ReactNode, Suspense, useState } from 'react'

const ChartPanel = lazy(() => import('./ChartPanel'))

export interface ReportMetric {
  label: string
  value: unknown
  display_value: string
  unit?: string
}

export interface StructuredReport {
  title: string
  conclusion: string
  markdown?: string
  metrics: ReportMetric[]
  insights: string[]
  data_scope: {
    time_range?: string
    row_count: number
  }
  caveats: string[]
  column_labels: Record<string, string>
  column_formats: Record<string, string>
  sensitive_columns?: Record<string, boolean>
}

interface Props {
  report?: StructuredReport
  fallbackContent: string
  chartConfig?: Record<string, unknown>
  rows?: Record<string, unknown>[]
  resultCount?: number
  sql?: string
  onExport: () => void
  onFeedback?: (feedbackType: string) => Promise<void>
}

function maskSensitive(value: unknown) {
  const text = String(value ?? '')
  if (text.length <= 4) return '****'
  if (text.length <= 8) return `${text.slice(0, 2)}****`
  return `${text.slice(0, 3)}****${text.slice(-3)}`
}

function displayText(value: unknown, fallback = '-') {
  if (value === null || value === undefined || value === '') return fallback
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean' || typeof value === 'bigint') {
    return String(value)
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function formatCell(value: unknown, format?: string) {
  if (value === null || value === undefined || value === '') return '-'
  if (format === 'boolean') return value ? '是' : '否'
  const numeric = typeof value === 'number' ? value : Number(value)
  if (!Number.isNaN(numeric)) {
    if (format === 'percent') {
      const percent = Math.abs(numeric) <= 1 ? numeric * 100 : numeric
      return `${percent.toFixed(2)}%`
    }
    if (format === 'currency') return numeric.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    if (format === 'integer') return numeric.toLocaleString('zh-CN', { maximumFractionDigits: 0 })
  }
  if (format === 'date' || format === 'datetime' || format === 'month') {
    const date = new Date(String(value))
    if (!Number.isNaN(date.getTime())) {
      return format === 'datetime'
        ? date.toLocaleString('zh-CN', { hour12: false })
        : date.toLocaleDateString('zh-CN')
    }
  }
  return displayText(value)
}

function isInternalColumn(key: string) {
  const normalized = key.trim().toLowerCase()
  return normalized === 'owner_id' || normalized === '_owner_id' || normalized === 'row_number' || normalized === '_row_number'
}

function hasRepeatedTimeValues(rows: Record<string, unknown>[], labels: Record<string, string>) {
  if (rows.length < 2) return false
  const keys = Object.keys(rows[0])
  const timeKey = keys.find((key) => {
    const label = String(labels[key] || key)
    return /日期|时间|date|time/i.test(label)
  })
  if (!timeKey) return false
  const values = rows.map((row) => String(row[timeKey] ?? '')).filter(Boolean)
  return values.length > 1 && new Set(values).size < values.length
}

function parseLegacyJson(content: string) {
  const cleaned = content
    .trim()
    .replace(/^```json\s*/i, '')
    .replace(/^```\s*/, '')
    .replace(/```$/, '')
    .trim()
  const start = cleaned.indexOf('{')
  const end = cleaned.lastIndexOf('}')
  if (start < 0 || end <= start) return null
  try {
    const parsed = JSON.parse(cleaned.slice(start, end + 1)) as {
      conclusion?: string
      insights?: string[]
      caveats?: string[]
    }
    return parsed.conclusion ? parsed : null
  } catch {
    return null
  }
}

function buildMarkdown(conclusion: string, insights: string[] = [], caveats: string[] = []) {
  const lines = ['## 结论', '', conclusion]
  if (insights.length) {
    lines.push('', '## 关键发现', '', ...insights.map((item) => `- ${item}`))
  }
  if (caveats.length) {
    lines.push('', '## 数据说明', '', ...caveats.map((item) => `- ${item}`))
  }
  return lines.join('\n')
}

function readableMarkdown(report: StructuredReport | undefined, fallbackContent: string) {
  if (typeof report?.markdown === 'string' && report.markdown.trim()) return report.markdown.trim()

  const source = displayText(report?.conclusion || fallbackContent, '分析已完成。')
  const legacy = parseLegacyJson(source)
  if (legacy) {
    return buildMarkdown(legacy.conclusion || '分析已完成。', legacy.insights, legacy.caveats)
  }
  if (/^\s*[\[{]/.test(source)) {
    return buildMarkdown('分析已完成，请查看下方核心指标和数据明细。')
  }
  return buildMarkdown(source || '分析已完成。', report?.insights, report?.caveats)
}

function cleanInlineMarkdown(value: string) {
  return value.replace(/\*\*(.*?)\*\*/g, '$1').replace(/`([^`]+)`/g, '$1')
}

function MarkdownReport({ content }: { content: string }) {
  const nodes: ReactNode[] = []
  let list: string[] = []
  const flushList = () => {
    if (!list.length) return
    const items = list
    list = []
    nodes.push(<ul key={`list-${nodes.length}`}>{items.map((item, index) => <li key={index}>{cleanInlineMarkdown(item)}</li>)}</ul>)
  }

  content.split(/\r?\n/).forEach((rawLine) => {
    const line = rawLine.trim()
    if (!line) {
      flushList()
      return
    }
    const heading = line.match(/^#{1,3}\s+(.+)$/)
    if (heading) {
      flushList()
      nodes.push(<h3 key={`heading-${nodes.length}`}>{cleanInlineMarkdown(heading[1])}</h3>)
      return
    }
    const item = line.match(/^[-*+]\s+(.+)$/)
    if (item) {
      list.push(item[1])
      return
    }
    flushList()
    nodes.push(<p key={`paragraph-${nodes.length}`}>{cleanInlineMarkdown(line)}</p>)
  })
  flushList()
  return <div className="markdown-report">{nodes}</div>
}

export default function ReportView({ report, fallbackContent, chartConfig, rows = [], resultCount, sql, onExport, onFeedback }: Props) {
  const [feedback, setFeedback] = useState<string>()
  const safeRows = Array.isArray(rows)
    ? rows
      .filter((row): row is Record<string, unknown> => Boolean(row) && typeof row === 'object' && !Array.isArray(row))
      .map((row) => Object.fromEntries(Object.entries(row).filter(([key]) => !isInternalColumn(key))))
    : []
  const labels = report?.column_labels && typeof report.column_labels === 'object' ? report.column_labels : {}
  const formats = report?.column_formats && typeof report.column_formats === 'object' ? report.column_formats : {}
  const sensitive = report?.sensitive_columns && typeof report.sensitive_columns === 'object' ? report.sensitive_columns : {}
  const total = resultCount ?? report?.data_scope?.row_count ?? safeRows.length
  const metrics = Array.isArray(report?.metrics) ? report.metrics : []
  const columns = safeRows.length
    ? Object.keys(safeRows[0]).map((key) => ({
        title: displayText(labels[key] || key),
        dataIndex: key,
        ellipsis: true,
        render: (value: unknown) => sensitive[key] ? maskSensitive(value) : formatCell(value, formats[key]),
      }))
    : []

  return (
    <article className="report-view">
      <header className="report-header">
        <div className="report-kicker"><BarChartOutlined /> 数据分析报告</div>
        <Typography.Title level={4}>{displayText(report?.title, '查询结果')}</Typography.Title>
        <MarkdownReport content={readableMarkdown(report, fallbackContent)} />
      </header>

      {metrics.length > 0 && (
        <section className="metric-grid" aria-label="核心指标">
          {metrics.map((metric, index) => (
            <div className="metric-item" key={`${displayText(metric?.label, '指标')}-${index}`}>
              <span>{displayText(metric?.label, '指标')}</span>
              <strong>{displayText(metric?.display_value ?? metric?.value)}</strong>
            </div>
          ))}
        </section>
      )}

      {chartConfig && !hasRepeatedTimeValues(safeRows, labels) && (
        <section className="report-section">
          <h3><LineChartOutlined /> 趋势与对比</h3>
          <Suspense fallback={<div className="chart-loading">图表加载中...</div>}>
            <ChartPanel config={chartConfig} />
          </Suspense>
        </section>
      )}

      <section className="report-section">
        <div className="section-heading">
          <h3><DatabaseOutlined /> 数据明细</h3>
          {safeRows.length > 0 && (
            <Button icon={<DownloadOutlined />} onClick={onExport}>导出完整 Excel</Button>
          )}
        </div>
        {safeRows.length > 0 ? (
          <>
            <Table
              size="small"
              dataSource={safeRows.map((row, index) => ({ ...row, key: index }))}
              columns={columns}
              pagination={{ pageSize: 10, showSizeChanger: false }}
              scroll={{ x: true }}
            />
            <Typography.Text type="secondary">当前预览 {safeRows.length} 条，共 {displayText(total, '0')} 条；Excel 将导出完整结果。</Typography.Text>
          </>
        ) : (
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="当前条件下没有数据" />
        )}
      </section>

      {report?.data_scope?.time_range && (
        <section className="report-note">
          <strong>统计范围</strong>
          <span>{displayText(report.data_scope.time_range)}</span>
        </section>
      )}

      {sql && (
        <Collapse
          ghost
          className="technical-details"
          items={[{
            key: 'sql',
            label: <span><CodeOutlined /> 技术详情</span>,
            children: <pre>{sql}</pre>,
          }]}
        />
      )}

      {onFeedback && (
        <div className="report-feedback">
          <Typography.Text type="secondary">这份分析有帮助吗？</Typography.Text>
          <Button
            size="small"
            type={feedback === 'helpful' ? 'primary' : 'text'}
            icon={<LikeOutlined />}
            onClick={async () => { await onFeedback('helpful'); setFeedback('helpful') }}
          >有帮助</Button>
          <Button
            size="small"
            danger={feedback === 'not_helpful'}
            type={feedback === 'not_helpful' ? 'primary' : 'text'}
            icon={<DislikeOutlined />}
            onClick={async () => { await onFeedback('not_helpful'); setFeedback('not_helpful') }}
          >需改进</Button>
        </div>
      )}
    </article>
  )
}
