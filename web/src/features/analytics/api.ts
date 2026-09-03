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
import { api, type ApiRequestConfig } from '@/lib/api'

export type AnalyticsPeriod =
  | 'today'
  | 'yesterday'
  | '7d'
  | 'month'
  | 'all'
  | 'custom'

export type UserModelUsage = {
  model_name?: string
  request_count?: number
  token_count?: number
  consumption?: number
}

export type AnalyticsRanking = {
  user_id?: number
  username?: string
  display_name?: string
  remark?: string
  role?: number
  consumption?: number
  request_count?: number
  token_count?: number
  models?: UserModelUsage[]
}

export type AnalyticsData = {
  total_users?: number
  active_today?: number
  active_period?: number
  total_consumption?: number
  rankings?: AnalyticsRanking[]
}

export async function getUserAnalytics(
  period: AnalyticsPeriod,
  range?: { start: number; end: number }
) {
  const res = await api.get<{
    success: boolean
    message?: string
    data?: AnalyticsData
  }>('/api/admin/user-analytics', { params: { period, ...range } })
  return res.data
}

/**
 * Export per-user, per-model usage statistics as an Excel workbook.
 * start/end are unix seconds; omit either to leave that bound open.
 */
export async function exportUserAnalytics(params: {
  start?: number
  end?: number
  lang?: string
}): Promise<Blob> {
  const res = await api.get('/api/admin/user-analytics/export', {
    params,
    responseType: 'blob',
    disableDuplicate: true,
    skipBusinessError: true,
  } as ApiRequestConfig)
  return res.data
}
