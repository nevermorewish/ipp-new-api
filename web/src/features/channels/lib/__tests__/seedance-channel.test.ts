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
import { describe, expect, it } from 'vitest'

import { CHANNEL_TYPE_OPTIONS, CHANNEL_TYPES } from '../../constants'
import { getChannelTypeIcon, getChannelTypeLabel } from '../channel-utils'

describe('Seedance channel metadata', () => {
  it('offers SeedanceAPI independently from existing channel types', () => {
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: 66,
      label: 'SeedanceAPI',
    })
    expect(getChannelTypeLabel(66)).toBe('SeedanceAPI')
    expect(getChannelTypeIcon(66)).toBe('Doubao')
    expect(CHANNEL_TYPES[59]).toBe('Sub2API')
    expect(CHANNEL_TYPES[62]).toBe('vLLM')
  })
  it('is selectable with the Seedance label and Doubao icon', () => {
    expect(CHANNEL_TYPES[64]).toBe('Seedance')
    expect(CHANNEL_TYPE_OPTIONS).toContainEqual({
      value: 64,
      label: 'Seedance',
    })
    expect(getChannelTypeLabel(64)).toBe('Seedance')
    expect(getChannelTypeIcon(64)).toBe('Doubao')
  })
})
