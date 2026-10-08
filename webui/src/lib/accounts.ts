import { useMemo } from 'react'
import { useDql } from './api'
import { AZURE_SUBSCRIPTIONS_QUERY } from './dql'

// Cloud resources name their account by id: an Azure subscription GUID means
// nothing to a reader. One cached lookup per session maps it to the name the
// subscription node carries. AWS account ids and GCP project ids stay as they
// are: Smartscape has no friendlier name for them.

const EMPTY = new Map<string, string>()

/** subscription id → name; empty until loaded, and for anything not on Azure. */
export function useAzureSubscriptions(enabled: boolean): Map<string, string> {
  const res = useDql(enabled ? { query: AZURE_SUBSCRIPTIONS_QUERY, from: 'now-30d', ttl: 3600 } : null)
  return useMemo(() => {
    const m = new Map<string, string>()
    for (const r of res.data?.records ?? []) if (r['azure.subscription'] && r.name) m.set(String(r['azure.subscription']), String(r.name))
    return m.size ? m : EMPTY
  }, [res.data])
}
