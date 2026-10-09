import ReactECharts from 'echarts-for-react'

interface Props {
  config: Record<string, unknown> | null | undefined
  height?: number
}

export default function ChartPanel({ config, height = 320 }: Props) {
  if (!config || typeof config !== 'object' || Array.isArray(config)) return null
  return (
    <ReactECharts
      option={config}
      style={{ height, width: '100%' }}
      opts={{ renderer: 'canvas' }}
    />
  )
}
