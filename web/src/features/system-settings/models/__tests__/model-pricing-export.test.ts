import { describe, expect, it } from 'vitest'
import * as XLSX from 'xlsx'

import type { PricingModel } from '@/features/pricing/types'

import {
  buildModelPricingExportRows,
  createModelPricingWorkbook,
  MODEL_PRICING_EXPORT_HEADERS,
} from '../model-pricing-export'
import type { ModelPricingSnapshot } from '../model-pricing-snapshots'

const snapshot = (
  values: Partial<ModelPricingSnapshot>
): ModelPricingSnapshot => ({
  name: 'model',
  ratio: '',
  hasConflict: false,
  ...values,
})

const pricingModel = (model_name: string, vendor_name: string): PricingModel =>
  ({ model_name, vendor_name }) as PricingModel

describe('model pricing Excel export', () => {
  it('sorts vendors and calculates all four discounts', () => {
    const rows = buildModelPricingExportRows(
      [
        snapshot({
          name: 'qwen3-test',
          ratio: '1',
          completionRatio: '3',
          cacheRatio: '0.1',
          createCacheRatio: '1.25',
          originalInputPrice: '4',
          originalOutputPrice: '12',
          originalCacheReadPrice: '0.4',
          originalCacheWritePrice: '5',
        }),
        snapshot({
          name: 'claude-test',
          ratio: '1.5',
          completionRatio: '5',
          cacheRatio: '0.1',
          createCacheRatio: '1.25',
          originalInputPrice: '10',
          originalOutputPrice: '50',
          originalCacheReadPrice: '1',
          originalCacheWritePrice: '5',
        }),
      ],
      [
        pricingModel('qwen3-test', '阿里巴巴'),
        pricingModel('claude-test', 'Anthropic'),
      ]
    )

    expect(rows.map((row) => row[1])).toEqual(['Claude', 'Qwen'])
    expect(rows[0].slice(0, 8)).toEqual([
      'claude-test',
      'Claude',
      10,
      50,
      1,
      5,
      3,
      15,
    ])
    expect(rows[0][8]).toBeCloseTo(0.3)
    expect(rows[0][9]).toBeCloseTo(3.75)
    expect(rows[0].slice(10, 14)).toEqual([1.5, 5, 0.1, 1.25])
    expect(rows[0][14]).toBeCloseTo(0.3)
    expect(rows[0][15]).toBeCloseTo(0.3)
    expect(rows[0][16]).toBeCloseTo(0.3)
    expect(rows[0][17]).toBeCloseTo(0.75)
  })

  it('includes the output discount column in the workbook header', () => {
    const workbook = createModelPricingWorkbook([])
    const sheet = workbook.Sheets.Pricing
    const header = XLSX.utils.sheet_to_json<string[]>(sheet, {
      header: 1,
      range: 3,
    })[0]

    expect(header).toEqual([...MODEL_PRICING_EXPORT_HEADERS])
    expect(header).toContain('折扣-输出')
  })
})
