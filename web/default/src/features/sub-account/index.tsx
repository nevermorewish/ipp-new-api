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
import {
  ExternalLink,
  ShieldCheck,
  UsersRound,
  WalletCards,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { useStatus } from '@/hooks/use-status'
import { useAuthStore } from '@/stores/auth-store'

const FEATURE_POINTS = [
  {
    titleKey: 'Centralized enterprise account management',
    descriptionKey:
      'Create and manage independent employee sub-accounts under one enterprise account, making team access clearer and easier to audit.',
    icon: UsersRound,
  },
  {
    titleKey: 'Shared enterprise billing',
    descriptionKey:
      'All sub-account usage is charged directly to the enterprise administrator account for centralized billing.',
    icon: WalletCards,
  },
  {
    titleKey: 'Safer permission boundaries',
    descriptionKey:
      'Keep API access, usage records, and account lifecycle management separated across team members while retaining enterprise-level control.',
    icon: ShieldCheck,
  },
] as const

function getStatusValue(status: unknown, key: string): string {
  if (!status || typeof status !== 'object') return ''
  const record = status as Record<string, unknown>
  const value = record[key]
  return typeof value === 'string' ? value.trim() : ''
}

export function SubAccountPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const { status } = useStatus()

  const isEnterpriseAdmin = user?.type === 1
  const enterpriseName = user?.enterprise_name?.trim() || ''
  const enterpriseSubAccountUrl = getStatusValue(
    status,
    'enterprise_sub_account_url'
  )
  const canOpenManagement = isEnterpriseAdmin && enterpriseSubAccountUrl !== ''

  const handleOpenManagement = () => {
    if (!isEnterpriseAdmin) {
      toast.info(
        t(
          'Please contact an administrator to enable enterprise sub-account features.'
        )
      )
      return
    }
    if (!enterpriseSubAccountUrl) {
      toast.info(t('Enterprise sub-account management link is not configured.'))
      return
    }
    window.open(enterpriseSubAccountUrl, '_blank', 'noopener,noreferrer')
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Enterprise Sub-account')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Enterprise sub-account management supports shared billing, delegated API access, and centralized team account governance.'
            )}
          </p>

          <Card>
            <CardHeader>
              <div className='flex flex-wrap items-start justify-between gap-3'>
                <div className='flex flex-col gap-1'>
                  <CardTitle>
                    {t('Enterprise Sub-account Management')}
                  </CardTitle>
                  <CardDescription>
                    {t(
                      'Designed for enterprise administrators who need to manage employee accounts, shared billing, and usage responsibility from one place.'
                    )}
                  </CardDescription>
                </div>
                {isEnterpriseAdmin ? (
                  <Badge variant='secondary'>
                    {enterpriseName
                      ? t('Enterprise: {{name}}', { name: enterpriseName })
                      : t('Enterprise administrator')}
                  </Badge>
                ) : (
                  <Badge variant='outline'>{t('Not enabled')}</Badge>
                )}
              </div>
            </CardHeader>
            <CardContent>
              <div className='grid gap-3 md:grid-cols-3'>
                {FEATURE_POINTS.map((point) => (
                  <div
                    key={point.titleKey}
                    className='flex min-h-[132px] flex-col gap-3 rounded-lg border p-4'
                  >
                    <point.icon className='text-muted-foreground size-5' />
                    <div className='flex flex-col gap-1'>
                      <h3 className='text-sm font-medium'>
                        {t(point.titleKey)}
                      </h3>
                      <p className='text-muted-foreground text-sm'>
                        {t(point.descriptionKey)}
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            </CardContent>
          </Card>

          {!isEnterpriseAdmin && (
            <Alert>
              <ShieldCheck />
              <AlertTitle>
                {t('Enterprise sub-account access is not enabled')}
              </AlertTitle>
              <AlertDescription>
                {t(
                  'Please contact an administrator to enable enterprise sub-account features.'
                )}
              </AlertDescription>
            </Alert>
          )}

          {isEnterpriseAdmin && !enterpriseSubAccountUrl && (
            <Alert>
              <ShieldCheck />
              <AlertTitle>{t('Management link is not configured')}</AlertTitle>
              <AlertDescription>
                {t(
                  'Ask a system administrator to configure the enterprise sub-account management link in system settings.'
                )}
              </AlertDescription>
            </Alert>
          )}

          <div className='border-primary/20 bg-primary/5 flex flex-col gap-4 rounded-lg border p-5 sm:flex-row sm:items-center sm:justify-between'>
            <div className='flex flex-col gap-1'>
              <h3 className='text-base font-semibold'>
                {t('Open enterprise account management')}
              </h3>
              <p className='text-muted-foreground text-sm'>
                {isEnterpriseAdmin
                    ? t(
                      'Open the enterprise management console to create and manage sub-accounts.'
                    )
                  : t(
                      'Please contact an administrator to enable enterprise sub-account features.'
                    )}
              </p>
            </div>
            <Button
              type='button'
              size='lg'
              className='w-full sm:w-auto'
              disabled={!canOpenManagement}
              onClick={handleOpenManagement}
            >
              <ExternalLink data-icon='inline-start' />
              {t('Open enterprise account management')}
            </Button>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
