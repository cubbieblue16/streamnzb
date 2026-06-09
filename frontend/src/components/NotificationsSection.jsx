import React, { useEffect, useMemo, useState } from 'react'
import { Loader2, Save, Plus, Trash2, Send, CheckCircle2, XCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/card'
import { apiFetch } from '../api'
import { cn } from '@/lib/utils'

// Event types must match pkg/services/notifier EventType constants.
const EVENT_TYPES = [
  { id: 'provider_auth_fail', label: 'Provider auth failure', desc: 'A Usenet provider rejected your credentials.' },
  { id: 'provider_down', label: 'Provider unreachable', desc: 'A Usenet provider could not be connected to.' },
  { id: 'indexer_quota', label: 'Indexer quota exhausted', desc: 'An indexer hit its daily API or download limit.' },
  { id: 'playback_failure', label: 'Playback failure', desc: 'A release failed to open for streaming.' },
  { id: 'failover_exhausted', label: 'Failover exhausted', desc: 'All fallback releases failed; playback gave up.' },
]

// Channel types must match pkg/services/notifier buildChannel.
const CHANNEL_TYPES = [
  { id: 'webhook', label: 'Webhook (JSON POST)' },
  { id: 'ntfy', label: 'ntfy' },
  { id: 'discord', label: 'Discord' },
]

const URL_PLACEHOLDER = {
  webhook: 'https://example.com/hook',
  ntfy: 'https://ntfy.sh/your-topic',
  discord: 'https://discord.com/api/webhooks/...',
}

// ntfy.sh is a free public server, so there is no sensible static default URL to
// pre-fill: every install sharing one topic would leak each other's alerts on a
// public, unauthenticated channel. Instead we mint a unique random topic per
// channel the first time ntfy is selected, so each self-hoster gets their own
// "https://ntfy.sh/streamnzb-<random>" to subscribe to and monitor. Security for
// ntfy.sh topics comes from the name being unguessable.
function randomNtfyTopic() {
  let suffix = ''
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    const bytes = new Uint8Array(8)
    crypto.getRandomValues(bytes)
    suffix = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  } else {
    // Fallback for environments without WebCrypto; still per-call unique enough.
    suffix = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e9).toString(36)}`
  }
  return `https://ntfy.sh/streamnzb-${suffix}`
}

function normalizeEvents(events) {
  const out = {}
  for (const ev of EVENT_TYPES) {
    out[ev.id] = events ? events[ev.id] === true : false
  }
  return out
}

function normalizeChannel(channel = {}) {
  const wanted = Array.isArray(channel.events)
    ? channel.events.filter((e) => EVENT_TYPES.some((t) => t.id === e))
    : []
  return {
    name: channel.name ?? '',
    type: CHANNEL_TYPES.some((t) => t.id === channel.type) ? channel.type : 'webhook',
    url: channel.url ?? '',
    header: channel.header ?? '',
    priority: channel.priority ?? '',
    events: wanted,
    enabled: channel.enabled !== false,
  }
}

function buildInitialState(initialValues = {}) {
  return {
    enabled: initialValues?.enabled === true,
    minInterval: Number(initialValues?.min_interval_seconds ?? 300) || 0,
    events: normalizeEvents(initialValues?.events),
    channels: Array.isArray(initialValues?.channels)
      ? initialValues.channels.map(normalizeChannel)
      : [],
  }
}

export function NotificationsSection({ initialValues, isSaving, onPersist }) {
  const defaults = useMemo(() => buildInitialState(initialValues), [initialValues])
  const [enabled, setEnabled] = useState(defaults.enabled)
  const [minInterval, setMinInterval] = useState(defaults.minInterval)
  const [events, setEvents] = useState(defaults.events)
  const [channels, setChannels] = useState(defaults.channels)
  const [saving, setSaving] = useState(false)
  const [status, setStatus] = useState(null)
  const [testState, setTestState] = useState({}) // index -> {loading, ok, message}

  // Re-sync when the saved config reloads from the server.
  useEffect(() => {
    setEnabled(defaults.enabled)
    setMinInterval(defaults.minInterval)
    setEvents(defaults.events)
    setChannels(defaults.channels)
    setTestState({})
  }, [defaults])

  const updateChannel = (index, patch) => {
    setChannels((prev) => prev.map((c, i) => (i === index ? { ...c, ...patch } : c)))
  }

  const changeChannelType = (index, nextType) => {
    setChannels((prev) =>
      prev.map((c, i) => {
        if (i !== index) return c
        const patch = { type: nextType }
        // Pre-populate a unique ntfy topic when switching to ntfy with no URL yet,
        // so self-hosters get their own channel to subscribe to instead of a blank
        // field or a shared default.
        if (nextType === 'ntfy' && !c.url.trim()) {
          patch.url = randomNtfyTopic()
        }
        return { ...c, ...patch }
      })
    )
  }

  const toggleChannelEvent = (index, eventId) => {
    setChannels((prev) =>
      prev.map((c, i) => {
        if (i !== index) return c
        const has = c.events.includes(eventId)
        return { ...c, events: has ? c.events.filter((e) => e !== eventId) : [...c.events, eventId] }
      })
    )
  }

  const addChannel = () => {
    setChannels((prev) => [...prev, normalizeChannel({ type: 'webhook', enabled: true })])
  }

  const removeChannel = (index) => {
    setChannels((prev) => prev.filter((_, i) => i !== index))
    setTestState((prev) => {
      const next = {}
      Object.entries(prev).forEach(([k, v]) => {
        const ki = Number(k)
        if (ki < index) next[ki] = v
        else if (ki > index) next[ki - 1] = v
      })
      return next
    })
  }

  const buildPayload = () => ({
    enabled,
    events: { ...events },
    min_interval_seconds: Number(minInterval) || 0,
    channels: channels.map((c) => ({
      name: c.name.trim(),
      type: c.type,
      url: c.url.trim(),
      header: c.header.trim(),
      priority: c.priority.trim(),
      events: c.events,
      enabled: c.enabled !== false,
    })),
  })

  const handleSave = async () => {
    setSaving(true)
    setStatus(null)
    try {
      await onPersist(buildPayload())
      setStatus({ type: 'success', message: 'Notification settings saved.' })
    } catch (error) {
      setStatus({ type: 'error', message: error?.message || 'Failed to save notification settings.' })
    } finally {
      setSaving(false)
    }
  }

  const handleTest = async (index) => {
    const channel = channels[index]
    setTestState((prev) => ({ ...prev, [index]: { loading: true } }))
    try {
      const result = await apiFetch('/api/notifier/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: channel.name.trim(),
          type: channel.type,
          url: channel.url.trim(),
          header: channel.header.trim(),
          priority: channel.priority.trim(),
        }),
      })
      if (result && result.success === false) {
        setTestState((prev) => ({ ...prev, [index]: { loading: false, ok: false, message: result.error || 'Test failed.' } }))
      } else {
        setTestState((prev) => ({ ...prev, [index]: { loading: false, ok: true, message: 'Test alert sent.' } }))
      }
    } catch (error) {
      setTestState((prev) => ({ ...prev, [index]: { loading: false, ok: false, message: error?.message || 'Test failed.' } }))
    }
  }

  const busy = saving || isSaving

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader>
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0 space-y-0.5">
              <CardTitle>Alerting</CardTitle>
              <CardDescription>
                Push notifications when StreamNZB detects problems. Choose which events fire and where each goes.
              </CardDescription>
            </div>
            <div className="flex items-center gap-2 shrink-0">
              <Label htmlFor="notifier-enabled" className="text-sm text-muted-foreground">
                {enabled ? 'Enabled' : 'Disabled'}
              </Label>
              <Switch id="notifier-enabled" checked={enabled} onCheckedChange={setEnabled} />
            </div>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="max-w-xs space-y-1.5">
            <Label htmlFor="notifier-min-interval">Minimum interval between identical alerts (seconds)</Label>
            <Input
              id="notifier-min-interval"
              type="number"
              min={0}
              value={minInterval}
              onChange={(e) => setMinInterval(e.target.value)}
            />
            <p className="text-xs text-muted-foreground">
              Repeated identical alerts within this window are suppressed to prevent storms. 0 disables rate limiting.
            </p>
          </div>
        </CardContent>
      </Card>

      <Card className={cn(!enabled && 'opacity-60')}>
        <CardHeader>
          <CardTitle>Events</CardTitle>
          <CardDescription>Master toggle per event type. An event that is off never fires on any channel.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {EVENT_TYPES.map((ev) => (
            <div key={ev.id} className="flex items-start justify-between gap-3">
              <div className="min-w-0 space-y-0.5">
                <Label htmlFor={`event-${ev.id}`} className="block">{ev.label}</Label>
                <p className="text-xs text-muted-foreground">{ev.desc}</p>
              </div>
              <Switch
                id={`event-${ev.id}`}
                checked={events[ev.id] === true}
                onCheckedChange={(v) => setEvents((prev) => ({ ...prev, [ev.id]: v }))}
                disabled={!enabled}
              />
            </div>
          ))}
        </CardContent>
      </Card>

      <Card className={cn(!enabled && 'opacity-60')}>
        <CardHeader>
          <div className="flex items-start justify-between gap-3">
            <div className="space-y-0.5">
              <CardTitle>Channels</CardTitle>
              <CardDescription>Where alerts are delivered. Leave per-channel events empty to receive every enabled event.</CardDescription>
            </div>
            <Button type="button" variant="outline" size="sm" className="shrink-0" onClick={addChannel} disabled={!enabled}>
              <Plus className="h-4 w-4 mr-1" /> Add channel
            </Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {channels.length === 0 && (
            <p className="text-sm text-muted-foreground">No channels configured. Add a webhook, ntfy, or Discord destination.</p>
          )}
          {channels.map((channel, index) => {
            const test = testState[index] || {}
            return (
              <div key={index} className="rounded-lg border border-border p-4 space-y-3">
                <div className="flex items-center justify-between gap-3">
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={channel.enabled !== false}
                      onCheckedChange={(v) => updateChannel(index, { enabled: v })}
                      disabled={!enabled}
                    />
                    <span className="text-sm text-muted-foreground">{channel.enabled !== false ? 'Active' : 'Paused'}</span>
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8 text-destructive"
                    onClick={() => removeChannel(index)}
                    aria-label="Remove channel"
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>

                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <Label>Name</Label>
                    <Input
                      value={channel.name}
                      placeholder={`${channel.type}-${index + 1}`}
                      onChange={(e) => updateChannel(index, { name: e.target.value })}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label>Type</Label>
                    <select
                      className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                      value={channel.type}
                      onChange={(e) => changeChannelType(index, e.target.value)}
                    >
                      {CHANNEL_TYPES.map((t) => (
                        <option key={t.id} value={t.id}>{t.label}</option>
                      ))}
                    </select>
                  </div>
                </div>

                <div className="space-y-1.5">
                  <Label>URL</Label>
                  <Input
                    value={channel.url}
                    placeholder={URL_PLACEHOLDER[channel.type] || ''}
                    onChange={(e) => updateChannel(index, { url: e.target.value })}
                  />
                </div>

                {channel.type === 'webhook' && (
                  <div className="space-y-1.5">
                    <Label>Custom header (optional)</Label>
                    <Input
                      value={channel.header}
                      placeholder="Authorization: Bearer your-token"
                      onChange={(e) => updateChannel(index, { header: e.target.value })}
                    />
                    <p className="text-xs text-muted-foreground">One "Key: Value" header sent with each POST.</p>
                  </div>
                )}

                {channel.type === 'ntfy' && (
                  <div className="space-y-1.5 max-w-xs">
                    <Label>Priority (optional)</Label>
                    <Input
                      value={channel.priority}
                      placeholder="default, high, urgent..."
                      onChange={(e) => updateChannel(index, { priority: e.target.value })}
                    />
                  </div>
                )}

                <div className="space-y-1.5">
                  <Label className="text-xs uppercase tracking-wide text-muted-foreground">Only notify for</Label>
                  <div className="flex flex-wrap gap-1.5">
                    {EVENT_TYPES.map((ev) => {
                      const active = channel.events.includes(ev.id)
                      return (
                        <button
                          key={ev.id}
                          type="button"
                          onClick={() => toggleChannelEvent(index, ev.id)}
                          className={cn(
                            'rounded-full border px-2.5 py-1 text-xs transition-colors',
                            active
                              ? 'border-primary bg-primary/10 text-foreground'
                              : 'border-border text-muted-foreground hover:text-foreground'
                          )}
                        >
                          {ev.label}
                        </button>
                      )
                    })}
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {channel.events.length === 0
                      ? 'No filter selected — this channel receives every enabled event.'
                      : `This channel receives only the ${channel.events.length} selected event${channel.events.length > 1 ? 's' : ''}.`}
                  </p>
                </div>

                <div className="flex items-center gap-3 pt-1">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => handleTest(index)}
                    disabled={test.loading || !channel.url.trim()}
                  >
                    {test.loading ? <Loader2 className="h-4 w-4 mr-1 animate-spin" /> : <Send className="h-4 w-4 mr-1" />}
                    Send test
                  </Button>
                  {test.message && (
                    <span className={cn('flex items-center gap-1 text-xs', test.ok ? 'text-emerald-600' : 'text-destructive')}>
                      {test.ok ? <CheckCircle2 className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
                      {test.message}
                    </span>
                  )}
                </div>
              </div>
            )
          })}
        </CardContent>
      </Card>

      <div className="flex items-center gap-3">
        <Button type="button" onClick={handleSave} disabled={busy}>
          {busy ? <Loader2 className="h-4 w-4 mr-2 animate-spin" /> : <Save className="h-4 w-4 mr-2" />}
          Save notification settings
        </Button>
        {status && (
          <span className={cn('flex items-center gap-1 text-sm', status.type === 'success' ? 'text-emerald-600' : 'text-destructive')}>
            {status.type === 'success' ? <CheckCircle2 className="h-4 w-4" /> : <XCircle className="h-4 w-4" />}
            {status.message}
          </span>
        )}
      </div>
    </div>
  )
}
