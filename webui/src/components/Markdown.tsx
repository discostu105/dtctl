import type { ReactNode } from 'react'

/**
 * Minimal, safe markdown for Davis descriptions and vulnerability text:
 * headings, paragraphs, bullet lists, **bold**, `code`. Builds React nodes —
 * never injects HTML.
 */
export function Markdown({ text, className }: { text: string; className?: string }) {
  const blocks: ReactNode[] = []
  const lines = text.replace(/\r/g, '').split('\n')
  let list: string[] = []
  let para: string[] = []
  const flushPara = () => {
    if (para.length) blocks.push(<p key={blocks.length}>{inline(para.join(' '))}</p>)
    para = []
  }
  const flushList = () => {
    if (list.length)
      blocks.push(
        <ul key={blocks.length}>
          {list.map((l, i) => (
            <li key={i}>{inline(l)}</li>
          ))}
        </ul>,
      )
    list = []
  }
  for (const raw of lines) {
    const line = raw.trimEnd()
    const h = /^(#{1,6})\s+(.*)$/.exec(line)
    const li = /^\s*[-*]\s+(.*)$/.exec(line)
    if (h) {
      flushPara()
      flushList()
      blocks.push(<h3 key={blocks.length}>{inline(h[2])}</h3>)
    } else if (li) {
      flushPara()
      list.push(li[1])
    } else if (!line.trim()) {
      flushPara()
      flushList()
    } else {
      flushList()
      para.push(line.trim())
    }
  }
  flushPara()
  flushList()
  return <div className={`md text-sm text-ink-2 ${className ?? ''}`}>{blocks}</div>
}

function inline(s: string): ReactNode[] {
  const out: ReactNode[] = []
  const re = /(\*\*[^*]+\*\*)|(`[^`]+`)/g
  let last = 0
  let m: RegExpExecArray | null
  let k = 0
  while ((m = re.exec(s))) {
    if (m.index > last) out.push(s.slice(last, m.index))
    if (m[1]) out.push(<strong key={k++}>{m[1].slice(2, -2)}</strong>)
    else out.push(<code key={k++}>{m[2].slice(1, -1)}</code>)
    last = m.index + m[0].length
  }
  if (last < s.length) out.push(s.slice(last))
  return out
}
