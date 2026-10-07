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
import { useQuery } from '@tanstack/react-query'
import { Activity, AlertCircle, Timer, TriangleAlert } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { formatNumber } from '@/lib/format'

import { getModelAnalytics, type ModelAnalyticsRange } from './api'
import { UsageChart } from './components/usage-chart'

const SLOW_TTFT_MS = 20000

const RANGE_OPTIONS: { value: ModelAnalyticsRange; labelKey: string }[] = [
  { value: '1h', labelKey: 'Last 1 hour' },
  { value: '2h', labelKey: 'Last 2 hours' },
  { value: '4h', labelKey: 'Last 4 hours' },
  { value: '1d', labelKey: 'Last 1 day' },
  { value: 'week', labelKey: 'This week' },
]

function formatMs(value: number) {
  if (value >= 1000) return `${(value / 1000).toFixed(1)}s`
  return `${value}ms`
}

function StatCard({
  title,
  value,
  icon: Icon,
  description,
  variant = 'default',
}: {
  title: string
  value: string
  icon: typeof Activity
  description?: string
  variant?: 'default' | 'destructive'
}) {
  return (
    <Card className={variant === 'destructive' ? 'border-destructive/40' : ''}>
      <CardHeader className='flex flex-row items-center justify-between pb-2'>
        <CardTitle className='text-sm font-medium'>{title}</CardTitle>
        <Icon
          className={
            variant === 'destructive'
              ? 'text-destructive size-4'
              : 'text-muted-foreground size-4'
          }
        />
      </CardHeader>
      <CardContent>
        <div
          className={
            variant === 'destructive'
              ? 'text-destructive text-2xl font-bold'
              : 'text-2xl font-bold'
          }
        >
          {value}
        </div>
        {description && (
          <p className='text-muted-foreground mt-1 text-xs'>{description}</p>
        )}
      </CardContent>
    </Card>
  )
}

function TtftCell(props: {
  row: {
    avgTtftMs: number
    isSlow: boolean
    maxTtftMs: number
  }
}) {
  const { t } = useTranslation()
  const row = props.row
  if (row.avgTtftMs <= 0) {
    return <span className='text-muted-foreground'>-</span>
  }
  if (!row.isSlow) {
    return <span className='font-mono'>{formatMs(row.avgTtftMs)}</span>
  }
  return (
    <TooltipProvider>
      <Tooltip>
        <TooltipTrigger
          aria-label={t(
            'First-token latency exceeded 20s (peak {{peak}}) in this range',
            { peak: formatMs(row.maxTtftMs) }
          )}
          render={
            <Badge
              render={<button type='button' />}
              variant='destructive'
              className='font-mono'
            />
          }
        >
          <TriangleAlert data-icon='inline-start' />
          {formatMs(row.avgTtftMs)}
        </TooltipTrigger>
        <TooltipContent>
          {t('First-token latency exceeded 20s (peak {{peak}}) in this range', {
            peak: formatMs(row.maxTtftMs),
          })}
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  )
}

export function ModelAnalytics() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [range, setRange] = useState<ModelAnalyticsRange>('2h')
  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['model-analytics', range],
    queryFn: () => getModelAnalytics(range),
    refetchInterval: 60000,
  })
  const items = useMemo(() => data?.items ?? [], [data])

  const { totals, modelRows, slowBucketCount } = useMemo(() => {
    let totalRequests = 0
    let totalErrors = 0
    let ttftWeightedSum = 0
    let ttftWeight = 0
    let slowBucketCount = 0

    const byModel = new Map<
      string,
      {
        requests: number
        errors: number
        ttftWeightedSum: number
        ttftWeight: number
        maxTtftMs: number
        slowBuckets: number
      }
    >()

    for (const item of items) {
      totalRequests += item.request_count
      totalErrors += item.error_count
      if (item.has_ttft) {
        const ttftCount = item.ttft_count ?? item.request_count
        ttftWeightedSum += item.avg_ttft_ms * ttftCount
        ttftWeight += ttftCount
        if (item.avg_ttft_ms > SLOW_TTFT_MS) slowBucketCount += 1
      }

      let row = byModel.get(item.model_name)
      if (!row) {
        row = {
          requests: 0,
          errors: 0,
          ttftWeightedSum: 0,
          ttftWeight: 0,
          maxTtftMs: 0,
          slowBuckets: 0,
        }
        byModel.set(item.model_name, row)
      }
      row.requests += item.request_count
      row.errors += item.error_count
      if (item.has_ttft) {
        const ttftCount = item.ttft_count ?? item.request_count
        row.ttftWeightedSum += item.avg_ttft_ms * ttftCount
        row.ttftWeight += ttftCount
        row.maxTtftMs = Math.max(row.maxTtftMs, item.avg_ttft_ms)
        if (item.avg_ttft_ms > SLOW_TTFT_MS) row.slowBuckets += 1
      }
    }

    let modelRows = [...byModel.entries()]
      .map(([modelName, row]) => ({
        modelName,
        requests: row.requests,
        errors: row.errors,
        errorRate: row.requests > 0 ? (row.errors / row.requests) * 100 : 0,
        avgTtftMs:
          row.ttftWeight > 0 ? row.ttftWeightedSum / row.ttftWeight : 0,
        p50TtftMs: 0,
        p90TtftMs: 0,
        maxTtftMs: row.maxTtftMs,
        isSlow: row.slowBuckets > 0,
      }))
      .sort((a, b) => b.requests - a.requests)

    const summaries = data?.model_summaries ?? []
    if (summaries.length > 0) {
      const summaryByModel = new Map(
        summaries.map((summary) => [summary.model_name, summary])
      )
      modelRows = modelRows.map((row) => {
        const summary = summaryByModel.get(row.modelName)
        if (!summary) return row
        return {
          ...row,
          requests: summary.requests,
          errors: summary.errors,
          errorRate:
            summary.requests > 0
              ? (summary.errors / summary.requests) * 100
              : 0,
          avgTtftMs: summary.avg_ttft_ms,
          p50TtftMs: summary.ttft_p50_ms,
          p90TtftMs: summary.ttft_p90_ms,
          maxTtftMs: summary.max_ttft_ms || row.maxTtftMs,
          isSlow: summary.max_ttft_ms > SLOW_TTFT_MS || row.isSlow,
        }
      })
    }

    return {
      totals: {
        requests: totalRequests,
        errorRate: totalRequests > 0 ? (totalErrors / totalRequests) * 100 : 0,
        avgTtftMs: ttftWeight > 0 ? ttftWeightedSum / ttftWeight : 0,
      },
      modelRows,
      slowBucketCount,
    }
  }, [data?.model_summaries, items])

  let usageContent
  if (isLoading) {
    usageContent = <LoadingState />
  } else if (isError) {
    usageContent = (
      <ErrorState title={t('Request failed')} onRetry={() => void refetch()} />
    )
  } else {
    usageContent = <UsageChart items={items} range={range} />
  }

  let summaryBody
  if (isLoading) {
    summaryBody = (
      <TableRow>
        <TableCell
          colSpan={7}
          className='text-muted-foreground h-32 text-center'
        >
          {t('Loading...')}
        </TableCell>
      </TableRow>
    )
  } else if (isError) {
    summaryBody = (
      <TableRow>
        <TableCell colSpan={7} className='text-destructive h-32 text-center'>
          {t('Request failed')}
        </TableCell>
      </TableRow>
    )
  } else if (modelRows.length === 0) {
    summaryBody = (
      <TableRow>
        <TableCell
          colSpan={7}
          className='text-muted-foreground h-32 text-center'
        >
          {t('No data')}
        </TableCell>
      </TableRow>
    )
  } else {
    summaryBody = modelRows.map((row) => (
      <TableRow key={row.modelName}>
        <TableCell
          title={row.modelName}
          className='max-w-[280px] min-w-[180px] truncate font-medium'
        >
          {row.modelName || '-'}
        </TableCell>
        <TableCell className='text-right font-mono'>
          {formatNumber(row.requests, locale)}
        </TableCell>
        <TableCell className='text-right font-mono'>
          {formatNumber(row.errors, locale)}
        </TableCell>
        <TableCell className='text-right font-mono'>
          {row.errorRate.toFixed(2)}%
        </TableCell>
        <TableCell className='text-right'>
          <TtftCell row={row} />
        </TableCell>
        <TableCell className='text-right font-mono whitespace-nowrap'>
          {row.p50TtftMs > 0 ? formatMs(row.p50TtftMs) : '-'}
        </TableCell>
        <TableCell className='text-right font-mono whitespace-nowrap'>
          {row.p90TtftMs > 0 ? formatMs(row.p90TtftMs) : '-'}
        </TableCell>
      </TableRow>
    ))
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Model Analytics')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <p className='text-muted-foreground mb-4 text-sm'>
          {t('View model call count analytics and charts')}
        </p>
        <Tabs
          className='max-w-full overflow-x-auto'
          value={range}
          onValueChange={(value) => setRange(value as ModelAnalyticsRange)}
        >
          <TabsList>
            {RANGE_OPTIONS.map((option) => (
              <TabsTrigger key={option.value} value={option.value}>
                {t(option.labelKey)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

        <div className='mt-4 grid gap-4 md:grid-cols-2 xl:grid-cols-4'>
          <StatCard
            title={t('Total Requests')}
            value={formatNumber(totals.requests, locale)}
            icon={Activity}
          />
          <StatCard
            title={t('Error Rate')}
            value={`${totals.errorRate.toFixed(2)}%`}
            icon={AlertCircle}
          />
          <StatCard
            title={t('Avg First-Token Latency')}
            value={totals.avgTtftMs > 0 ? formatMs(totals.avgTtftMs) : '-'}
            icon={Timer}
          />
          <StatCard
            title={t('Slow First-Token (>20s)')}
            value={formatNumber(slowBucketCount, locale)}
            icon={TriangleAlert}
            variant={slowBucketCount > 0 ? 'destructive' : 'default'}
          />
        </div>

        <Card className='mt-4'>
          <CardHeader>
            <CardTitle>{t('Usage over time')}</CardTitle>
          </CardHeader>
          <CardContent>{usageContent}</CardContent>
        </Card>

        <Card className='mt-4'>
          <CardHeader>
            <CardTitle>{t('Model summary')}</CardTitle>
          </CardHeader>
          <CardContent className='min-w-0'>
            <div className='w-full overflow-x-auto'>
              <Table className='min-w-[900px]'>
                <TableHeader>
                  <TableRow>
                    <TableHead className='min-w-[180px] whitespace-nowrap'>
                      {t('Model')}
                    </TableHead>
                    <TableHead className='text-right whitespace-nowrap'>
                      {t('Requests')}
                    </TableHead>
                    <TableHead className='text-right whitespace-nowrap'>
                      {t('Errors')}
                    </TableHead>
                    <TableHead className='text-right whitespace-nowrap'>
                      {t('Error Rate')}
                    </TableHead>
                    <TableHead className='text-right whitespace-nowrap'>
                      {t('TTFT Average')}
                    </TableHead>
                    <TableHead className='text-right whitespace-nowrap'>
                      {t('TTFT P50')}
                    </TableHead>
                    <TableHead className='text-right whitespace-nowrap'>
                      {t('TTFT P90')}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>{summaryBody}</TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
        {data && (
          <p className='text-muted-foreground mt-2 text-xs'>
            {t('Updated as of {{time}}, each bar represents {{seconds}}s', {
              time: dayjs.unix(data.end).format('YYYY-MM-DD HH:mm:ss'),
              seconds: data.bucket_seconds,
            })}
          </p>
        )}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
