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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18n from 'i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'

import { ModelAnalytics } from '..'
import type { ModelAnalyticsResponse } from '../api'
import { UsageChart } from '../components/usage-chart'

let client: QueryClient

const data: ModelAnalyticsResponse = {
  start: 1791270000,
  end: 1791277200,
  range: '2h',
  bucket_seconds: 3600,
  items: [
    {
      model_name: 'alpha',
      bucket_ts: 1791270000,
      request_count: 1000,
      error_count: 1,
      avg_latency_ms: 2000,
      avg_ttft_ms: 1000,
      ttft_count: 1,
      has_ttft: true,
    },
    {
      model_name: 'alpha',
      bucket_ts: 1791273600,
      request_count: 2,
      error_count: 0,
      avg_latency_ms: 3000,
      avg_ttft_ms: 3000,
      ttft_count: 2,
      has_ttft: true,
    },
    {
      model_name: 'beta',
      bucket_ts: 1791273600,
      request_count: 1,
      error_count: 1,
      avg_latency_ms: 1000,
      avg_ttft_ms: 0,
      ttft_count: 0,
      has_ttft: false,
    },
  ],
}

beforeEach(async () => {
  const getRect = HTMLElement.prototype.getBoundingClientRect
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      if (this.classList.contains('recharts-responsive-container')) {
        return new DOMRect(0, 0, 800, 320)
      }
      return getRect.call(this)
    }
  )
  for (const [language, resource] of Object.entries({
    zhCN: zh,
    zhTW,
    fr,
    ja,
    ru,
    vi: viLocale,
  })) {
    i18n.addResourceBundle(language, 'translation', resource.translation)
  }
  await i18n.changeLanguage('en')
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
})

afterEach(async () => {
  cleanup()
  client.clear()
  await i18n.changeLanguage('en')
})

function renderPage() {
  return render(
    <QueryClientProvider client={client}>
      <ModelAnalytics />
    </QueryClientProvider>
  )
}

it('weights TTFT by measured requests and orders the model summary by request volume', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data } })
  renderPage()
  const table = screen.getByRole('table')
  await within(table).findByText('alpha')
  const rows = within(table).getAllByRole('row')
  expect(rows[1]).toHaveTextContent('alpha')
  expect(rows[1]).toHaveTextContent('1,002')
  expect(rows[1]).toHaveTextContent('2.3s')
  expect(rows[2]).toHaveTextContent('beta')
  expect(
    within(rows[2])
      .getAllByRole('cell')
      .slice(4)
      .map((cell) => cell.textContent)
  ).toEqual(['-', '-', '-'])
})

it('shows server TTFT percentiles and exposes slow latency details from the focusable trigger', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...data,
        model_summaries: [
          {
            model_name: 'alpha',
            requests: 1002,
            errors: 1,
            avg_ttft_ms: 5000,
            ttft_p50_ms: 1000,
            ttft_p90_ms: 25000,
            max_ttft_ms: 30000,
            ttft_count: 3,
          },
        ],
      },
    },
  })
  renderPage()
  const table = screen.getByRole('table')
  const button = await within(table).findByRole('button', {
    name: 'First-token latency exceeded 20s (peak 30.0s) in this range',
  })
  expect(within(table).getByText('25.0s')).toBeVisible()
  expect(button).toHaveAttribute('type', 'button')
  expect(button).toHaveAttribute(
    'aria-label',
    'First-token latency exceeded 20s (peak 30.0s) in this range'
  )
})

it('changes the request range using the period tabs and retains accessible selection', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data } })
  renderPage()
  await within(screen.getByRole('table')).findByText('alpha')
  expect(screen.getByRole('tab', { name: 'Last 2 hours' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await userEvent.click(screen.getByRole('tab', { name: 'This week' }))
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/admin/model-analytics', {
      params: { range: 'week' },
    })
  )
  expect(screen.getByRole('tab', { name: 'This week' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
})

it('shows empty states in both the chart and summary when no requests are available', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: { ...data, items: [] } },
  })
  renderPage()
  await waitFor(() => expect(screen.getAllByText('No data')).toHaveLength(2))
  expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
})

it('shows loading, rejects a business failure, and lets the user retry successfully', async () => {
  let resolve: (value: unknown) => void = () => {}
  const pending = new Promise((done) => {
    resolve = done
  })
  const get = vi
    .spyOn(api, 'get')
    .mockImplementationOnce(() => pending as ReturnType<typeof api.get>)
    .mockResolvedValue({ data: { success: true, data } })
  renderPage()
  expect(screen.getAllByText('Loading...')).toHaveLength(2)
  await act(async () =>
    resolve({ data: { success: false, message: 'database unavailable' } })
  )
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.getAllByText('Request failed')).toHaveLength(2)
  expect(screen.queryByText('No data')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await within(screen.getByRole('table')).findByText('alpha')
  ).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
})

it.each(['zhCN', 'zhTW', 'en', 'fr', 'ja', 'ru', 'vi', 'invalid-locale'])(
  'formats request counts after switching the interface language to %s',
  async (language) => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data } })
    renderPage()
    await within(screen.getByRole('table')).findByText('alpha')
    await act(async () => {
      await i18n.changeLanguage(language)
    })
    const row = within(screen.getByRole('table')).getAllByRole('row')[1]
    expect(within(row).getAllByRole('cell')[1].textContent).toBe(
      new Intl.NumberFormat(toIntlLocale(language)).format(1002)
    )
  }
)

it('keeps the summary scrollable and retains the full name of a long model', async () => {
  const name = 'long-model-name-'.repeat(12)
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: { ...data, items: [{ ...data.items[0], model_name: name }] },
    },
  })
  renderPage()
  const table = screen.getByRole('table')
  expect(await within(table).findByTitle(name)).toHaveClass('truncate')
  expect(table).toHaveClass('min-w-[900px]')
  expect(table.parentElement).toHaveClass('overflow-x-auto')
  expect(screen.getByRole('tablist').parentElement).toHaveClass(
    'overflow-x-auto'
  )
})

it('groups models outside the top five under Other in the chart legend', async () => {
  render(
    <UsageChart
      range='2h'
      items={['a', 'b', 'c', 'd', 'e', 'f'].map((model_name, index) => ({
        ...data.items[0],
        model_name,
        request_count: 6 - index,
      }))}
    />
  )
  expect(await screen.findByText('Other')).toBeVisible()
  expect(screen.queryByText('f')).not.toBeInTheDocument()
  expect(screen.getByText('a')).toBeVisible()
})
