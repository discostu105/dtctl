import { Gauge } from 'lucide-react'
import { fmtBytes, fmtCompact } from '../lib/format'
import { setFullScan, type Sampling, type ScanWindow } from '../lib/sampling'
import { Tip } from './ui'

/** "≈ 1:10 sample" pill: says that counts are extrapolated, and why. */
export function SampledBadge({ ratio, sampling }: { ratio: number; sampling?: Sampling }) {
  if (ratio <= 1) return null
  return (
    <Tip
      content={
        <div className="max-w-xs space-y-1">
          <div className="font-medium text-ink">Sampled 1 in {fmtCompact(ratio)} records</div>
          <div>
            {sampling?.estBytes
              ? `This timeframe holds about ${fmtBytes(sampling.estBytes)} (${fmtCompact(sampling.estRecords)} records). `
              : ''}
            Reading all of it would be slow and could hit Grail's scan limit, so the chart reads a sample and scales counts up. Shapes and proportions
            hold; small counts are approximate. Narrow the timeframe or filters for exact numbers.
          </div>
        </div>
      }
    >
      <span className="inline-flex h-5 cursor-help items-center gap-1 rounded bg-warn-wash px-1.5 text-2xs font-medium text-warn">
        <Gauge className="size-3" />≈ 1:{fmtCompact(ratio)} sample
      </span>
    </Tip>
  )
}

/** Banner for views that read a narrowed window on large tenants. */
export function ScanNotice({ sw, what }: { sw: ScanWindow; what: string }) {
  if (!sw.narrowed && !sw.forcedFull) return null
  if (sw.forcedFull)
    return (
      <div className="mb-4 flex items-center gap-2 rounded-lg border border-warn/30 bg-warn-wash px-3 py-2 text-xs text-ink-2">
        <Gauge className="size-4 shrink-0 text-warn" />
        <span>
          Scanning the full {sw.full.label.toLowerCase()} {sw.estBytes ? `(≈ ${fmtBytes(sw.estBytes)} of ${sw.table})` : ''}. This can be slow and may stop at Grail's scan limit.
        </span>
        <button type="button" onClick={() => setFullScan(sw.table, false)} className="ml-auto shrink-0 rounded-md px-2 py-1 font-medium text-accent-ink hover:bg-accent-wash">
          Back to the fast window
        </button>
      </div>
    )
  return (
    <div className="mb-4 flex items-center gap-2 rounded-lg border border-warn/30 bg-warn-wash px-3 py-2 text-xs text-ink-2">
      <Gauge className="size-4 shrink-0 text-warn" />
      <span>
        This tenant has ≈ {fmtBytes(sw.estBytes)} of {sw.table} in the {sw.full.label.toLowerCase()}. To stay fast and under the scan limit, {what} read the{' '}
        <b className="font-medium text-ink">{sw.tf?.label.toLowerCase()}</b>.
      </span>
      <button type="button" onClick={() => setFullScan(sw.table, true)} className="ml-auto shrink-0 rounded-md px-2 py-1 font-medium text-accent-ink hover:bg-accent-wash">
        Scan the full {sw.full.label.toLowerCase().replace(/^last /, '')}
      </button>
    </div>
  )
}
