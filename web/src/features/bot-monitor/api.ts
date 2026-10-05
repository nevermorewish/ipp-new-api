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
import type { AxiosRequestConfig } from 'axios'

import { api } from '@/lib/api'

import { unwrapApiResult, type ApiResult } from './api-result'

export type BotMonitorRobot = {
  id: number
  name: string
  type: 'feishu' | 'wechat'
  enabled: boolean
  has_api_url: boolean
  conversation_id: string
  conversation_nickname: string
  has_feishu_secret: boolean
  feishu_chat_count: number
  last_status: string
  last_error: string
  last_sent_at: number
}
export type BotMonitorRobotPayload = {
  name: string
  type: 'feishu' | 'wechat'
  enabled: boolean
  feishu_app_id?: string
  feishu_app_secret?: string
  feishu_chat_ids?: string
  api_url?: string
  conversation_id?: string
  conversation_nickname?: string
}
export type BotMonitorConversationOption = {
  conversation_id: string
  nickname: string
  is_external: number
  total: number
}
export type BotMonitorBinding = {
  id: number
  name: string
  robot_id: number
  robot_name: string
  robot_type: 'feishu' | 'wechat'
  channel_ids: number[]
  min_interval_seconds: number
  trigger_status_codes: string
  enabled: boolean
}
export type BotMonitorBindingPayload = {
  name: string
  robot_id: number
  channel_ids: number[]
  min_interval_seconds: number
  trigger_status_codes: string
  enabled: boolean
}
export type MonitorChannelOption = {
  id: number
  name: string
  type: number
  status: number
}
export type BotMonitorLatencyBinding = {
  id: number
  name: string
  robot_id: number
  robot_name: string
  robot_type: 'feishu' | 'wechat'
  model_names: string[]
  first_token_timeout_seconds: number
  min_interval_seconds: number
  enabled: boolean
}
export type BotMonitorLatencyBindingPayload = {
  name: string
  robot_id: number
  model_names: string[]
  first_token_timeout_seconds: number
  min_interval_seconds: number
  enabled: boolean
}
export type BotMonitorBalanceBinding = {
  id: number
  robot_id: number
  robot_name: string
  robot_type: 'feishu' | 'wechat'
  user_ids: number[]
  threshold_amount: number
  check_interval_hours: number
  enabled: boolean
}
export type BotMonitorBalanceBindingPayload = {
  robot_id: number
  user_ids: number[]
  threshold_amount: number
  check_interval_hours: number
  enabled: boolean
}
export type MonitorUserOption = {
  id: number
  username: string
  display_name: string
  quota: number
  status: number
}
const locallyHandledError: AxiosRequestConfig & {
  skipBusinessError: boolean
} = { skipBusinessError: true }

export async function listBotMonitorRobots() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: BotMonitorRobot[] }>>(
      '/api/admin/monitor-robots'
    )
  ).items
}
export async function createBotMonitorRobot(payload: BotMonitorRobotPayload) {
  return unwrapApiResult(
    await api.post<ApiResult<BotMonitorRobot>>(
      '/api/admin/monitor-robots',
      payload,
      locallyHandledError
    )
  )
}
export async function updateBotMonitorRobot(
  id: number,
  payload: BotMonitorRobotPayload
) {
  return unwrapApiResult(
    await api.put<ApiResult<BotMonitorRobot>>(
      `/api/admin/monitor-robots/${id}`,
      payload,
      locallyHandledError
    )
  )
}
export async function deleteBotMonitorRobot(id: number) {
  return unwrapApiResult(
    await api.delete<ApiResult<null>>(
      `/api/admin/monitor-robots/${id}`,
      locallyHandledError
    )
  )
}
export async function testBotMonitorRobot(id: number) {
  return unwrapApiResult(
    await api.post<ApiResult<null>>(
      `/api/admin/monitor-robots/${id}/test`,
      undefined,
      locallyHandledError
    )
  )
}
export async function listBotMonitorRobotConversations(id: number) {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: BotMonitorConversationOption[] }>>(
      `/api/admin/monitor-robots/${id}/conversations`,
      locallyHandledError
    )
  ).items
}
export async function queryBotMonitorRobotConversations(apiUrl: string) {
  return unwrapApiResult(
    await api.post<ApiResult<{ items: BotMonitorConversationOption[] }>>(
      '/api/admin/monitor-robots/conversations/query',
      { api_url: apiUrl },
      locallyHandledError
    )
  ).items
}
export async function listBotMonitorBindings() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: BotMonitorBinding[] }>>(
      '/api/admin/monitor-bindings'
    )
  ).items
}
export async function createBotMonitorBinding(
  payload: BotMonitorBindingPayload
) {
  return unwrapApiResult(
    await api.post<ApiResult<BotMonitorBinding>>(
      '/api/admin/monitor-bindings',
      payload,
      locallyHandledError
    )
  )
}
export async function updateBotMonitorBinding(
  id: number,
  payload: BotMonitorBindingPayload
) {
  return unwrapApiResult(
    await api.put<ApiResult<BotMonitorBinding>>(
      `/api/admin/monitor-bindings/${id}`,
      payload,
      locallyHandledError
    )
  )
}
export async function deleteBotMonitorBinding(id: number) {
  return unwrapApiResult(
    await api.delete<ApiResult<null>>(
      `/api/admin/monitor-bindings/${id}`,
      locallyHandledError
    )
  )
}
export async function listBotMonitorChannelOptions() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: MonitorChannelOption[] }>>(
      '/api/admin/monitor-bindings/channel-options'
    )
  ).items
}
export async function listBotMonitorLatencyBindings() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: BotMonitorLatencyBinding[] }>>(
      '/api/admin/monitor-latency-bindings'
    )
  ).items
}
export async function createBotMonitorLatencyBinding(
  payload: BotMonitorLatencyBindingPayload
) {
  return unwrapApiResult(
    await api.post<ApiResult<BotMonitorLatencyBinding>>(
      '/api/admin/monitor-latency-bindings',
      payload,
      locallyHandledError
    )
  )
}
export async function updateBotMonitorLatencyBinding(
  id: number,
  payload: BotMonitorLatencyBindingPayload
) {
  return unwrapApiResult(
    await api.put<ApiResult<BotMonitorLatencyBinding>>(
      `/api/admin/monitor-latency-bindings/${id}`,
      payload,
      locallyHandledError
    )
  )
}
export async function deleteBotMonitorLatencyBinding(id: number) {
  return unwrapApiResult(
    await api.delete<ApiResult<null>>(
      `/api/admin/monitor-latency-bindings/${id}`,
      locallyHandledError
    )
  )
}
export async function listBotMonitorLatencyModelOptions() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: string[] }>>(
      '/api/admin/monitor-latency-bindings/model-options'
    )
  ).items
}
export async function listBotMonitorBalanceBindings() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: BotMonitorBalanceBinding[] }>>(
      '/api/admin/monitor-balance-bindings'
    )
  ).items
}
export async function createBotMonitorBalanceBinding(
  payload: BotMonitorBalanceBindingPayload
) {
  return unwrapApiResult(
    await api.post<ApiResult<BotMonitorBalanceBinding>>(
      '/api/admin/monitor-balance-bindings',
      payload,
      locallyHandledError
    )
  )
}
export async function updateBotMonitorBalanceBinding(
  id: number,
  payload: BotMonitorBalanceBindingPayload
) {
  return unwrapApiResult(
    await api.put<ApiResult<BotMonitorBalanceBinding>>(
      `/api/admin/monitor-balance-bindings/${id}`,
      payload,
      locallyHandledError
    )
  )
}
export async function deleteBotMonitorBalanceBinding(id: number) {
  return unwrapApiResult(
    await api.delete<ApiResult<null>>(
      `/api/admin/monitor-balance-bindings/${id}`,
      locallyHandledError
    )
  )
}
export async function listBotMonitorUserOptions() {
  return unwrapApiResult(
    await api.get<ApiResult<{ items: MonitorUserOption[] }>>(
      '/api/admin/monitor-balance-bindings/user-options'
    )
  ).items
}
