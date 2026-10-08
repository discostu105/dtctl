import { Gauge } from 'lucide-react'
import { fmtBytes, fmtCompact } from '../lib/format'
import type { Sampling } from '../lib/sampling'
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
