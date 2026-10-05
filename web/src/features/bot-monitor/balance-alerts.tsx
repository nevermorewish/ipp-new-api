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
import { Add01Icon, Delete02Icon, Edit02Icon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { MultiSelect, type Option } from '@/components/multi-select'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from '@/components/ui/empty'
import {
  Field,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Spinner } from '@/components/ui/spinner'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatQuota } from '@/lib/format'

import {
  createBotMonitorBalanceBinding,
  deleteBotMonitorBalanceBinding,
  listBotMonitorBalanceBindings,
  listBotMonitorUserOptions,
  updateBotMonitorBalanceBinding,
  type BotMonitorBalanceBinding,
  type BotMonitorBalanceBindingPayload,
  type BotMonitorRobot,
} from './api'

const emptyBalanceBinding: BotMonitorBalanceBindingPayload = {
  robot_id: 0,
  user_ids: [],
  threshold_amount: 5000,
  check_interval_hours: 1,
  enabled: true,
}

const formatCNYAmount = (amount: number) =>
  new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'CNY',
    maximumFractionDigits: 2,
  }).format(amount)

const formatHours = (hours: number) =>
  Number.isFinite(hours) ? Number(hours.toFixed(2)).toString() : '-'

type BalanceAlertsSectionProps = {
  robots: BotMonitorRobot[]
}

export function BalanceAlertsSection(props: BalanceAlertsSectionProps) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const bindings = useQuery({
    queryKey: ['bot-monitor-balance-bindings'],
    queryFn: listBotMonitorBalanceBindings,
  })
  const users = useQuery({
    queryKey: ['bot-monitor-user-options'],
    queryFn: listBotMonitorUserOptions,
  })
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingBinding, setEditingBinding] =
    useState<BotMonitorBalanceBinding | null>(null)
  const [form, setForm] = useState(emptyBalanceBinding)

  const userOptions = useMemo<Option[]>(
    () =>
      (users.data ?? []).map((user) => {
        const displayName =
          user.display_name && user.display_name !== user.username
            ? ` (${user.display_name})`
            : ''
        return {
          value: String(user.id),
          label: `#${user.id} ${user.username}${displayName} · ${formatQuota(user.quota)}`,
        }
      }),
    [users.data]
  )
  const userNames = useMemo(
    () =>
      new Map(
        (users.data ?? []).map((user) => [
          user.id,
          `#${user.id} ${user.username}`,
        ])
      ),
    [users.data]
  )

  const refresh = () => {
    void client.invalidateQueries({
      queryKey: ['bot-monitor-balance-bindings'],
    })
    void client.invalidateQueries({ queryKey: ['bot-monitor-user-options'] })
  }
  const saveBinding = useMutation({
    mutationFn: () =>
      editingBinding
        ? updateBotMonitorBalanceBinding(editingBinding.id, form)
        : createBotMonitorBalanceBinding(form),
    onSuccess: () => {
      toast.success(t('Balance alert saved'))
      setDialogOpen(false)
      refresh()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const openBinding = (binding?: BotMonitorBalanceBinding) => {
    setEditingBinding(binding ?? null)
    setForm(
      binding
        ? {
            robot_id: binding.robot_id,
            user_ids: binding.user_ids,
            threshold_amount: binding.threshold_amount,
            check_interval_hours: binding.check_interval_hours,
            enabled: binding.enabled,
          }
        : {
            ...emptyBalanceBinding,
            robot_id: props.robots[0]?.id ?? 0,
          }
    )
    setDialogOpen(true)
  }
  const removeBinding = async (binding: BotMonitorBalanceBinding) => {
    if (!window.confirm(t('This balance alert will be permanently deleted.'))) {
      return
    }
    try {
      await deleteBotMonitorBalanceBinding(binding.id)
      toast.success(t('Balance alert deleted'))
      refresh()
    } catch (error) {
      toast.error((error as Error).message)
    }
  }

  return (
    <>
      <section className='flex flex-col gap-4'>
        <div className='flex flex-wrap items-start justify-between gap-4'>
          <div className='flex flex-col gap-1'>
            <h3 className='text-base font-semibold'>{t('Balance alerts')}</h3>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Check selected user balances at the configured interval and notify the configured robot when they fall below the threshold.'
              )}
            </p>
          </div>
          <Button onClick={() => openBinding()} disabled={!props.robots.length}>
            <HugeiconsIcon icon={Add01Icon} data-icon='inline-start' />
            {t('Add balance alert')}
          </Button>
        </div>
        <Card className='overflow-hidden'>
          {(bindings.isLoading || users.isLoading) && (
            <div className='flex min-h-24 items-center justify-center'>
              <Spinner />
            </div>
          )}
          {/* oxlint-disable-next-line no-nested-ternary */}
          {!bindings.isLoading && !users.isLoading && bindings.data?.length ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Robot')}</TableHead>
                  <TableHead>{t('Users')}</TableHead>
                  <TableHead>{t('Balance threshold (CNY)')}</TableHead>
                  <TableHead>{t('Check interval')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {bindings.data.map((binding) => (
                  <TableRow key={binding.id}>
                    <TableCell>
                      {binding.robot_name}{' '}
                      <Badge variant='outline'>
                        {binding.robot_type === 'feishu'
                          ? t('Feishu')
                          : t('WeChat')}
                      </Badge>
                    </TableCell>
                    <TableCell className='max-w-80'>
                      {binding.user_ids
                        .map((id) => userNames.get(id) ?? `#${id}`)
                        .join(', ')}
                    </TableCell>
                    <TableCell>
                      {formatCNYAmount(binding.threshold_amount)}
                    </TableCell>
                    <TableCell>
                      {t('Every {{hours}} hours', {
                        hours: formatHours(binding.check_interval_hours),
                      })}
                    </TableCell>
                    <TableCell>
                      {binding.enabled ? t('Enabled') : t('Disabled')}
                    </TableCell>
                    <TableCell>
                      <div className='flex justify-end gap-1'>
                        <Button
                          variant='ghost'
                          size='icon'
                          onClick={() => openBinding(binding)}
                          title={t('Edit')}
                        >
                          <HugeiconsIcon icon={Edit02Icon} />
                        </Button>
                        <Button
                          variant='ghost'
                          size='icon'
                          onClick={() => void removeBinding(binding)}
                          title={t('Delete')}
                        >
                          <HugeiconsIcon icon={Delete02Icon} />
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : !bindings.isLoading && !users.isLoading ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t('No balance alerts')}</EmptyTitle>
                <EmptyDescription>
                  {t(
                    'Add an alert to monitor selected users with a configurable balance check interval.'
                  )}
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : null}
        </Card>
      </section>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>
              {editingBinding
                ? t('Edit balance alert')
                : t('Add balance alert')}
            </DialogTitle>
            <DialogDescription>
              {t(
                'Select users and a robot. A reminder is sent at the configured interval while any selected balance is below the threshold.'
              )}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{t('Select a robot')}</FieldLabel>
              <Select
                value={String(form.robot_id)}
                onValueChange={(value) =>
                  setForm({ ...form, robot_id: Number(value) })
                }
              >
                <SelectTrigger>
                  <SelectValue placeholder={t('Select a robot')}>
                    {props.robots.find((robot) => robot.id === form.robot_id)
                      ?.name ?? ''}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {props.robots.map((robot) => (
                      <SelectItem key={robot.id} value={String(robot.id)}>
                        {robot.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel>{t('Users')}</FieldLabel>
              <MultiSelect
                options={userOptions}
                selected={form.user_ids.map(String)}
                onChange={(values) =>
                  setForm({ ...form, user_ids: values.map(Number) })
                }
                placeholder={t('Select users...')}
              />
              <FieldDescription>
                {t('The current balance is shown next to each user.')}
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor='balance-threshold-amount'>
                {t('Balance threshold (CNY)')}
              </FieldLabel>
              <InputGroup>
                <InputGroupInput
                  id='balance-threshold-amount'
                  type='number'
                  min={0}
                  step={0.01}
                  value={form.threshold_amount}
                  onChange={(event) =>
                    setForm({
                      ...form,
                      threshold_amount: Number(event.target.value),
                    })
                  }
                />
                <InputGroupAddon>¥ CNY</InputGroupAddon>
              </InputGroup>
              <FieldDescription>
                {t(
                  'Enter a user balance amount in RMB. For example, 5000 means ¥5,000.'
                )}
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor='balance-check-interval-hours'>
                {t('Check interval (hours)')}
              </FieldLabel>
              <Input
                id='balance-check-interval-hours'
                type='number'
                min={0.01}
                step={0.01}
                value={form.check_interval_hours}
                onChange={(event) =>
                  setForm({
                    ...form,
                    check_interval_hours: Number(event.target.value),
                  })
                }
              />
              <FieldDescription>
                {t(
                  'Supports decimal hours; the minimum is 0.01 hours (about 36 seconds).'
                )}
              </FieldDescription>
            </Field>
            <Field orientation='horizontal'>
              <Switch
                checked={form.enabled}
                onCheckedChange={(enabled) => setForm({ ...form, enabled })}
              />
              <FieldLabel>{t('Enabled')}</FieldLabel>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant='outline' onClick={() => setDialogOpen(false)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => saveBinding.mutate()}
              disabled={saveBinding.isPending}
            >
              {saveBinding.isPending && <Spinner data-icon='inline-start' />}
              {t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
