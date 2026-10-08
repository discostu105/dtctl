import { Waypoints } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useLocation, useSearch } from 'wouter'
import { FacetSearch, FacetSummary, useFacets } from '../components/Facets'
import { PageHeader, Panel } from '../components/Panel'
import { SPAN_FACETS, SpanTable } from '../components/signals'
import { ErrorBox, Segmented } from '../components/ui'
import { useDql, type Rec } from '../lib/api'
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
  // The text is searched server-side (span, service, endpoint over the whole
  // timeframe); facets then narrow the loaded spans client-side.
  const [rowsForFacets, setRowsForFacets] = useState<Rec[] | undefined>()
  const fc = useFacets(rowsForFacets, SPAN_FACETS, { text: (r) => `${r['span.name']} ${r['endpoint.name'] ?? ''} ${r.service}` })
  const [debounced, setDebounced] = useState(fc.text.trim())
  useEffect(() => {
    const t = setTimeout(() => setDebounced(fc.text.split(/\s+/).filter((w) => w && !w.includes(':')).join(' ')), 350)
    return () => clearTimeout(t)
  }, [fc.text])
  const extra = debounced
    ? [`contains(span.name, ${q(debounced)}, caseSensitive:false) or contains(coalesce(dt.service.name, service.name, ""), ${q(debounced)}, caseSensitive:false) or contains(coalesce(endpoint.name, ""), ${q(debounced)}, caseSensitive:false)`]
    : []
  const spec = tfSpec(tf, spansQuery(lens, extra, 500))
  const res = useDql(spec)
  useEffect(() => setRowsForFacets(res.data?.records), [res.data])

  return (
    <div className="flex h-full flex-col p-5">
      <PageHeader
        title="Traces"
        icon={<Waypoints className="size-5" />}
        sub={`Distributed traces · ${tf.label.toLowerCase()}`}
        actions={
          <>
            <Segmented
              value={lens}
              onChange={(l) => {
                const p = new URLSearchParams(window.location.search)
                p.set('lens', l)
                navigate(`/traces?${p}`, { replace: true })
              }}
              options={SPAN_LENSES.map((l) => ({ value: l.key, label: l.label }))}
            />
            <FacetSearch fc={fc} placeholder="Filter spans by name, service, endpoint…" className="w-72" />
          </>
        }
      />
      <Panel spec={spec} result={res} className="min-h-0 flex-1" bodyClassName="flex min-h-0 flex-col" head={<FacetSummary fc={fc} noun={(res.data?.records.length ?? 0) >= 500 ? 'newest spans' : 'spans'} />}>
        {res.error ? <ErrorBox error={res.error} /> : <SpanTable records={fc.rows} loading={res.isLoading} facets={fc} className="flex-1" autoFocus />}
      </Panel>
    </div>
  )
}
