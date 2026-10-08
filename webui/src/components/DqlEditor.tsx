// DQL editor: CodeMirror 6 + Grail's own language services.
//  - completions from /query:autocomplete (commands, functions, data objects,
//    and fields that actually exist in the data), with docs and synopsis
//  - value completions after `field ==`: runs the query's own prefix to list
//    the field's top values with counts
//  - live diagnostics from /query:verify, underlined at the exact range
// Lazy-loaded by the Query page so the rest of the app stays light.
import { autocompletion, closeBrackets, closeBracketsKeymap, completionKeymap, startCompletion, type Completion, type CompletionContext, type CompletionResult } from '@codemirror/autocomplete'
import { defaultKeymap, history, historyKeymap, indentWithTab, toggleComment } from '@codemirror/commands'
import { bracketMatching, HighlightStyle, StreamLanguage, syntaxHighlighting } from '@codemirror/language'
import { linter, lintKeymap, type Diagnostic } from '@codemirror/lint'
import { highlightSelectionMatches, searchKeymap } from '@codemirror/search'
import { EditorState, Prec } from '@codemirror/state'
import { drawSelection, EditorView, highlightActiveLine, highlightActiveLineGutter, keymap, lineNumbers, placeholder as cmPlaceholder } from '@codemirror/view'
import { tags as t } from '@lezer/highlight'
import { useEffect, useRef } from 'react'
import { runDql } from '../lib/api'

const COMMANDS = new Set([
  'fetch', 'filter', 'filterOut', 'fields', 'fieldsAdd', 'fieldsRemove', 'fieldsRename', 'fieldsKeep', 'fieldsSummary', 'fieldsFlatten',
  'summarize', 'sort', 'limit', 'lookup', 'join', 'joinNested', 'append', 'parse', 'expand', 'dedup', 'timeseries', 'makeTimeseries',
  'smartscapeNodes', 'smartscapeEdges', 'data', 'describe', 'metrics', 'search', 'traverse', 'load',
])
const KEYWORDS = new Set(['and', 'or', 'not', 'xor', 'true', 'false', 'null', 'asc', 'desc'])

// Small stream tokenizer: commands only count at the start of a stage.
const dql = StreamLanguage.define<{ stageStart: boolean }>({
  name: 'dql',
  startState: () => ({ stageStart: true }),
  token(stream, state) {
    if (stream.eatSpace()) return null
    if (stream.match('//')) {
      stream.skipToEnd()
      return 'comment'
    }
    if (stream.match('|')) {
      state.stageStart = true
      return 'separator'
    }
    if (stream.match(/^"(?:[^"\\]|\\.)*"?/)) return 'string'
    if (stream.match(/^`[^`]*`?/)) return 'variableName'
    if (stream.match(/^\d+(?:\.\d+)?(?:ms|s|m|h|d|w|y|ns|us)?\b/)) return 'number'
    if (stream.match(/^[A-Za-z_$][\w.$]*/)) {
      const word = stream.current()
      const wasStart = state.stageStart
      state.stageStart = false
      if (wasStart && COMMANDS.has(word)) return 'keyword'
      if (stream.peek() === '(') return 'function'
      if (stream.peek() === ':') return 'attributeName'
      if (KEYWORDS.has(word)) return 'operatorKeyword'
      return 'propertyName'
    }
    if (stream.match(/^(==|!=|<=|>=|[=<>+\-*/%~])/)) return 'operator'
    stream.next()
    return null
  },
  languageData: { commentTokens: { line: '//' } },
})

const highlight = HighlightStyle.define([
  { tag: t.keyword, color: 'var(--accent-ink)', fontWeight: '600' },
  { tag: t.function(t.variableName), color: 'var(--s1)' },
  { tag: t.string, color: 'var(--ok)' },
  { tag: t.number, color: 'var(--warn)' },
  { tag: t.comment, color: 'var(--ink-3)', fontStyle: 'italic' },
  { tag: t.separator, color: 'var(--ink-3)' },
  { tag: t.operatorKeyword, color: 'var(--accent-ink)' },
  { tag: t.operator, color: 'var(--ink-2)' },
  { tag: t.attributeName, color: 'var(--s7)' },
  { tag: t.propertyName, color: 'var(--ink)' },
  { tag: t.variableName, color: 'var(--s5)' },
])

const theme = EditorView.theme({
  '&': { backgroundColor: 'transparent', color: 'var(--ink)', fontSize: '13px' },
  '&.cm-focused': { outline: 'none' },
  '.cm-scroller': { fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace", lineHeight: '21px' },
  '.cm-content': { padding: '12px 0', caretColor: 'var(--ink)' },
  '.cm-gutters': { backgroundColor: 'transparent', border: 'none', color: 'var(--ink-4)' },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--ink-2)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent) 5%, transparent)' },
  '.cm-cursor': { borderLeftColor: 'var(--ink)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'color-mix(in srgb, var(--accent) 28%, transparent) !important' },
  '.cm-matchingBracket': { backgroundColor: 'var(--accent-wash)', outline: '1px solid color-mix(in srgb, var(--accent) 40%, transparent)' },
  '.cm-selectionMatch': { backgroundColor: 'var(--line-strong)' },
  '.cm-placeholder': { color: 'var(--ink-4)' },
  '.cm-tooltip': { backgroundColor: 'var(--bg-raised)', border: 'none', borderRadius: '8px', boxShadow: 'var(--shadow)', overflow: 'hidden' },
  '.cm-tooltip-autocomplete > ul': { fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace", fontSize: '12px', maxHeight: '320px' },
  '.cm-tooltip-autocomplete > ul > li': { padding: '3px 10px', lineHeight: '20px' },
  '.cm-tooltip-autocomplete > ul > li[aria-selected]': { backgroundColor: 'var(--accent-wash)', color: 'var(--ink)' },
  '.cm-completionDetail': { marginLeft: '10px', color: 'var(--ink-3)', fontStyle: 'normal', fontFamily: "'Inter Variable', ui-sans-serif, sans-serif", fontSize: '11px' },
  '.cm-completionMatchedText': { textDecoration: 'none', color: 'var(--accent-ink)', fontWeight: '600' },
  '.cm-completionInfo': { padding: '8px 10px', maxWidth: '380px', fontFamily: "'Inter Variable', ui-sans-serif, sans-serif", fontSize: '12px', lineHeight: '1.45', color: 'var(--ink-2)' },
  '.cm-completionIcon': { opacity: '0.7', width: '1.4em' },
  '.cm-diagnostic': { fontFamily: "'Inter Variable', ui-sans-serif, sans-serif", fontSize: '12px', padding: '6px 10px' },
  '.cm-diagnostic-error': { borderLeft: '3px solid var(--crit)' },
  '.cm-diagnostic-warning': { borderLeft: '3px solid var(--warn)' },
  '.cm-lintRange-error': { backgroundImage: 'none', textDecoration: 'underline wavy var(--crit)', textUnderlineOffset: '3px' },
  '.cm-lintRange-warning': { backgroundImage: 'none', textDecoration: 'underline wavy var(--warn)', textUnderlineOffset: '3px' },
})

const HEADERS = { 'X-Dtctl-Web': '1', 'Content-Type': 'application/json' }

async function post(op: 'autocomplete' | 'verify', body: unknown, signal?: AbortSignal) {
  const res = await fetch(`/api/dql/${op}`, { method: 'POST', headers: HEADERS, body: JSON.stringify(body), signal })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}

const KIND: Record<string, { type: string; detail: string }> = {
  COMMAND_NAME: { type: 'keyword', detail: 'command' },
  FUNCTION_NAME: { type: 'function', detail: 'function' },
  DATA_OBJECT: { type: 'class', detail: 'data object' },
  SIMPLE_IDENTIFIER: { type: 'property', detail: 'field' },
  PARAMETER_KEY: { type: 'variable', detail: 'parameter' },
  ENUM_VALUE: { type: 'enum', detail: 'value' },
  BOOLEAN: { type: 'constant', detail: 'boolean' },
  TIME_UNIT: { type: 'constant', detail: 'unit' },
}

function infoNode(info?: string, synopsis?: string) {
  if (!info && !synopsis) return undefined
  return () => {
    const el = document.createElement('div')
    if (synopsis) {
      const s = document.createElement('div')
      s.textContent = synopsis
      s.style.cssText = "font-family:'JetBrains Mono Variable',ui-monospace,monospace;color:var(--ink);margin-bottom:4px"
      el.appendChild(s)
    }
    if (info) {
      const d = document.createElement('div')
      d.textContent = info
      el.appendChild(d)
    }
    return el
  }
}

/** A function suggestion ("count(") is inserted as "count()" with the cursor inside. */
function applySuggestion(text: string): Completion['apply'] {
  if (!text.endsWith('(')) return text
  return (view, _c, from, to) => {
    const insert = text + ')'
    view.dispatch({ changes: { from, to, insert }, selection: { anchor: from + text.length }, userEvent: 'input.complete' })
  }
}

export interface Timeframe {
  from: string
  to: string
}

export default function DqlEditor({
  value,
  onChange,
  onRun,
  onHistory,
  onVerify,
  timeframe,
  autoFocus,
}: {
  value: string
  onChange: (v: string) => void
  onRun: (doc: string) => void
  onHistory?: (dir: 1 | -1) => string | null
  onVerify?: (v: { valid: boolean; messages: string[] } | null) => void
  timeframe: Timeframe
  autoFocus?: boolean
}) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  // Callbacks change every render; read them through a ref.
  const cb = useRef({ onChange, onRun, onHistory, onVerify, timeframe })
  cb.current = { onChange, onRun, onHistory, onVerify, timeframe }

  useEffect(() => {
    const complete = async (ctx: CompletionContext): Promise<CompletionResult | null> => {
      const doc = ctx.state.doc.toString()
      const pos = ctx.pos
      const before = doc.slice(0, pos)
      const word = ctx.matchBefore(/[\w.$`]*/)
      if (!ctx.explicit && word && word.from === word.to && !/[\s|(,{:=]$/.test(before)) return null

      const results: Completion[] = []
      let from = word ? word.from : pos

      // 1. Values: `field ==` / `field != "par…` → top values from the data.
      const m = /([A-Za-z_$`][\w.$`]*)\s*(==|!=)\s*("?)([^"|\n]*)$/.exec(before)
      if (m) {
        const field = m[1]
        const stageStart = before.lastIndexOf('|', m.index)
        const prefix = stageStart > 0 ? doc.slice(0, stageStart) : ''
        if (/^\s*(fetch|timeseries|smartscapeNodes|data)\b/.test(prefix)) {
          try {
            const r = await runDql({
              query: `${prefix.trimEnd()}\n| summarize c = count(), by:{v = ${field}}\n| sort c desc\n| limit 30`,
              from: cb.current.timeframe.from,
              to: cb.current.timeframe.to,
              ttl: 60,
            })
            if (ctx.aborted) return null
            const typed = m[3] + m[4]
            from = pos - typed.length
            for (const row of r.records) {
              if (row.v == null) continue
              const v = row.v
              const lit = typeof v === 'string' ? JSON.stringify(v) : String(v)
              results.push({ label: lit, type: 'text', detail: `${Number(row.c).toLocaleString()} records`, boost: 99 })
            }
          } catch {
            /* fall through to the language service */
          }
        }
      }

      // 2. Grail's language service.
      if (!results.length) {
        try {
          const r = await post('autocomplete', { query: doc, cursorPosition: pos })
          if (ctx.aborted) return null
          const sugg: any[] = r.suggestions ?? []
          if (sugg.length) from = pos - (sugg[0].alreadyTypedCharacters ?? 0)
          for (const s of sugg) {
            const head = (s.parts ?? [])[0] ?? {}
            const k = KIND[head.type] ?? { type: 'text', detail: String(head.type ?? '').toLowerCase().replace(/_/g, ' ') }
            results.push({
              label: s.suggestion.trimEnd(),
              apply: applySuggestion(s.suggestion.trimEnd()),
              type: k.type,
              detail: k.detail,
              info: infoNode(head.info, head.synopsis),
            })
          }
        } catch {
          return null
        }
      }
      if (!results.length) return null
      return { from, options: results, validFor: /^[\w.$`"]*$/ }
    }

    const lint = linter(
      async (v) => {
        const doc = v.state.doc.toString()
        if (!doc.trim()) {
          cb.current.onVerify?.(null)
          return []
        }
        try {
          const r = await post('verify', { query: doc })
          const diags: Diagnostic[] = []
          const messages: string[] = []
          for (const n of r.notifications ?? []) {
            messages.push(n.message)
            const sp = n.syntaxPosition
            if (sp?.start?.index != null) {
              const from = Math.min(sp.start.index, doc.length)
              const to = Math.min(Math.max((sp.end?.index ?? sp.start.index) + 1, from + 1), doc.length)
              diags.push({ from, to, severity: n.severity === 'ERROR' ? 'error' : n.severity === 'WARNING' ? 'warning' : 'info', message: n.message })
            }
          }
          cb.current.onVerify?.({ valid: !!r.valid, messages })
          return diags
        } catch {
          return []
        }
      },
      { delay: 450 },
    )

    const keys = Prec.highest(
      keymap.of([
        { key: 'Mod-Enter', run: (v) => (cb.current.onRun(v.state.doc.toString()), true) },
        { key: 'Mod-/', run: toggleComment },
        { key: 'Mod-Space', run: startCompletion },
        {
          key: 'Mod-ArrowUp',
          run: (v) => {
            const h = cb.current.onHistory?.(1)
            if (h != null) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: h } })
            return true
          },
        },
        {
          key: 'Mod-ArrowDown',
          run: (v) => {
            const h = cb.current.onHistory?.(-1)
            if (h != null) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: h } })
            return true
          },
        },
      ]),
    )

    view.current = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          keys,
          lineNumbers(),
          highlightActiveLineGutter(),
          highlightActiveLine(),
          history(),
          drawSelection(),
          bracketMatching(),
          closeBrackets(),
          highlightSelectionMatches(),
          EditorView.lineWrapping,
          dql,
          syntaxHighlighting(highlight),
          theme,
          cmPlaceholder('fetch logs | filter loglevel == "ERROR"'),
          autocompletion({ override: [complete], activateOnTyping: true, icons: true, closeOnBlur: true }),
          lint,
          keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...searchKeymap, ...completionKeymap, ...lintKeymap, indentWithTab]),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) cb.current.onChange(u.state.doc.toString())
          }),
        ],
      }),
    })
    if (autoFocus) view.current.focus()
    return () => view.current?.destroy()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Controlled value: apply external changes (history pick, example click).
  useEffect(() => {
    const v = view.current
    if (v && v.state.doc.toString() !== value) v.dispatch({ changes: { from: 0, to: v.state.doc.length, insert: value } })
  }, [value])

  return <div ref={host} className="min-h-[120px]" />
}
