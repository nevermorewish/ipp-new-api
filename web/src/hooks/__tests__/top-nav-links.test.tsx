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
import { cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { useTopNavLinks } from '../use-top-nav-links'

beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

it.each([
  [undefined, true, '/docs/'],
  ['https://docs.newapi.pro', true, '/docs/'],
  ['https://custom.example/guide', true, 'https://custom.example/guide'],
  [undefined, false, undefined],
])(
  'docs link %s with visibility %s navigates to %s',
  (docsLink, visible, expected) => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: Infinity } },
    })
    client.setQueryData(['status'], {
      docs_link: docsLink,
      HeaderNavModules: JSON.stringify({ docs: visible }),
    })
    function Wrapper({ children }: { children: ReactNode }) {
      return (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      )
    }
    const { result, unmount } = renderHook(() => useTopNavLinks(), {
      wrapper: Wrapper,
    })
    const docs = result.current.find((link) => link.title === 'Docs')
    expect(docs?.href).toBe(expected)
    if (expected) expect(docs?.external).toBe(true)
    unmount()
    client.clear()
  }
)
