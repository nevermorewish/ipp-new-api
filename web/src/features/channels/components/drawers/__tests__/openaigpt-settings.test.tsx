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
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useForm } from 'react-hook-form'
import { describe, expect, test } from 'vitest'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  type ChannelFormValues,
} from '../../../lib/channel-form'
import { OpenAIGPTSettings } from '../openaigpt-settings'

function SettingsForm(props: { channelType?: number; disabled?: boolean }) {
  const form = useForm<ChannelFormValues>({
    defaultValues: CHANNEL_FORM_DEFAULT_VALUES,
  })
  return (
    <OpenAIGPTSettings
      control={form.control}
      channelType={props.channelType ?? 63}
      disabled={props.disabled}
    />
  )
}

describe('OpenAI-GPT compatibility controls', () => {
  test('has labelled switches with descriptions that can be toggled by keyboard and pointer', async () => {
    const user = userEvent.setup()
    render(<SettingsForm />)
    const temperature = screen.getByRole('switch', {
      name: 'Remove temperature for GPT-5 and GPT-6',
    })
    const azure = screen.getByRole('switch', {
      name: 'Azure GPT compatibility',
    })
    expect(temperature).not.toBeChecked()
    expect(azure).not.toBeChecked()
    expect(azure).toHaveAccessibleDescription()
    await user.click(azure)
    expect(azure).toBeChecked()
    temperature.focus()
    await user.keyboard(' ')
    expect(temperature).toBeChecked()
  })
  test('locks both settings when sensitive settings are locked', async () => {
    const user = userEvent.setup()
    render(<SettingsForm disabled />)
    for (const control of screen.getAllByRole('switch')) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      await user.click(control)
      expect(control).not.toBeChecked()
    }
  })
  test.each([1, 62])(
    'does not display GPT settings for channel type %s',
    (channelType) => {
      render(<SettingsForm channelType={channelType} />)
      expect(screen.queryByRole('switch')).not.toBeInTheDocument()
    }
  )
})
