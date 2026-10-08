import { Waypoints } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { FilterInput, PageHeader, Panel } from '../components/Panel'
import { SpanTable, spanFailed } from '../components/signals'
import { ErrorBox, Segmented } from '../components/ui'
import { useDql } from '../lib/api'
import { q, SPAN_LENSES, spansQuery } from '../lib/dql'
import { tfSpec } from '../lib/shared'
import { useTitle } from '../lib/store'
import { useTimeframe } from '../lib/timeframe'

export default function Traces() {
  useTitle('Traces')
  const tf = useTimeframe()
  const params = new URLSearchParams(useSearch())
  const [, navigate] = useLocation()
  const lens = params.get('lens') ?? 'roots'
  const [text, setText] = useState(params.get('q') ?? '')
  const [debounced, setDebounced] = useState(text)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(text.trim()), 350)
    return () => clearTimeout(t)
  }, [text])
  const extra = debounced
    ? [`contains(span.name, ${q(debounced)}, caseSensitive:false) or contains(coalesce(dt.service.name, service.name, ""), ${q(debounced)}, caseSensitive:false) or contains(coalesce(endpoint.name, ""), ${q(debounced)}, caseSensitive:false)`]
    : []
  const spec = tfSpec(tf, spansQuery(lens, extra, 500))
  const res = useDql(spec)
  const rows = res.data?.records
  const failed = rows?.filter(spanFailed).length ?? 0

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Traces"
        icon={<Waypoints className="size-5" />}
        sub={rows ? `${rows.length}${rows.length >= 500 ? '+' : ''} spans · ${failed} failed · ${tf.label.toLowerCase()}` : 'Distributed traces'}
        actions={
          <>
            <Segmented
              value={lens}
              onChange={(l) => navigate(`/traces?lens=${l}`, { replace: true })}
              options={SPAN_LENSES.map((l) => ({ value: l.key, label: l.label }))}
            />
            <FilterInput value={text} onChange={setText} placeholder="Span, service or endpoint…" className="w-64" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" title={SPAN_LENSES.find((l) => l.key === lens)?.label}>
        {res.error ? <ErrorBox error={res.error} /> : <SpanTable records={rows} loading={res.isLoading} className="flex-1" />}
      </Panel>
    </div>
  )
}
