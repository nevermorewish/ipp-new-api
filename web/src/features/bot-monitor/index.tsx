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
  Add01Icon,
  BotIcon,
  Delete02Icon,
  Edit02Icon,
  Refresh01Icon,
  Search01Icon,
  SentIcon,
} from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
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
import { Textarea } from '@/components/ui/textarea'

import {
  createBotMonitorBinding,
  createBotMonitorRobot,
  deleteBotMonitorBinding,
  deleteBotMonitorRobot,
  listBotMonitorBindings,
  listBotMonitorChannelOptions,
  listBotMonitorRobotConversations,
  queryBotMonitorRobotConversations,
  listBotMonitorRobots,
  testBotMonitorRobot,
  updateBotMonitorBinding,
  updateBotMonitorRobot,
  type BotMonitorBinding,
  type BotMonitorBindingPayload,
  type BotMonitorRobot,
  type BotMonitorRobotPayload,
  type BotMonitorConversationOption,
} from './api'
import { BalanceAlertsSection } from './balance-alerts'
import { LatencyAlertsSection } from './latency-alerts'

const emptyRobot: BotMonitorRobotPayload = {
  name: '',
  type: 'wechat',
  enabled: true,
  feishu_app_id: '',
  feishu_app_secret: '',
  feishu_chat_ids: '',
  api_url: '',
  conversation_id: '',
  conversation_nickname: '',
}
const emptyBinding: BotMonitorBindingPayload = {
  name: '',
  robot_id: 0,
  channel_ids: [],
  min_interval_seconds: 300,
  trigger_status_codes: '400-499,500-599',
  enabled: true,
}
const time = (value: number) =>
  value ? new Date(value * 1000).toLocaleString() : '-'

function MonitorSection({
  title,
  description,
  actions,
  children,
}: {
  title: string
  description: string
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <section className='flex flex-col gap-4'>
      <div className='flex flex-wrap items-start justify-between gap-4'>
        <div className='flex flex-col gap-1'>
          <h3 className='text-base font-semibold'>{title}</h3>
          <p className='text-muted-foreground text-sm'>{description}</p>
        </div>
        {actions}
      </div>
      {children}
    </section>
  )
}

export function BotMonitorPage() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const robots = useQuery({
    queryKey: ['bot-monitor-robots'],
    queryFn: listBotMonitorRobots,
  })
  const bindings = useQuery({
    queryKey: ['bot-monitor-bindings'],
    queryFn: listBotMonitorBindings,
  })
  const channels = useQuery({
    queryKey: ['bot-monitor-channel-options'],
    queryFn: listBotMonitorChannelOptions,
  })
  const sortedChannels = useMemo(
    () => [...(channels.data ?? [])].sort((a, b) => a.id - b.id),
    [channels.data]
  )
  const [robotDialog, setRobotDialog] = useState(false)
  const [editingRobot, setEditingRobot] = useState<BotMonitorRobot | null>(null)
  const [robotForm, setRobotForm] = useState(emptyRobot)
  const [conversationOptions, setConversationOptions] = useState<
    BotMonitorConversationOption[]
  >([])
  const [conversationLoading, setConversationLoading] = useState(false)
  const [bindingDialog, setBindingDialog] = useState(false)
  const [editingBinding, setEditingBinding] =
    useState<BotMonitorBinding | null>(null)
  const [bindingForm, setBindingForm] = useState(emptyBinding)
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ['bot-monitor-robots'] })
    void client.invalidateQueries({ queryKey: ['bot-monitor-bindings'] })
    void client.invalidateQueries({
      queryKey: ['bot-monitor-balance-bindings'],
    })
    void client.invalidateQueries({
      queryKey: ['bot-monitor-latency-bindings'],
    })
    void client.invalidateQueries({ queryKey: ['bot-monitor-user-options'] })
  }
  const saveRobot = useMutation({
    mutationFn: () =>
      editingRobot
        ? updateBotMonitorRobot(editingRobot.id, robotForm)
        : createBotMonitorRobot(robotForm),
    onSuccess: () => {
      toast.success(t('Robot saved'))
      setRobotDialog(false)
      refresh()
    },
    onError: (error: Error) => toast.error(error.message),
  })
  const saveBinding = useMutation({
    mutationFn: () =>
      editingBinding
        ? updateBotMonitorBinding(editingBinding.id, bindingForm)
        : createBotMonitorBinding(bindingForm),
    onSuccess: () => {
      toast.success(t('Binding saved'))
      setBindingDialog(false)
      refresh()
    },
    onError: (error: Error) => toast.error(error.message),
  })
  const openRobot = (robot?: BotMonitorRobot) => {
    setEditingRobot(robot ?? null)
    setConversationOptions([])
    setRobotForm(
      robot
        ? {
            ...emptyRobot,
            name: robot.name,
            type: robot.type,
            enabled: robot.enabled,
            api_url: '',
            conversation_id: robot.conversation_id,
            conversation_nickname: robot.conversation_nickname,
          }
        : emptyRobot
    )
    setRobotDialog(true)
  }
  const queryConversations = async () => {
    if (robotForm.type !== 'wechat') return
    setConversationLoading(true)
    try {
      const options = editingRobot
        ? await listBotMonitorRobotConversations(editingRobot.id)
        : await queryBotMonitorRobotConversations(robotForm.api_url ?? '')
      setConversationOptions(options)
      if (options.length === 0) {
        toast.info(t('No conversations found'))
      }
    } catch (error) {
      toast.error((error as Error).message)
    } finally {
      setConversationLoading(false)
    }
  }
  const openBinding = (binding?: BotMonitorBinding) => {
    const defaultRobot = robots.data?.[0]
    setEditingBinding(binding ?? null)
    setBindingForm(
      binding
        ? {
            name: binding.name,
            robot_id: binding.robot_id,
            channel_ids: binding.channel_ids,
            min_interval_seconds: binding.min_interval_seconds,
            trigger_status_codes: binding.trigger_status_codes,
            enabled: binding.enabled,
          }
        : {
            ...emptyBinding,
            robot_id: defaultRobot?.id ?? 0,
            channel_ids:
              defaultRobot?.type === 'feishu'
                ? sortedChannels.map((channel) => channel.id)
                : [],
          }
    )
    setBindingDialog(true)
  }
  const removeRobot = async (robot: BotMonitorRobot) => {
    if (!window.confirm(t('This robot will be permanently deleted.'))) return
    try {
      await deleteBotMonitorRobot(robot.id)
      toast.success(t('Robot deleted'))
      refresh()
    } catch (error) {
      toast.error((error as Error).message)
    }
  }
  const removeBinding = async (binding: BotMonitorBinding) => {
    if (!window.confirm(t('This binding will be permanently deleted.'))) return
    try {
      await deleteBotMonitorBinding(binding.id)
      refresh()
    } catch (error) {
      toast.error((error as Error).message)
    }
  }
  const testRobot = async (robot: BotMonitorRobot) => {
    try {
      await testBotMonitorRobot(robot.id)
      toast.success(t('Test notification sent'))
      refresh()
    } catch (error) {
      toast.error((error as Error).message)
    }
  }
  const selectBindingRobot = (robotId: number) => {
    const robot = robots.data?.find((item) => item.id === robotId)
    let channelIds = bindingForm.channel_ids
    if (!editingBinding) {
      channelIds =
        robot?.type === 'feishu'
          ? sortedChannels.map((channel) => channel.id)
          : []
    }
    setBindingForm({
      ...bindingForm,
      robot_id: robotId,
      channel_ids: channelIds,
    })
  }
  const selectAllChannels = () => {
    setBindingForm({
      ...bindingForm,
      channel_ids: sortedChannels.map((channel) => channel.id),
    })
  }
  const invertChannelSelection = () => {
    const selected = new Set(bindingForm.channel_ids)
    setBindingForm({
      ...bindingForm,
      channel_ids: sortedChannels
        .filter((channel) => !selected.has(channel.id))
        .map((channel) => channel.id),
    })
  }
  return (
    <>
      <SectionPageLayout>
        <SectionPageLayout.Title>
          <span className='inline-flex items-center gap-2'>
            <HugeiconsIcon icon={BotIcon} className='text-primary' />
            {t('Robot monitoring')}
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <Button
            variant='outline'
            size='sm'
            onClick={() => {
              void robots.refetch()
              void bindings.refetch()
              void channels.refetch()
              void client.invalidateQueries({
                queryKey: ['bot-monitor-balance-bindings'],
              })
              void client.invalidateQueries({
                queryKey: ['bot-monitor-latency-bindings'],
              })
              void client.invalidateQueries({
                queryKey: ['bot-monitor-user-options'],
              })
            }}
            disabled={
              robots.isFetching || bindings.isFetching || channels.isFetching
            }
          >
            {(robots.isFetching ||
              bindings.isFetching ||
              channels.isFetching) && <Spinner data-icon='inline-start' />}
            {!robots.isFetching &&
              !bindings.isFetching &&
              !channels.isFetching && (
                <HugeiconsIcon icon={Refresh01Icon} data-icon='inline-start' />
              )}
            {t('Refresh')}
          </Button>
          <Button size='sm' onClick={() => openRobot()}>
            <HugeiconsIcon icon={Add01Icon} data-icon='inline-start' />
            {t('Add robot')}
          </Button>
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <div className='flex flex-col gap-8'>
            <MonitorSection
              title={t('Robot')}
              description={t(
                'Configure the robots that receive channel error alerts.'
              )}
            >
              <Card className='overflow-hidden'>
                {robots.isLoading && (
                  <div className='flex min-h-24 items-center justify-center'>
                    <Spinner />
                  </div>
                )}
                {/* The table and empty state share one mutually exclusive branch. */}
                {/* oxlint-disable-next-line no-nested-ternary */}
                {!robots.isLoading && robots.data?.length ? (
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('Name')}</TableHead>
                        <TableHead>{t('Robot type')}</TableHead>
                        <TableHead>{t('Destination')}</TableHead>
                        <TableHead>{t('Status')}</TableHead>
                        <TableHead>{t('Last sent')}</TableHead>
                        <TableHead />
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {robots.data.map((robot) => (
                        <TableRow key={robot.id}>
                          <TableCell className='font-medium'>
                            {robot.name}
                          </TableCell>
                          <TableCell>
                            <Badge variant='outline'>
                              {robot.type === 'feishu'
                                ? t('Feishu')
                                : t('WeChat')}
                            </Badge>
                          </TableCell>
                          <TableCell>
                            {robot.type === 'feishu' &&
                              t('{{count}} chat(s)', {
                                count: robot.feishu_chat_count,
                              })}
                            {robot.type !== 'feishu' &&
                              robot.conversation_nickname && (
                                <div className='flex flex-col gap-1'>
                                  <span className='font-medium'>
                                    {robot.conversation_nickname}
                                  </span>
                                  <span>
                                    {t('Conversation {{id}}', {
                                      id: robot.conversation_id,
                                    })}
                                  </span>
                                </div>
                              )}
                            {robot.type !== 'feishu' &&
                              !robot.conversation_nickname &&
                              t('Conversation {{id}}', {
                                id: robot.conversation_id,
                              })}
                          </TableCell>
                          <TableCell>
                            {robot.enabled ? (
                              <Badge>{t('Enabled')}</Badge>
                            ) : (
                              <Badge variant='secondary'>{t('Disabled')}</Badge>
                            )}
                            {robot.last_status === 'failed' && (
                              <p className='text-destructive text-xs'>
                                {robot.last_error}
                              </p>
                            )}
                          </TableCell>
                          <TableCell>{time(robot.last_sent_at)}</TableCell>
                          <TableCell>
                            <div className='flex justify-end gap-1'>
                              <Button
                                variant='ghost'
                                size='icon'
                                onClick={() => void testRobot(robot)}
                                title={t('Send test')}
                              >
                                <HugeiconsIcon icon={SentIcon} />
                              </Button>
                              <Button
                                variant='ghost'
                                size='icon'
                                onClick={() => openRobot(robot)}
                                title={t('Edit')}
                              >
                                <HugeiconsIcon icon={Edit02Icon} />
                              </Button>
                              <Button
                                variant='ghost'
                                size='icon'
                                onClick={() => void removeRobot(robot)}
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
                ) : !robots.isLoading ? (
                  <Empty>
                    <EmptyHeader>
                      <EmptyTitle>{t('No monitor robots')}</EmptyTitle>
                      <EmptyDescription>
                        {t(
                          'Add a Feishu or WeChat robot before creating a channel binding.'
                        )}
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                ) : null}
              </Card>
            </MonitorSection>
            <MonitorSection
              title={t('Channel bindings')}
              description={t(
                'Choose which channels send alerts to each robot.'
              )}
              actions={
                <Button
                  onClick={() => openBinding()}
                  disabled={!robots.data?.length}
                >
                  <HugeiconsIcon icon={Add01Icon} data-icon='inline-start' />
                  {t('Add binding')}
                </Button>
              }
            >
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
                        <TableHead>{t('Channels')}</TableHead>
                        <TableHead>{t('Alert rules')}</TableHead>
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
                          <TableCell>
                            {binding.channel_ids.join(', ')}
                          </TableCell>
                          <TableCell>
                            {binding.trigger_status_codes ||
                              t('All status codes')}{' '}
                            / {binding.min_interval_seconds}s
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
                ) : !bindings.isLoading ? (
                  <Empty>
                    <EmptyHeader>
                      <EmptyTitle>{t('No channel bindings')}</EmptyTitle>
                    </EmptyHeader>
                  </Empty>
                ) : null}
              </Card>
            </MonitorSection>
            <LatencyAlertsSection robots={robots.data ?? []} />
            <BalanceAlertsSection robots={robots.data ?? []} />
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
      <Dialog open={robotDialog} onOpenChange={setRobotDialog}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>
              {editingRobot ? t('Edit robot') : t('Add robot')}
            </DialogTitle>
            <DialogDescription>
              {t('Configure a Feishu or WeChat robot.')}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{t('Name')}</FieldLabel>
              <Input
                value={robotForm.name}
                onChange={(event) =>
                  setRobotForm({ ...robotForm, name: event.target.value })
                }
              />
            </Field>
            <Field>
              <FieldLabel>{t('Robot type')}</FieldLabel>
              <Select
                value={robotForm.type}
                onValueChange={(value) =>
                  setRobotForm({
                    ...robotForm,
                    type: value as 'feishu' | 'wechat',
                  })
                }
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value='feishu'>{t('Feishu')}</SelectItem>
                    <SelectItem value='wechat'>{t('WeChat')}</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            {robotForm.type === 'feishu' ? (
              <>
                <Field>
                  <FieldLabel>{t('Feishu App ID')}</FieldLabel>
                  <Input
                    value={robotForm.feishu_app_id}
                    onChange={(event) =>
                      setRobotForm({
                        ...robotForm,
                        feishu_app_id: event.target.value,
                      })
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel>{t('Feishu App Secret')}</FieldLabel>
                  <Input
                    type='password'
                    value={robotForm.feishu_app_secret}
                    placeholder={
                      editingRobot
                        ? t('Leave blank to keep current value')
                        : undefined
                    }
                    onChange={(event) =>
                      setRobotForm({
                        ...robotForm,
                        feishu_app_secret: event.target.value,
                      })
                    }
                  />
                </Field>
                <Field>
                  <FieldLabel>{t('Feishu chat IDs')}</FieldLabel>
                  <Textarea
                    value={robotForm.feishu_chat_ids}
                    placeholder={t('One chat ID per line or comma-separated')}
                    onChange={(event) =>
                      setRobotForm({
                        ...robotForm,
                        feishu_chat_ids: event.target.value,
                      })
                    }
                  />
                </Field>
              </>
            ) : (
              <>
                <Field>
                  <FieldLabel>{t('WeChat API URL')}</FieldLabel>
                  <Input
                    value={robotForm.api_url}
                    placeholder={
                      editingRobot
                        ? t('Leave blank to keep current value')
                        : t('Enter the WeChat ExecCommand endpoint')
                    }
                    onChange={(event) =>
                      setRobotForm({
                        ...robotForm,
                        api_url: event.target.value,
                      })
                    }
                  />
                  <FieldDescription>
                    {t('ExecCommand endpoint used to send WeChat messages.')}
                  </FieldDescription>
                </Field>
                <Field>
                  <FieldLabel>{t('Conversation ID')}</FieldLabel>
                  <div className='flex items-center gap-2'>
                    <Input
                      value={robotForm.conversation_id}
                      placeholder='R:10872034605494222'
                      onChange={(event) =>
                        setRobotForm({
                          ...robotForm,
                          conversation_id: event.target.value,
                          conversation_nickname: '',
                        })
                      }
                    />
                    <Button
                      type='button'
                      variant='outline'
                      onClick={() => void queryConversations()}
                      disabled={conversationLoading}
                    >
                      {conversationLoading ? (
                        <Spinner data-icon='inline-start' />
                      ) : (
                        <HugeiconsIcon
                          icon={Search01Icon}
                          data-icon='inline-start'
                        />
                      )}
                      {conversationLoading
                        ? t('Querying...')
                        : t('Query conversations')}
                    </Button>
                  </div>
                  {conversationOptions.length > 0 && (
                    <Select
                      items={conversationOptions.map((conversation) => ({
                        label: `${conversation.nickname || t('Unnamed conversation')} - ${conversation.conversation_id}`,
                        value: conversation.conversation_id,
                      }))}
                      value={robotForm.conversation_id}
                      onValueChange={(conversationId) => {
                        if (!conversationId) return
                        setRobotForm({
                          ...robotForm,
                          conversation_id: conversationId,
                          conversation_nickname:
                            conversationOptions.find(
                              (conversation) =>
                                conversation.conversation_id === conversationId
                            )?.nickname ?? '',
                        })
                      }}
                    >
                      <SelectTrigger>
                        <SelectValue placeholder={t('Select a conversation')} />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          {conversationOptions.map((conversation) => (
                            <SelectItem
                              key={conversation.conversation_id}
                              value={conversation.conversation_id}
                            >
                              {conversation.nickname ||
                                t('Unnamed conversation')}{' '}
                              - {conversation.conversation_id}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  )}
                  <FieldDescription>
                    {t('Private or group conversation that receives alerts.')}
                  </FieldDescription>
                </Field>
              </>
            )}
            <Field orientation='horizontal'>
              <Switch
                checked={robotForm.enabled}
                onCheckedChange={(enabled) =>
                  setRobotForm({ ...robotForm, enabled })
                }
              />
              <FieldLabel>{t('Enabled')}</FieldLabel>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant='outline' onClick={() => setRobotDialog(false)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => saveRobot.mutate()}
              disabled={saveRobot.isPending}
            >
              {saveRobot.isPending && <Spinner />}
              {t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <Dialog open={bindingDialog} onOpenChange={setBindingDialog}>
        <DialogContent className='max-h-[90vh] overflow-y-auto sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>
              {editingBinding ? t('Edit binding') : t('Add binding')}
            </DialogTitle>
            <DialogDescription>
              {t('Bind one or more channels to a robot.')}
            </DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{t('Name')}</FieldLabel>
              <Input
                value={bindingForm.name}
                onChange={(event) =>
                  setBindingForm({ ...bindingForm, name: event.target.value })
                }
              />
            </Field>
            <Field>
              <FieldLabel>{t('Select a robot')}</FieldLabel>
              <Select
                value={String(bindingForm.robot_id)}
                onValueChange={(value) => selectBindingRobot(Number(value))}
              >
                <SelectTrigger>
                  <SelectValue placeholder={t('Select a robot')}>
                    {robots.data?.find(
                      (robot) => robot.id === bindingForm.robot_id
                    )?.name ?? ''}
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {robots.data?.map((robot) => (
                      <SelectItem key={robot.id} value={String(robot.id)}>
                        {robot.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <FieldLabel>{t('Channels')}</FieldLabel>
                <div className='flex items-center gap-2'>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={selectAllChannels}
                    disabled={!channels.data?.length}
                  >
                    {t('Select all')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={invertChannelSelection}
                    disabled={!channels.data?.length}
                  >
                    {t('Invert selection')}
                  </Button>
                </div>
              </div>
              <FieldGroup className='max-h-48 overflow-y-auto rounded-md border p-3'>
                {sortedChannels.map((channel) => (
                  <label
                    key={channel.id}
                    className='flex items-center gap-2 text-sm'
                  >
                    <Checkbox
                      checked={bindingForm.channel_ids.includes(channel.id)}
                      onCheckedChange={(checked) =>
                        setBindingForm({
                          ...bindingForm,
                          channel_ids: checked
                            ? [...bindingForm.channel_ids, channel.id]
                            : bindingForm.channel_ids.filter(
                                (id) => id !== channel.id
                              ),
                        })
                      }
                    />
                    {channel.id} - {channel.name}
                  </label>
                ))}
              </FieldGroup>
            </Field>
            <Field>
              <FieldLabel>{t('Minimum interval (seconds)')}</FieldLabel>
              <Input
                type='number'
                min={0}
                max={86400}
                value={bindingForm.min_interval_seconds}
                onChange={(event) =>
                  setBindingForm({
                    ...bindingForm,
                    min_interval_seconds: Number(event.target.value),
                  })
                }
              />
            </Field>
            <Field>
              <FieldLabel>{t('Trigger status codes')}</FieldLabel>
              <Input
                placeholder='400-499,500-599'
                value={bindingForm.trigger_status_codes}
                onChange={(event) =>
                  setBindingForm({
                    ...bindingForm,
                    trigger_status_codes: event.target.value,
                  })
                }
              />
              <FieldDescription>
                {t('Leave blank to match all status codes.')}
              </FieldDescription>
            </Field>
            <Field orientation='horizontal'>
              <Switch
                checked={bindingForm.enabled}
                onCheckedChange={(enabled) =>
                  setBindingForm({ ...bindingForm, enabled })
                }
              />
              <FieldLabel>{t('Enabled')}</FieldLabel>
            </Field>
          </FieldGroup>
          <DialogFooter>
            <Button variant='outline' onClick={() => setBindingDialog(false)}>
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => saveBinding.mutate()}
              disabled={saveBinding.isPending}
            >
              {saveBinding.isPending && <Spinner />}
              {t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
