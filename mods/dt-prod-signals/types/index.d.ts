/** One operation (span name × service) whose spans matched the file. */
export type SignalRow = {
  op: string
  service: string
  spans: number
  errors: number
  exceptions: number
  /** Span duration percentiles, nanoseconds. */
  p50: number
  p95: number
  /** A sample status message of a failed span, when one failed. */
  message: string | null
}

export type SignalStatus = 'loading' | 'ok' | 'empty' | 'error'

/** What Dynatrace said about one repo-relative file. */
export type FileSignal = {
  rel: string
  status: SignalStatus
  /** How spans were matched to the file, in words. */
  matchedBy: string
  timeframe: string
  dql: string
  rows: SignalRow[]
  scannedBytes: number | null
  error: string | null
  fetchedAt: number
}

declare module 'claude-code' {
  interface PluginState {
    'dt-prod-signals': {
      files: Record<string, FileSignal>
      /** Repo-relative paths, most recently touched first. */
      recent: string[]
      isHidden: boolean
      /** The file whose DQL the pane reveals, if any. */
      openDql: string | null
      /** host/org/repo of the git remote, when found. */
      repo: string | null
    }
  }
}
