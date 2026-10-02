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
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { RatioSettingsCard } from '../ratio-settings-card'

const mocks = vi.hoisted(() => ({
  updateOption: vi.fn().mockResolvedValue({ success: true }),
  savePricing: vi.fn().mockResolvedValue(undefined),
  refetch: vi.fn(),
}))

vi.mock('@/features/model-pricing/api', () => ({
  buildPricingChanges: () => [],
  useModelPricing: () => ({
    data: {
      entries: [],
      options: {
        ModelPrice: '{}',
        ModelRatio: '{}',
        CacheRatio: '{}',
        CreateCacheRatio: '{}',
        CompletionRatio: '{}',
        ImageRatio: '{}',
        AudioRatio: '{}',
        AudioCompletionRatio: '{}',
        'billing_setting.billing_mode': '{}',
        'billing_setting.billing_expr': '{}',
        'billing_setting.plugin_billing_expr': '{}',
      },
      empty_version: 'empty',
    },
    isError: false,
    refetch: mocks.refetch,
  }),
  useSaveModelPricing: () => ({
    isError: false,
    isPending: false,
    mutateAsync: mocks.savePricing,
    reset: vi.fn(),
  }),
}))

vi.mock('../../hooks/use-update-option', () => ({
  useUpdateOption: () => ({
    isPending: false,
    mutateAsync: mocks.updateOption,
  }),
}))

vi.mock('../model-ratio-form', () => ({
  ModelRatioForm: (props: {
    savedValues: Record<string, string | boolean>
    onSave: (values: Record<string, string | boolean>) => Promise<void>
  }) => (
    <button
      type='button'
      onClick={() =>
        void props.onSave({
          ...props.savedValues,
          OriginalModelPrice: '{"gpt-5":{"input":10}}',
        })
      }
    >
      Save
    </button>
  ),
}))

afterEach(() => {
  vi.clearAllMocks()
})

it('persists original model prices when they are the only changed pricing field', async () => {
  const user = userEvent.setup()
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RatioSettingsCard
        modelDefaults={{
          ModelPrice: '{}',
          ModelRatio: '{}',
          OriginalModelPrice: '{}',
          CacheRatio: '{}',
          CreateCacheRatio: '{}',
          CompletionRatio: '{}',
          ImageRatio: '{}',
          AudioRatio: '{}',
          AudioCompletionRatio: '{}',
          ExposeRatioEnabled: false,
          BillingMode: '{}',
          BillingExpr: '{}',
          PluginBillingExpr: '{}',
        }}
        groupDefaults={{
          GroupRatio: '{}',
          TopupGroupRatio: '{}',
          UserUsableGroups: '{}',
          GroupGroupRatio: '{}',
          AutoGroups: '[]',
          MaxTokenAutoGroups: 1,
          DefaultUseAutoGroup: false,
          GroupSpecialUsableGroup: '{}',
        }}
        toolPricesDefault='{}'
        visibleTabs={['models']}
      />
    </QueryClientProvider>
  )

  const save = await screen.findByRole('button', { name: 'Save' })
  await user.click(save)

  await waitFor(() =>
    expect(mocks.updateOption).toHaveBeenCalledWith({
      key: 'OriginalModelPrice',
      value: '{"gpt-5":{"input":10}}',
    })
  )
  expect(mocks.savePricing).not.toHaveBeenCalled()
})
