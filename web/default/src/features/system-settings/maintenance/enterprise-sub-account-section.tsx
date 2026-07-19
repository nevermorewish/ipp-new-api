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
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormLabel,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'

type EnterpriseSubAccountFormValues = {
  EnterpriseSubAccountUrl: string
}

type EnterpriseSubAccountSectionProps = {
  defaultValue: string
}

function normalizeValue(value: unknown): string {
  if (value === undefined || value === null) return ''
  return typeof value === 'string' ? value : String(value)
}

export function EnterpriseSubAccountSection(
  props: EnterpriseSubAccountSectionProps
) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<EnterpriseSubAccountFormValues>({
    defaultValues: {
      EnterpriseSubAccountUrl: normalizeValue(props.defaultValue),
    },
  })

  useEffect(() => {
    form.reset({
      EnterpriseSubAccountUrl: normalizeValue(props.defaultValue),
    })
  }, [form, props.defaultValue])

  const onSubmit = async (values: EnterpriseSubAccountFormValues) => {
    const nextValue = normalizeValue(values.EnterpriseSubAccountUrl)
    if (nextValue === normalizeValue(props.defaultValue)) {
      return
    }
    await updateOption.mutateAsync({
      key: 'EnterpriseSubAccountUrl',
      value: nextValue,
    })
  }

  return (
    <SettingsSection title={t('Enterprise Sub-account')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)}>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Configure the enterprise sub-account management page shown to enterprise administrators.'
            )}
          </p>

          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() =>
              form.reset({
                EnterpriseSubAccountUrl: normalizeValue(props.defaultValue),
              })
            }
            isSaving={updateOption.isPending}
            resetLabel='Reset'
            saveLabel='Save Changes'
          />
          <FormField
            control={form.control}
            name='EnterpriseSubAccountUrl'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enterprise sub-account URL')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Enterprise administrators are sent to this URL from the enterprise sub-account page.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Input placeholder='https://example.com/team' {...field} />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
