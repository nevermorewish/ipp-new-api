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
import { Code2, Download, Eye, RotateCcw, Save, Upload } from 'lucide-react'
import { memo, useCallback, useEffect, useRef, useState } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { JsonCodeEditor } from '@/components/json-code-editor'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'
import { getEnabledModels } from '@/features/channels/api'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import {
  ModelRatioVisualEditor,
  type ModelRatioVisualEditorHandle,
} from './model-ratio-visual-editor'
import { formatJsonForTextarea } from './utils'

type ModelFormValues = {
  ModelPrice: string
  ModelRatio: string
  OriginalModelPrice?: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  BillingMode: string
  BillingExpr: string
}

type ModelRatioFormProps = {
  form: UseFormReturn<any>
  savedValues: any
  onSave: (values: any) => Promise<void>
  onReset: () => void
  isSaving: boolean
  isResetting: boolean
  variant?: 'default' | 'unset'
}

type ModelJsonFieldName =
  | 'ModelPrice'
  | 'ModelRatio'
  | 'OriginalModelPrice'
  | 'CacheRatio'
  | 'CreateCacheRatio'
  | 'CompletionRatio'
  | 'ImageRatio'
  | 'AudioRatio'
  | 'AudioCompletionRatio'

const modelJsonFields: Array<{
  name: ModelJsonFieldName
  labelKey: string
  descriptionKey: string
}> = [
  {
    name: 'ModelPrice',
    labelKey: 'Model fixed pricing',
    descriptionKey:
      'JSON map of model → USD cost per request. Takes precedence over ratio based billing.',
  },
  {
    name: 'ModelRatio',
    labelKey: 'Model ratio',
    descriptionKey: 'JSON map of model → multiplier applied to quota billing.',
  },
  {
    name: 'OriginalModelPrice',
    labelKey: 'Original model prices',
    descriptionKey:
      'Optional direct USD prices per 1M tokens. Used only to display list prices and discounts.',
  },
  {
    name: 'CacheRatio',
    labelKey: 'Prompt cache ratio',
    descriptionKey: 'Optional ratio used when upstream cache hits occur.',
  },
  {
    name: 'CreateCacheRatio',
    labelKey: 'Create cache ratio',
    descriptionKey:
      'Ratio applied when creating cache entries for supported models.',
  },
  {
    name: 'CompletionRatio',
    labelKey: 'Completion ratio',
    descriptionKey:
      'Applies to custom completion endpoints. JSON map of model → ratio.',
  },
  {
    name: 'ImageRatio',
    labelKey: 'Image ratio',
    descriptionKey: 'Configure per-model ratio for image inputs or outputs.',
  },
  {
    name: 'AudioRatio',
    labelKey: 'Audio ratio',
    descriptionKey:
      'Ratio applied to audio inputs where supported by the upstream model.',
  },
  {
    name: 'AudioCompletionRatio',
    labelKey: 'Audio completion ratio',
    descriptionKey: 'Ratio applied to audio completions for streaming models.',
  },
]

const MODEL_PRICING_FIELDS = [
  'ModelPrice',
  'ModelRatio',
  'OriginalModelPrice',
  'CacheRatio',
  'CreateCacheRatio',
  'CompletionRatio',
  'ImageRatio',
  'AudioRatio',
  'AudioCompletionRatio',
  'BillingMode',
  'BillingExpr',
] as const satisfies ReadonlyArray<keyof ModelFormValues>

type ModelPricingField = (typeof MODEL_PRICING_FIELDS)[number]

type ModelPricingExport = {
  type: 'new-api-model-pricing'
  version: 1
  exported_at: string
  data: Record<ModelPricingField, unknown>
}

function parseJsonValue(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return {}
  return JSON.parse(trimmed)
}

function stringifyImportedJsonValue(value: unknown): string {
  if (typeof value === 'string') {
    return formatJsonForTextarea(value)
  }
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return JSON.stringify(value, null, 2)
  }
  return JSON.stringify(value ?? {}, null, 2)
}

function getImportedPricingData(
  parsed: unknown
): Partial<Record<ModelPricingField, unknown>> {
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Invalid model pricing import file')
  }

  const record = parsed as Record<string, unknown>
  const nested = record.data ?? record.pricing ?? record.model_pricing
  const source =
    nested && typeof nested === 'object' && !Array.isArray(nested)
      ? (nested as Record<string, unknown>)
      : record
  const result: Partial<Record<ModelPricingField, unknown>> = {}

  for (const field of MODEL_PRICING_FIELDS) {
    if (Object.hasOwn(source, field)) {
      result[field] = source[field]
      continue
    }
    let legacyKey: string | undefined
    if (field === 'BillingMode') {
      legacyKey = 'billing_setting.billing_mode'
    } else if (field === 'BillingExpr') {
      legacyKey = 'billing_setting.billing_expr'
    }
    if (legacyKey && Object.hasOwn(source, legacyKey)) {
      result[field] = source[legacyKey]
    }
  }

  if (Object.keys(result).length === 0) {
    throw new Error('Import file does not contain model pricing data')
  }
  return result
}

function ModelJsonTextareaField(props: {
  form: UseFormReturn<ModelFormValues>
  name: ModelJsonFieldName
  label: string
  description: string
}) {
  return (
    <FormField
      control={props.form.control}
      name={props.name}
      render={({ field }) => (
        <FormItem className='flex min-w-0 flex-col gap-2'>
          <FormLabel>{props.label}</FormLabel>
          <FormControl>
            <JsonCodeEditor
              value={field.value ?? ''}
              onChange={(value) => field.onChange(value)}
              name={field.name}
              onBlur={field.onBlur}
              textareaRef={field.ref}
            />
          </FormControl>
          <FormDescription className='text-xs leading-5'>
            {props.description}
          </FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}

export const ModelRatioForm = memo(function ModelRatioForm({
  form,
  savedValues,
  onSave,
  onReset,
  isSaving,
  isResetting,
  variant = 'default',
}: ModelRatioFormProps) {
  const { t } = useTranslation()
  const isUnsetVariant = variant === 'unset'
  const [editMode, setEditMode] = useState<'visual' | 'json'>('visual')
  const visualEditorRef = useRef<ModelRatioVisualEditorHandle>(null)
  const importInputRef = useRef<HTMLInputElement | null>(null)

  const enabledModelsQuery = useQuery({
    queryKey: ['enabled-models'],
    queryFn: getEnabledModels,
    enabled: isUnsetVariant,
  })

  const enabledModelsError = isUnsetVariant
    ? enabledModelsQuery.isError ||
      (enabledModelsQuery.data !== undefined &&
        !enabledModelsQuery.data.success)
    : false
  const enabledModelsErrorMessage = enabledModelsQuery.data?.message

  useEffect(() => {
    if (!enabledModelsError) return
    toast.error(enabledModelsErrorMessage || t('Failed to load enabled models'))
  }, [enabledModelsError, enabledModelsErrorMessage, t])

  const handleFieldChange = useCallback(
    (field: keyof ModelFormValues, value: string) => {
      form.setValue(field, value, {
        shouldValidate: true,
        shouldDirty: true,
      })
    },
    [form]
  )

  const toggleEditMode = useCallback(() => {
    setEditMode((prev) => (prev === 'visual' ? 'json' : 'visual'))
  }, [])

  const handleExportModelPricing = useCallback(() => {
    try {
      const data = Object.fromEntries(
        MODEL_PRICING_FIELDS.map((field) => [
          field,
          parseJsonValue(form.getValues(field)),
        ])
      ) as Record<ModelPricingField, unknown>
      const payload: ModelPricingExport = {
        type: 'new-api-model-pricing',
        version: 1,
        exported_at: new Date().toISOString(),
        data,
      }
      const blob = new Blob([`${JSON.stringify(payload, null, 2)}\n`], {
        type: 'application/json',
      })
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `model-pricing-${new Date().toISOString().slice(0, 10)}.json`
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
      toast.success(t('Model pricing exported successfully'))
    } catch (error) {
      toast.error(
        error instanceof Error
          ? t(error.message)
          : t('Failed to export model pricing')
      )
    }
  }, [form, t])

  const handleImportModelPricing = useCallback(
    async (file: File) => {
      try {
        const imported = getImportedPricingData(JSON.parse(await file.text()))
        for (const field of MODEL_PRICING_FIELDS) {
          if (!Object.hasOwn(imported, field)) continue
          const value = imported[field]
          form.setValue(field, stringifyImportedJsonValue(value), {
            shouldDirty: true,
            shouldValidate: true,
          })
        }
        toast.success(
          t(
            'Model pricing imported. Review the changes, then click Save model prices to apply them.'
          )
        )
      } catch (error) {
        toast.error(
          error instanceof Error
            ? t(error.message)
            : t('Failed to import model pricing')
        )
      } finally {
        if (importInputRef.current) importInputRef.current.value = ''
      }
    },
    [form, t]
  )

  const handleSave = useCallback(async () => {
    if (editMode === 'visual') {
      const committed = await visualEditorRef.current?.commitOpenEditor()
      if (committed === false) return
    }

    await form.handleSubmit(onSave)()
  }, [editMode, form, onSave])

  return (
    <div className='space-y-6'>
      {!isUnsetVariant && (
        <div className='flex flex-wrap justify-end gap-2'>
          <input
            ref={importInputRef}
            type='file'
            accept='application/json,.json'
            className='hidden'
            onChange={(event) => {
              const file = event.target.files?.[0]
              if (file) void handleImportModelPricing(file)
            }}
          />
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => importInputRef.current?.click()}
          >
            <Upload data-icon='inline-start' />
            {t('Import model pricing')}
          </Button>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={handleExportModelPricing}
          >
            <Download data-icon='inline-start' />
            {t('Export model pricing')}
          </Button>
          <Button
            type='button'
            variant='destructive'
            size='sm'
            onClick={onReset}
            disabled={isResetting}
          >
            <RotateCcw data-icon='inline-start' />
            {t('Reset prices')}
          </Button>
          <Button
            type='button'
            size='sm'
            onClick={handleSave}
            disabled={isSaving}
          >
            <Save data-icon='inline-start' />
            {isSaving ? t('Saving...') : t('Save model prices')}
          </Button>
          <Button variant='outline' size='sm' onClick={toggleEditMode}>
            {editMode === 'visual' ? (
              <>
                <Code2 className='mr-2 h-4 w-4' />
                {t('Switch to JSON')}
              </>
            ) : (
              <>
                <Eye className='mr-2 h-4 w-4' />
                {t('Switch to Visual')}
              </>
            )}
          </Button>
        </div>
      )}

      <Form {...form}>
        {editMode === 'visual' ? (
          <div className='space-y-6'>
            <ModelRatioVisualEditor
              ref={visualEditorRef}
              savedModelPrice={savedValues.ModelPrice}
              savedModelRatio={savedValues.ModelRatio}
              savedOriginalModelPrice={savedValues.OriginalModelPrice || '{}'}
              savedCacheRatio={savedValues.CacheRatio}
              savedCreateCacheRatio={savedValues.CreateCacheRatio}
              savedCompletionRatio={savedValues.CompletionRatio}
              savedImageRatio={savedValues.ImageRatio}
              savedAudioRatio={savedValues.AudioRatio}
              savedAudioCompletionRatio={savedValues.AudioCompletionRatio}
              savedBillingMode={savedValues.BillingMode}
              savedBillingExpr={savedValues.BillingExpr}
              modelPrice={form.watch('ModelPrice')}
              modelRatio={form.watch('ModelRatio')}
              originalModelPrice={form.watch('OriginalModelPrice') || '{}'}
              cacheRatio={form.watch('CacheRatio')}
              createCacheRatio={form.watch('CreateCacheRatio')}
              completionRatio={form.watch('CompletionRatio')}
              imageRatio={form.watch('ImageRatio')}
              audioRatio={form.watch('AudioRatio')}
              audioCompletionRatio={form.watch('AudioCompletionRatio')}
              billingMode={form.watch('BillingMode')}
              billingExpr={form.watch('BillingExpr')}
              candidateModelNames={
                isUnsetVariant ? enabledModelsQuery.data?.data : undefined
              }
              candidateModelsLoading={
                isUnsetVariant && enabledModelsQuery.isLoading
              }
              filterMode={isUnsetVariant ? 'unset' : 'all'}
              onSave={handleSave}
              isSaving={isSaving}
              onChange={(field, value) => {
                const fieldMap: Record<string, keyof ModelFormValues> = {
                  'billing_setting.billing_mode': 'BillingMode',
                  'billing_setting.billing_expr': 'BillingExpr',
                }
                const formField =
                  fieldMap[field] || (field as keyof ModelFormValues)
                handleFieldChange(formField, value)
              }}
            />

            {!isUnsetVariant && (
              <FormField
                control={form.control}
                name='ExposeRatioEnabled'
                render={({ field }) => (
                  <SettingsSwitchItem>
                    <SettingsSwitchContent>
                      <FormLabel>{t('Expose ratio API')}</FormLabel>
                      <FormDescription>
                        {t(
                          'Allow clients to query configured ratios via `/api/ratio`.'
                        )}
                      </FormDescription>
                    </SettingsSwitchContent>
                    <FormControl>
                      <Switch
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                    </FormControl>
                  </SettingsSwitchItem>
                )}
              />
            )}
          </div>
        ) : (
          <SettingsForm onSubmit={form.handleSubmit(onSave)}>
            <div className='grid min-w-0 gap-x-5 gap-y-8 lg:grid-cols-2 2xl:grid-cols-3'>
              {modelJsonFields.map((config) => (
                <ModelJsonTextareaField
                  key={config.name}
                  form={form}
                  name={config.name}
                  label={t(config.labelKey)}
                  description={t(config.descriptionKey)}
                />
              ))}
            </div>

            <FormField
              control={form.control}
              name='ExposeRatioEnabled'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>{t('Expose ratio API')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Allow clients to query configured ratios via `/api/ratio`.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
          </SettingsForm>
        )}
      </Form>
    </div>
  )
})
