import type { ReactNode } from 'react'

const COMMANDS = new Set([
  'fetch', 'filter', 'filterOut', 'fields', 'fieldsAdd', 'fieldsRemove', 'fieldsRename', 'fieldsKeep', 'fieldsSummary',
  'summarize', 'sort', 'limit', 'lookup', 'join', 'joinNested', 'append', 'parse', 'expand', 'dedup', 'timeseries',
  'makeTimeseries', 'smartscapeNodes', 'smartscapeEdges', 'data', 'describe', 'metrics', 'search', 'traverse', 'fieldsFlatten', 'load',
])
const KEYWORDS = new Set(['and', 'or', 'not', 'xor', 'true', 'false', 'null', 'by', 'from', 'to', 'interval', 'asc', 'desc'])

const TOKEN = /(\/\/[^\n]*)|("(?:[^"\\]|\\.)*")|(`[^`]*`)|(\b\d+(?:\.\d+)?(?:ms|s|m|h|d|w)?\b)|(\|)|([A-Za-z_][\w.]*)(\s*\()?|(\s+)|(.)/g

/** Tiny DQL tokenizer → React nodes (no innerHTML). */
export function highlightDql(src: string): ReactNode[] {
  const out: ReactNode[] = []
  let m: RegExpExecArray | null
  let i = 0
  TOKEN.lastIndex = 0
  while ((m = TOKEN.exec(src))) {
    const [all, cmt, str, bq, numb, pipe, ident, paren] = m
    const k = i++
    if (cmt) out.push(<span key={k} className="dql-cmt">{cmt}</span>)
    else if (str) out.push(<span key={k} className="dql-str">{str}</span>)
    else if (bq) out.push(<span key={k}>{bq}</span>)
    else if (numb) out.push(<span key={k} className="dql-num">{numb}</span>)
    else if (pipe) out.push(<span key={k} className="dql-pipe">{pipe}</span>)
    else if (ident) {
      if (COMMANDS.has(ident)) out.push(<span key={k} className="dql-kw">{ident}</span>)
      else if (paren) out.push(<span key={k} className="dql-fn">{ident}</span>)
      else if (KEYWORDS.has(ident)) out.push(<span key={k} className="dql-kw" style={{ fontWeight: 400 }}>{ident}</span>)
      else out.push(ident)
      if (paren) out.push(paren)
    } else out.push(all)
  }
  return out
}
