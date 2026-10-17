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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { forwardRef, useImperativeHandle, type ForwardedRef } from 'react'
import { useForm } from 'react-hook-form'
import { toast } from 'sonner'
import { describe, expect, test, vi } from 'vitest'

import { parseModelPricingBackup } from '../model-pricing-export'
import { ModelRatioForm } from '../model-ratio-form'

vi.mock('../model-ratio-visual-editor', () => ({
  ModelRatioVisualEditor: forwardRef(function ModelRatioVisualEditorMock(
    _props: unknown,
    ref: ForwardedRef<{ commitOpenEditor: () => Promise<boolean> }>
  ) {
    useImperativeHandle(ref, () => ({
      commitOpenEditor: async () => true,
    }))
    return <div data-testid='model-ratio-visual-editor' />
  }),
}))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}))

type ModelFormValues = {
  ModelPrice: string
  ModelRatio: string
  OriginalModelPrice: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  BillingMode: string
  BillingExpr: string
  PluginBillingExpr: string
}

const defaultValues: ModelFormValues = {
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
}

function TestForm(props: {
  onSave: (values: ModelFormValues) => Promise<void>
}) {
  const form = useForm<ModelFormValues>({ defaultValues })

  return (
    <ModelRatioForm
      form={form}
      savedValues={defaultValues}
      onSave={props.onSave}
      onReset={() => undefined}
      isSaving={false}
      isResetting={false}
    />
  )
}

describe('model pricing import', () => {
  test('submits imported pricing from the default visual mode', async () => {
    const onSave = vi.fn(async (_values: ModelFormValues) => undefined)
    const queryClient = new QueryClient()
    const importedFile = {
      text: async () =>
        JSON.stringify({
          type: 'new-api-model-pricing',
          version: 1,
          data: {
            ModelPrice: { 'test-model': 0.02 },
            ModelRatio: { 'test-model': 0.6 },
            OriginalModelPrice: { 'test-model': { input: 1, output: 2 } },
            BillingMode: { 'test-model': 'tiered_expr' },
            BillingExpr: { 'test-model': 'p * 2' },
            PluginBillingExpr: {
              'doubao::test-model': 'u("tokens") * 3 / 1000000',
            },
          },
        }),
    } as File

    const { container } = render(
      <QueryClientProvider client={queryClient}>
        <TestForm onSave={onSave} />
      </QueryClientProvider>
    )

    const importInput =
      container.querySelector<HTMLInputElement>('input[type="file"]')
    if (!importInput) throw new Error('Expected model pricing file input')

    fireEvent.change(importInput, { target: { files: [importedFile] } })

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith(
        'Model pricing imported. Review the changes, then click Save model prices to apply them.'
      )
    })

    const saveButton = screen.getByRole('button', {
      name: 'Save model prices',
    })
    expect(onSave).not.toHaveBeenCalled()
    await waitFor(() => expect(saveButton).toBeEnabled())
    fireEvent.click(saveButton)

    await waitFor(() => {
      expect(onSave.mock.calls[0]?.[0]).toEqual(
        expect.objectContaining({
          ModelPrice: '{\n  "test-model": 0.02\n}',
          ModelRatio: '{\n  "test-model": 0.6\n}',
          OriginalModelPrice: JSON.stringify(
            { 'test-model': { input: 1, output: 2 } },
            null,
            2
          ),
          PluginBillingExpr: JSON.stringify(
            { 'doubao::test-model': 'u("tokens") * 3 / 1000000' },
            null,
            2
          ),
        })
      )
    })

    const createURL = vi
      .spyOn(URL, 'createObjectURL')
      .mockReturnValue('blob:pricing')
    const revokeURL = vi
      .spyOn(URL, 'revokeObjectURL')
      .mockImplementation(() => undefined)
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(() => undefined)
    fireEvent.click(
      screen.getByRole('button', { name: 'Export model pricing' })
    )
    expect(createURL).toHaveBeenCalledOnce()
    const blob = createURL.mock.calls[0][0] as Blob
    const exportedText = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => resolve(reader.result as string)
      reader.onerror = () => reject(reader.error)
      reader.readAsText(blob)
    })
    const exported = JSON.parse(exportedText)
    expect(exported).toMatchObject({
      type: 'new-api-model-pricing',
      version: 1,
    })
    const { ExposeRatioEnabled: _exposeRatio, ...savedPricing } =
      onSave.mock.calls[0][0]
    expect(parseModelPricingBackup(exported)).toEqual(savedPricing)
    expect(click).toHaveBeenCalledOnce()
    expect(revokeURL).toHaveBeenCalledWith('blob:pricing')
    createURL.mockRestore()
    revokeURL.mockRestore()
    click.mockRestore()
    queryClient.clear()
  })

  test('rejects a malformed backup without changing any draft field', async () => {
    const onSave = vi.fn(async (_values: ModelFormValues) => undefined)
    const queryClient = new QueryClient()
    render(
      <QueryClientProvider client={queryClient}>
        <TestForm onSave={onSave} />
      </QueryClientProvider>
    )
    const input = screen.getByLabelText('Import model pricing')
    fireEvent.change(input, {
      target: {
        files: [
          {
            text: async () =>
              JSON.stringify({
                data: { ModelPrice: { damaged: 123 }, BillingExpr: [] },
              }),
          },
        ],
      },
    })
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        'Invalid model pricing import file'
      )
    )
    expect(onSave).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Switch to JSON' }))
    fireEvent.click(screen.getByRole('button', { name: 'Save model prices' }))
    await waitFor(() =>
      expect(onSave.mock.calls[0]?.[0]).toEqual(defaultValues)
    )
    queryClient.clear()
  })
})
