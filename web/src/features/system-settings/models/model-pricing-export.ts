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
import type { WorkBook } from 'xlsx'
import * as XLSX from 'xlsx'

import type { PricingModel } from '@/features/pricing/types'

import type { ModelPricingSnapshot, ModelRow } from './model-pricing-snapshots'

export const MODEL_PRICING_EXPORT_HEADERS = [
  '模型名称',
  '厂商',
  '原价-输入',
  '原价-输出',
  '原价-缓存读取',
  '原价-缓存写入',
  '当前价-输入',
  '当前价-输出',
  '当前价-缓存读取',
  '当前价-缓存写入',
  '内部 ModelRatio',
  'CompletionRatio',
  'CacheRatio',
  'CreateCacheRatio',
  '折扣-输入',
  '折扣-输出',
  '折扣-缓存读取',
  '折扣-缓存写入',
] as const

const VENDOR_ORDER = [
  'Claude',
  'OpenAI',
  'Gemini',
  'Kimi',
  'DeepSeek',
  'GLM',
  'Qwen',
] as const

type ExportCell = string | number | null
export type ModelPricingExportRow = [string, string, ...ExportCell[]]

const toNumber = (value?: string | number | null) => {
  if (value === '' || value === undefined || value === null) return null
  const number = Number(value)
  return Number.isFinite(number) ? number : null
}

const multiply = (left: number | null, right: number | null) =>
  left !== null && right !== null ? left * right : null

const discount = (current: number | null, original: number | null) =>
  current !== null && original !== null && original !== 0
    ? current / original
    : null

const vendorGroup = (vendorName: string | undefined, modelName: string) => {
  const value = `${vendorName || ''} ${modelName}`.toLowerCase()
  if (value.includes('anthropic') || value.includes('claude')) return 'Claude'
  if (value.includes('openai') || value.includes('gpt')) return 'OpenAI'
  if (value.includes('google') || value.includes('gemini')) return 'Gemini'
  if (value.includes('moonshot') || value.includes('kimi')) return 'Kimi'
  if (value.includes('deepseek')) return 'DeepSeek'
  if (
    value.includes('zhipu') ||
    value.includes('智谱') ||
    value.includes('glm')
  ) {
    return 'GLM'
  }
  if (
    value.includes('qwen') ||
    value.includes('阿里') ||
    value.includes('alibaba')
  ) {
    return 'Qwen'
  }
  return vendorName || ''
}

const toExportRow = (
  snapshot: ModelPricingSnapshot,
  vendorName: string | undefined
): ModelPricingExportRow => {
  const originalInput = toNumber(snapshot.originalInputPrice)
  const originalOutput = toNumber(snapshot.originalOutputPrice)
  const originalCacheRead = toNumber(snapshot.originalCacheReadPrice)
  const originalCacheWrite = toNumber(snapshot.originalCacheWritePrice)
  const modelRatio = toNumber(snapshot.ratio)
  const completionRatio = toNumber(snapshot.completionRatio)
  const cacheRatio = toNumber(snapshot.cacheRatio)
  const createCacheRatio = toNumber(snapshot.createCacheRatio)
  const currentInput = multiply(modelRatio, 2)
  const currentOutput = multiply(currentInput, completionRatio)
  const currentCacheRead = multiply(currentInput, cacheRatio)
  const currentCacheWrite = multiply(currentInput, createCacheRatio)

  return [
    snapshot.name,
    vendorGroup(vendorName, snapshot.name),
    originalInput,
    originalOutput,
    originalCacheRead,
    originalCacheWrite,
    currentInput,
    currentOutput,
    currentCacheRead,
    currentCacheWrite,
    modelRatio,
    completionRatio,
    cacheRatio,
    createCacheRatio,
    discount(currentInput, originalInput),
    discount(currentOutput, originalOutput),
    discount(currentCacheRead, originalCacheRead),
    discount(currentCacheWrite, originalCacheWrite),
  ]
}

export function buildModelPricingExportRows(
  rows: Array<ModelPricingSnapshot | ModelRow>,
  pricingModels: PricingModel[]
): ModelPricingExportRow[] {
  const vendorByModel = new Map(
    pricingModels.map((model) => [model.model_name, model.vendor_name])
  )

  return rows
    .map((row) => ('draft' in row ? (row.draft ?? row.saved ?? row) : row))
    .map((snapshot) => toExportRow(snapshot, vendorByModel.get(snapshot.name)))
    .sort((left, right) => {
      const leftGroup = VENDOR_ORDER.indexOf(
        left[1] as (typeof VENDOR_ORDER)[number]
      )
      const rightGroup = VENDOR_ORDER.indexOf(
        right[1] as (typeof VENDOR_ORDER)[number]
      )
      const leftOrder = leftGroup === -1 ? VENDOR_ORDER.length : leftGroup
      const rightOrder = rightGroup === -1 ? VENDOR_ORDER.length : rightGroup
      return leftOrder - rightOrder || left[0].localeCompare(right[0])
    })
}

export function createModelPricingWorkbook(
  rows: ModelPricingExportRow[],
  exportedAt = new Date()
): WorkBook {
  const sheetRows: ExportCell[][] = [
    ['ippnewapi /pricing 模型价格明细'],
    [
      '来源：当前模型计价页面；价格单位：CNY/百万 tokens。按 Claude、OpenAI、Gemini、Kimi、DeepSeek、GLM、Qwen 聚合排序。',
    ],
    [
      `导出时间：${exportedAt.toISOString()}；当前页面显示 ${rows.length} 个模型。`,
    ],
    [...MODEL_PRICING_EXPORT_HEADERS],
    ...rows,
  ]
  const worksheet = XLSX.utils.aoa_to_sheet(sheetRows)
  worksheet['!merges'] = [
    {
      s: { r: 0, c: 0 },
      e: { r: 0, c: MODEL_PRICING_EXPORT_HEADERS.length - 1 },
    },
    {
      s: { r: 1, c: 0 },
      e: { r: 1, c: MODEL_PRICING_EXPORT_HEADERS.length - 1 },
    },
    {
      s: { r: 2, c: 0 },
      e: { r: 2, c: MODEL_PRICING_EXPORT_HEADERS.length - 1 },
    },
  ]
  worksheet['!autofilter'] = {
    ref: `A4:R${Math.max(rows.length + 4, 4)}`,
  }
  worksheet['!cols'] = [
    { wch: 34 },
    { wch: 14 },
    ...Array.from({ length: 16 }, () => ({ wch: 16 })),
  ]

  for (let rowIndex = 4; rowIndex < rows.length + 4; rowIndex += 1) {
    for (const columnIndex of [14, 15, 16, 17]) {
      const cell =
        worksheet[XLSX.utils.encode_cell({ r: rowIndex, c: columnIndex })]
      if (cell && typeof cell.v === 'number') cell.z = '0.00%'
    }
  }

  const workbook = XLSX.utils.book_new()
  XLSX.utils.book_append_sheet(workbook, worksheet, 'Pricing')
  return workbook
}
