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
import type { Control } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { FormField } from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'

import { CHANNEL_TYPE_OPENAI_GPT } from '../../constants'
import type { ChannelFormValues } from '../../lib/channel-form'

export function OpenAIGPTSettings(props: {
  control: Control<ChannelFormValues>
  channelType: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  return (
    <>
      {props.channelType === CHANNEL_TYPE_OPENAI_GPT && (
        <FormField
          control={props.control}
          name='remove_gpt_temperature'
          render={({ field }) => (
            <FieldGroup className='px-4 py-3'>
              <Field orientation='horizontal'>
                <FieldContent>
                  <FieldLabel htmlFor='remove-gpt-temperature'>
                    {t('Remove temperature for GPT-5 and GPT-6')}
                  </FieldLabel>
                  <FieldDescription id='remove-gpt-temperature-description'>
                    {t(
                      'Off by default. When enabled, removes temperature from GPT-5 and GPT-6 requests on this channel, including pass-through requests.'
                    )}
                  </FieldDescription>
                </FieldContent>
                <Switch
                  id='remove-gpt-temperature'
                  aria-describedby='remove-gpt-temperature-description'
                  disabled={props.disabled}
                  checked={field.value ?? false}
                  onCheckedChange={field.onChange}
                />
              </Field>
            </FieldGroup>
          )}
        />
      )}
      {props.channelType === CHANNEL_TYPE_OPENAI_GPT && (
        <FormField
          control={props.control}
          name='remove_azure_gpt_encryption'
          render={({ field }) => (
            <FieldGroup className='px-4 py-3'>
              <Field orientation='horizontal'>
                <FieldContent>
                  <FieldLabel htmlFor='remove-azure-gpt-encryption'>
                    {t('Azure GPT compatibility')}
                  </FieldLabel>
                  <FieldDescription id='remove-azure-gpt-encryption-description'>
                    {t(
                      'Off by default. Removes encrypted history and unsupported GPT-5/6 temperature, repairs stale tool-call IDs, and preserves tool definitions and call associations. Removed encrypted content cannot be recovered.'
                    )}
                  </FieldDescription>
                </FieldContent>
                <Switch
                  id='remove-azure-gpt-encryption'
                  aria-describedby='remove-azure-gpt-encryption-description'
                  disabled={props.disabled}
                  checked={field.value ?? false}
                  onCheckedChange={field.onChange}
                />
              </Field>
            </FieldGroup>
          )}
        />
      )}
    </>
  )
}
