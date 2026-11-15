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

import {
  CHANNEL_TYPE_ANTHROPIC_OFFICIAL,
  CHANNEL_TYPE_OPTIONS,
} from '../../constants'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformFormDataToCreatePayload,
} from '../channel-form'

const form = {
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'Anthropic official pool',
  type: CHANNEL_TYPE_ANTHROPIC_OFFICIAL,
  key: 'sk-ant-test-key',
  models: 'claude-haiku-5-5',
}

describe('AnthropicOfficial channel settings', () => {
  test('offers AnthropicOfficial under its backend id', () => {
    expect(CHANNEL_TYPE_ANTHROPIC_OFFICIAL).toBe(67)
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: 67,
      label: 'AnthropicOfficial',
    })
  })

  test('saves the Anthropic request switches like an Anthropic channel', () => {
    const payload = transformFormDataToCreatePayload({
      ...form,
      allow_inference_geo: true,
      claude_beta_query: true,
    }).channel

    expect(JSON.parse(payload.settings ?? '{}')).toMatchObject({
      allow_inference_geo: true,
      claude_beta_query: true,
    })
  })
})
