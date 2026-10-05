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
/*
Copyright (C) 2023-2026 huanxing

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.
*/
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

import {
  createBotMonitorLatencyBinding,
  deleteBotMonitorLatencyBinding,
  listBotMonitorLatencyBindings,
  listBotMonitorLatencyModelOptions,
  updateBotMonitorLatencyBinding,
  type BotMonitorLatencyBinding,
  type BotMonitorLatencyBindingPayload,
  type BotMonitorRobot,
} from './api'

const DEFAULT_TIMEOUT_SECONDS = 30
const DEFAULT_INTERVAL_SECONDS = 300
const emptyLatencyBinding: BotMonitorLatencyBindingPayload = {
  name: '',
  robot_id: 0,
  model_names: [],
  first_token_timeout_seconds: DEFAULT_TIMEOUT_SECONDS,
  min_interval_seconds: DEFAULT_INTERVAL_SECONDS,
  enabled: true,
}

type LatencyAlertsSectionProps = {
  robots: BotMonitorRobot[]
}

export function LatencyAlertsSection({ robots }: LatencyAlertsSectionProps) {
  const { t } = useTranslation()
  const client = useQueryClient()
  const bindings = useQuery({
    queryKey: ['bot-monitor-latency-bindings'],
    queryFn: listBotMonitorLatencyBindings,
  })
  const modelOptions = useQuery({
    queryKey: ['bot-monitor-latency-model-options'],
    queryFn: listBotMonitorLatencyModelOptions,
  })
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingBinding, setEditingBinding] =
    useState<BotMonitorLatencyBinding | null>(null)
  const [form, setForm] = useState(emptyLatencyBinding)

  const options = useMemo<Option[]>(
    () =>
      (modelOptions.data ?? []).map((name) => ({ value: name, label: name })),
    [modelOptions.data]
  )
  const refresh = () => {
    void client.invalidateQueries({
      queryKey: ['bot-monitor-latency-bindings'],
    })
  }
  const saveBinding = useMutation({
    mutationFn: () =>
      editingBinding
        ? updateBotMonitorLatencyBinding(editingBinding.id, form)
        : createBotMonitorLatencyBinding(form),
    onSuccess: () => {
      toast.success(t('Latency alert saved'))
      setDialogOpen(false)
      refresh()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const openBinding = (binding?: BotMonitorLatencyBinding) => {
    setEditingBinding(binding ?? null)
    setForm(
      binding
        ? {
            name: binding.name,
            robot_id: binding.robot_id,
            model_names: binding.model_names,
            first_token_timeout_seconds: binding.first_token_timeout_seconds,
            min_interval_seconds: binding.min_interval_seconds,
            enabled: binding.enabled,
          }
        : {
            ...emptyLatencyBinding,
            robot_id: robots[0]?.id ?? 0,
          }
    )
    setDialogOpen(true)
  }
  const removeBinding = async (binding: BotMonitorLatencyBinding) => {
    if (!window.confirm(t('This latency alert will be permanently deleted.'))) {
      return
    }
    try {
      await deleteBotMonitorLatencyBinding(binding.id)
      toast.success(t('Latency alert deleted'))
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
            <h3 className='text-base font-semibold'>
              {t('First-token alerts')}
            </h3>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Notify a robot when a selected model does not return its first token before the configured timeout.'
              )}
            </p>
          </div>
          <Button onClick={() => openBinding()} disabled={!robots.length}>
            <HugeiconsIcon icon={Add01Icon} data-icon='inline-start' />
            {t('Add first-token alert')}
          </Button>
        </div>
        <Card className='overflow-hidden'>
          {bindings.isLoading && (
            <div className='flex min-h-24 items-center justify-center'>
              <Spinner />
            </div>
          )}
          {/* oxlint-disable-next-line no-nested-ternary */}
          {!bindings.isLoading && bindings.data?.length ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Name')}</TableHead>
                  <TableHead>{t('Robot')}</TableHead>
                  <TableHead>{t('Models')}</TableHead>
                  <TableHead>{t('First-token timeout')}</TableHead>
                  <TableHead>{t('Alert interval')}</TableHead>
                  <TableHead>{t('Status')}</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {bindings.data.map((binding) => (
                  <TableRow key={binding.id}>
                    <TableCell>{binding.name}</TableCell>
                    <TableCell>
                      {binding.robot_name}{' '}
                      <Badge variant='outline'>
                        {binding.robot_type === 'feishu'
                          ? t('Feishu')
                          : t('WeChat')}
                      </Badge>
                    </TableCell>
                    <TableCell className='max-w-80 truncate'>
                      {binding.model_names.join(', ')}
                    </TableCell>
                    <TableCell>
                      {binding.first_token_timeout_seconds}s
                    </TableCell>
                    <TableCell>{binding.min_interval_seconds}s</TableCell>
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
          ) : !bindings.isLoading ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>{t('No first-token alerts')}</EmptyTitle>
                <EmptyDescription>
                  {t(
                    'Add an alert to monitor first-token latency for selected models.'
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
                ? t('Edit first-token alert')
                : t('Add first-token alert')}
            </DialogTitle>
            <DialogDescription>
              {t('Select models and a robot for first-token timeout alerts.')}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{t('Name')}</FieldLabel>
              <Input
                value={form.name}
                onChange={(event) =>
                  setForm({ ...form, name: event.target.value })
                }
              />
            </Field>
            <Field>
              <FieldLabel>{t('Select a robot')}</FieldLabel>
              <select
                className='border-input bg-background h-9 w-full rounded-md border px-3 text-sm'
                value={String(form.robot_id)}
                onChange={(event) =>
                  setForm({ ...form, robot_id: Number(event.target.value) })
                }
              >
                <option value='0'>{t('Select a robot')}</option>
                {robots.map((robot) => (
                  <option key={robot.id} value={robot.id}>
                    {robot.name}
                  </option>
                ))}
              </select>
            </Field>
            <Field>
              <FieldLabel>{t('Models')}</FieldLabel>
              <MultiSelect
                options={options}
                selected={form.model_names}
                onChange={(values) => setForm({ ...form, model_names: values })}
                placeholder={
                  modelOptions.isLoading
                    ? t('Loading models...')
                    : t('Select models...')
                }
              />
              <FieldDescription>
                {t('Only streaming requests for these models are checked.')}
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel>{t('First-token timeout (seconds)')}</FieldLabel>
              <Input
                type='number'
                min={1}
                max={86400}
                value={form.first_token_timeout_seconds}
                onChange={(event) =>
                  setForm({
                    ...form,
                    first_token_timeout_seconds: Number(event.target.value),
                  })
                }
              />
              <FieldDescription>
                {t(
                  'Default is 30 seconds. An alert is sent when no first token arrives before this limit.'
                )}
              </FieldDescription>
            </Field>
            <Field>
              <FieldLabel>{t('Alert interval (seconds)')}</FieldLabel>
              <Input
                type='number'
                min={0}
                max={86400}
                value={form.min_interval_seconds}
                onChange={(event) =>
                  setForm({
                    ...form,
                    min_interval_seconds: Number(event.target.value),
                  })
                }
              />
              <FieldDescription>
                {t(
                  'Suppress repeated alerts for the same model during this interval; use 0 to disable suppression.'
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
