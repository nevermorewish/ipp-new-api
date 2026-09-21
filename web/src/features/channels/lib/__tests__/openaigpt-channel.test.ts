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
import { describe, expect, test } from 'vitest'

import { CHANNEL_TYPE_OPENAI_GPT, CHANNEL_TYPE_OPTIONS } from '../../constants'
import type { Channel } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  channelFormSchema,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  transformChannelToFormDefaults,
} from '../channel-form'

const form = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'GPT upstream',
  type: CHANNEL_TYPE_OPENAI_GPT,
  key: 'test-key',
  models: 'gpt-6-astra',
}

describe('OpenAI-GPT channel settings', () => {
  test('offers OpenAI-GPT independently of the existing Seedance channel', () => {
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: 63,
      label: 'OpenAI-GPT',
    })
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: 62,
      label: 'Seedance',
    })
    expect(channelFormSchema.safeParse(form).success).toBe(true)
  })
  test('defaults compatibility switches to off', () => {
    const payload = transformFormDataToCreatePayload(form).channel
    expect(JSON.parse(payload.settings ?? '{}')).toMatchObject({
      remove_gpt_temperature: false,
      remove_azure_gpt_encryption: false,
    })
  })
  test('saves and reloads both compatibility switches', () => {
    const enabled = {
      ...form,
      remove_gpt_temperature: true,
      remove_azure_gpt_encryption: true,
    }
    const created = transformFormDataToCreatePayload(enabled).channel
    const updated = transformFormDataToUpdatePayload(enabled, 42)
    for (const payload of [created, updated]) {
      expect(JSON.parse(payload.settings ?? '{}')).toMatchObject({
        remove_gpt_temperature: true,
        remove_azure_gpt_encryption: true,
      })
      expect(
        transformChannelToFormDefaults({
          ...payload,
          id: 42,
          channel_info: { is_multi_key: false },
        } as Channel)
      ).toMatchObject({
        remove_gpt_temperature: true,
        remove_azure_gpt_encryption: true,
      })
    }
  })
  test.each([1, 62])(
    'removes GPT-only switches when changing the type to %s',
    (type) => {
      const changed = {
        ...form,
        type,
        remove_gpt_temperature: true,
        remove_azure_gpt_encryption: true,
      }
      for (const payload of [
        transformFormDataToCreatePayload(changed).channel,
        transformFormDataToUpdatePayload(changed, 42),
      ]) {
        const settings = JSON.parse(payload.settings ?? '{}')
        expect(settings).not.toHaveProperty('remove_gpt_temperature')
        expect(settings).not.toHaveProperty('remove_azure_gpt_encryption')
      }
    }
  )
})
