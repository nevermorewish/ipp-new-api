/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts'

import { EmptyState } from '@/components/empty-state'
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import dayjs from '@/lib/dayjs'

import type { ModelAnalyticsBucket, ModelAnalyticsRange } from '../api'

const TOP_MODEL_COUNT = 5
const OTHER_KEY = 'other'
const CHART_COLORS = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
]

interface UsageChartProps {
  items: ModelAnalyticsBucket[]
  range: ModelAnalyticsRange
}

function tsFormat(range: ModelAnalyticsRange) {
  return range === '1d' || range === 'week' ? 'MM-DD HH:mm' : 'HH:mm'
}

export function UsageChart(props: UsageChartProps) {
  const { t } = useTranslation()
  const { items, range } = props

  const { chartData, config, seriesKeys } = useMemo(() => {
    const totalsByModel = new Map<string, number>()
    for (const item of items) {
      totalsByModel.set(
        item.model_name,
        (totalsByModel.get(item.model_name) ?? 0) + item.request_count
      )
    }
    const topModels = [...totalsByModel.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, TOP_MODEL_COUNT)
      .map(([name]) => name)
    const keyByModel = new Map(topModels.map((name, i) => [name, `m${i}`]))

    const seriesKeys = topModels.map((_, i) => `m${i}`)
    const hasOther = totalsByModel.size > topModels.length
    if (hasOther) seriesKeys.push(OTHER_KEY)

    const config: ChartConfig = {}
    topModels.forEach((name, i) => {
      config[`m${i}`] = { label: name, color: CHART_COLORS[i] }
    })
    if (hasOther) {
      config[OTHER_KEY] = {
        label: t('Other'),
        color: 'var(--muted-foreground)',
      }
    }

    const rowByTs = new Map<number, Record<string, number>>()
    for (const item of items) {
      let row = rowByTs.get(item.bucket_ts)
      if (!row) {
        row = { ts: item.bucket_ts }
        rowByTs.set(item.bucket_ts, row)
      }
      const mappedKey = keyByModel.get(item.model_name)
      const key = mappedKey ?? OTHER_KEY
      row[key] = (row[key] ?? 0) + item.request_count
    }

    const chartData = [...rowByTs.values()].sort((a, b) => a.ts - b.ts)

    return { chartData, config, seriesKeys }
  }, [items, t])

  const format = tsFormat(range)

  if (chartData.length === 0) {
    return <EmptyState title={t('No data')} className='h-64 min-h-0' />
  }

  return (
    <ChartContainer config={config} className='h-64 w-full sm:h-80'>
      <BarChart data={chartData} margin={{ left: 4, right: 4 }}>
        <CartesianGrid vertical={false} stroke='#ccc' />
        <XAxis
          dataKey='ts'
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          tickFormatter={(value: number) => dayjs.unix(value).format(format)}
        />
        <YAxis
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          allowDecimals={false}
        />
        <ChartTooltip
          content={
            <ChartTooltipContent
              labelFormatter={(_, payload) => {
                const ts = payload?.[0]?.payload?.ts as number | undefined
                return ts ? dayjs.unix(ts).format('YYYY-MM-DD HH:mm') : ''
              }}
            />
          }
        />
        {seriesKeys.map((key, i) => (
          <Bar
            key={key}
            dataKey={key}
            stackId='usage'
            fill={`var(--color-${key})`}
            stroke='var(--card)'
            strokeWidth={2}
            maxBarSize={24}
            radius={i === seriesKeys.length - 1 ? [2, 2, 0, 0] : 0}
          />
        ))}
        <ChartLegend content={<ChartLegendContent className='flex-wrap' />} />
      </BarChart>
    </ChartContainer>
  )
}
