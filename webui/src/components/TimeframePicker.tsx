import * as Popover from '@radix-ui/react-popover'
import clsx from 'clsx'
import { CalendarClock, Check, ChevronDown } from 'lucide-react'
import { useState } from 'react'
import { tfPickerStore, useStore } from '../lib/store'
import { parseRel, PRESETS, setTimeframe, useTimeframe } from '../lib/timeframe'

export function TimeframePicker() {
  const tf = useTimeframe()
  const open = useStore(tfPickerStore)
  const [custom, setCustom] = useState('')
  const parsed = custom ? parseRel(custom.replace(/\s+/g, '')) : null

  return (
    <Popover.Root open={open} onOpenChange={tfPickerStore.set}>
      <Popover.Trigger asChild>
        <button
          type="button"
          className="inline-flex h-8 items-center gap-2 rounded-lg border border-line bg-sunken px-2.5 text-sm text-ink-2 transition-colors hover:border-line-strong hover:text-ink"
        >
          <CalendarClock className="size-3.5 text-ink-3" />
          <span className="max-w-56 truncate">{tf.label}</span>
          <ChevronDown className="size-3.5 text-ink-3" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="end" sideOffset={6} className="anim-pop z-50 w-64 rounded-lg bg-raised p-1.5 shadow-pop">
          <input
            autoFocus
            value={custom}
            onChange={(e) => setCustom(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && parsed) {
                setTimeframe(parsed)
                setCustom('')
                tfPickerStore.set(false)
              }
            }}
            placeholder="Type e.g. 45m, 12h, 3d…"
            className="mb-1 h-8 w-full rounded-md border border-line bg-sunken px-2 text-sm outline-none placeholder:text-ink-4 focus:border-accent/60"
          />
          {custom && (
            <div className="px-2 py-1 text-xs text-ink-3">
              {parsed ? (
                <>
                  ↵ <span className="text-ink">{parsed.label}</span>
                </>
              ) : (
                'Use a number + s/m/h/d/w'
              )}
            </div>
          )}
          {PRESETS.map((p) => (
            <button
              key={p.key}
              type="button"
              onClick={() => {
                setTimeframe(parseRel(p.key)!)
                tfPickerStore.set(false)
              }}
              className={clsx(
                'flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-sm hover:bg-panel-hover',
                tf.key === p.key ? 'text-ink' : 'text-ink-2',
              )}
            >
              <span className="w-4">{tf.key === p.key && <Check className="size-3.5 text-accent" />}</span>
              <span className="flex-1">{p.label}</span>
              <span className="text-2xs text-ink-4">{p.key}</span>
            </button>
          ))}
          <div className="mt-1 border-t border-line px-2 pt-1.5 pb-0.5 text-2xs text-ink-3">Tip: drag across any chart to zoom in.</div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
