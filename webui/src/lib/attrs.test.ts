import { describe, expect, it } from 'vitest'
import {
  ATTRIBUTES,
  NS_LABELS,
  PRIMARY_TAGS,
  TOP_VALUES,
  UNSET,
  attrCondition,
  attrStages,
  describeField,
  discoveryQuery,
  fieldExpr,
  groupAttrs,
  groupRank,
  mapField,
  nodesSource,
  parseAttr,
  parseAttrs,
  parseDiscovery,
  parseField,
  parseValues,
  sameAttr,
  serializeAttr,
  valueCond,
  valuesQuery,
  withAttrs,
  type AttrFilter,
  type AttrSource,
} from './attrs'

const LABEL = 'tags:k8s.labels[app.kubernetes.io/name]'
const LABEL_EXPR = '`tags:k8s.labels`[`app.kubernetes.io/name`]'

describe('parseField / mapField', () => {
  it('splits a tag-map reference into map and key', () => {
    expect(parseField(LABEL)).toEqual({ field: LABEL, map: 'tags:k8s.labels', key: 'app.kubernetes.io/name' })
    expect(mapField('tags:aws', 'Name')).toBe('tags:aws[Name]')
  })

  it('treats everything else as a scalar field', () => {
    expect(parseField('k8s.namespace.name')).toEqual({ field: 'k8s.namespace.name' })
    expect(parseField('foo[bar]')).toEqual({ field: 'foo[bar]' }) // only tags:* are maps
  })
})

describe('fieldExpr', () => {
  it('leaves bare dotted fields alone', () => {
    expect(fieldExpr('k8s.namespace.name')).toBe('k8s.namespace.name')
    expect(fieldExpr('primary_tags.team')).toBe('primary_tags.team')
  })

  it('backtick-quotes both segments of a tag-map reference', () => {
    expect(fieldExpr(LABEL)).toBe(LABEL_EXPR)
  })

  it('backtick-quotes the whole path when a key has special characters', () => {
    expect(fieldExpr('primary_tags.cost-center')).toBe('`primary_tags.cost-center`')
    expect(fieldExpr('k8s.namespace.label.kubernetes.io/metadata.name')).toBe('`k8s.namespace.label.kubernetes.io/metadata.name`')
  })
})

describe('valueCond', () => {
  const e = 'f'
  it('maps "not set" to isNull', () => expect(valueCond(e, UNSET)).toBe('isNull(f)'))
  it('maps patterns to matchesValue', () => expect(valueCond(e, 'web-*')).toBe('matchesValue(f, "web-*")'))
  it('compares booleans typed and as text', () => expect(valueCond(e, 'true')).toBe('(f == true or f == "true")'))
  it('compares integers typed and as text', () => {
    expect(valueCond(e, '42')).toBe('(f == 42 or f == "42")')
    expect(valueCond(e, '-3')).toBe('(f == -3 or f == "-3")')
  })
  it('compares decimals typed and as text', () => expect(valueCond(e, '1.5')).toBe('(f == 1.5 or f == "1.5")'))
  it('adds toUid and ~ legs for hex uids', () => {
    expect(valueCond(e, 'a1b2c3d4e5f60718')).toBe('(f == "a1b2c3d4e5f60718" or f == toUid("a1b2c3d4e5f60718") or f ~ "a1b2c3d4e5f60718")')
  })
  it('adds a ~ leg for Smartscape ids, UUIDs and IPs', () => {
    expect(valueCond(e, 'HOST-0123456789ABCDEF')).toBe('(f == "HOST-0123456789ABCDEF" or f ~ "HOST-0123456789ABCDEF")')
    const uuid = '123e4567-e89b-12d3-a456-426614174000'
    expect(valueCond(e, uuid)).toBe(`(f == "${uuid}" or f ~ "${uuid}")`)
    expect(valueCond(e, '10.0.0.1')).toBe('(f == "10.0.0.1" or f ~ "10.0.0.1")')
  })
  it('compares plain strings as quoted literals', () => {
    expect(valueCond(e, 'prod')).toBe('f == "prod"')
    expect(valueCond(e, '1.2.3')).toBe('f == "1.2.3"') // a version, not a number
  })
})

describe('injection safety', () => {
  it('rejects field identities carrying backticks, line breaks or pipes', () => {
    expect(parseAttr('f`x=1')).toBeNull()
    expect(parseAttr('f|x=1')).toBeNull()
    expect(parseAttr('f\nx=1')).toBeNull()
    expect(parseAttr('f\rx=1')).toBeNull()
    expect(parseAttr('tags:k8s.labels[a`b]=1')).toBeNull()
    expect(parseAttr('-f|x=1')).toBeNull()
  })

  it('keeps hostile values inside one escaped string literal', () => {
    expect(valueCond('f', 'x" or true or "')).toBe('f == "x\\" or true or \\""')
    expect(valueCond('f', 'a`|b\n| fetch logs')).toBe('f == "a`|b\\n| fetch logs"')
    expect(valueCond('f', 'back\\slash')).toBe('f == "back\\\\slash"')
  })

  it('never emits a raw newline or an unbalanced quote, whatever the value', () => {
    const hostile = ['"', '\\"', '\n| filter true', '*"*', '"; drop', ' ', '`', '}', ')']
    for (const v of hostile) {
      for (const stage of attrStages([{ field: 'f', value: v }, { field: 'g', value: v, neg: true }])) {
        expect(stage).not.toContain('\n')
        // every quote that is not escaped opens or closes a literal: they pair up
        expect(stage.match(/(?<!\\)(?:\\\\)*"/g)?.length ?? 0).toSatisfy((n: number) => n % 2 === 0)
      }
    }
  })
})

describe('parseAttr / serializeAttr', () => {
  it('parses a scalar filter', () => {
    expect(parseAttr('k8s.namespace.name=prod')).toEqual({ field: 'k8s.namespace.name', value: 'prod', neg: false })
  })

  it('parses negation', () => {
    expect(parseAttr('-primary_tags.team=payments')).toEqual({ field: 'primary_tags.team', value: 'payments', neg: true })
  })

  it('ends the field at the first = outside brackets', () => {
    expect(parseAttr(`${LABEL}=checkout`)).toEqual({ field: LABEL, value: 'checkout', neg: false })
    expect(parseAttr('tags:k8s.annotations[a=b]=c')).toEqual({ field: 'tags:k8s.annotations[a=b]', value: 'c', neg: false })
    expect(parseAttr('f=a=b')).toEqual({ field: 'f', value: 'a=b', neg: false })
  })

  it('keeps empty values and the "not set" marker', () => {
    expect(parseAttr('f=')).toEqual({ field: 'f', value: '', neg: false })
    expect(parseAttr(`f=${UNSET}`)).toEqual({ field: 'f', value: UNSET, neg: false })
  })

  it('rejects input without a field or an =', () => {
    expect(parseAttr('=x')).toBeNull()
    expect(parseAttr('-=x')).toBeNull()
    expect(parseAttr('noequals')).toBeNull()
    expect(parseAttr('tags:x[a=b')).toBeNull() // bracket never closes
  })

  it('round-trips through serializeAttr', () => {
    for (const s of ['f=v', '-f=v', `${LABEL}=checkout`, 'tags:k8s.annotations[a=b]=c=d', `-tags:aws[Name]=${UNSET}`, 'primary_tags.team=a b&c#d']) {
      expect(serializeAttr(parseAttr(s)!)).toBe(s)
    }
  })

  it('round-trips through URL encoding and drops invalid entries', () => {
    const p = new URLSearchParams()
    p.append('a', `${LABEL}=checkout`)
    p.append('a', 'f`x=evil')
    p.append('a', '-primary_tags.team=a&b')
    p.append('f', 'ns:prod') // other params are ignored
    const back = parseAttrs(new URLSearchParams(p.toString()))
    expect(back).toEqual([
      { field: LABEL, value: 'checkout', neg: false },
      { field: 'primary_tags.team', value: 'a&b', neg: true },
    ])
  })

  it('compares filters by field, value and polarity', () => {
    expect(sameAttr({ field: 'f', value: 'v' }, { field: 'f', value: 'v', neg: false })).toBe(true)
    expect(sameAttr({ field: 'f', value: 'v' }, { field: 'f', value: 'v', neg: true })).toBe(false)
  })
})

describe('attrStages / attrCondition', () => {
  const NS = 'k8s.namespace.name'

  it('groups by field and polarity in first-seen order', () => {
    const g = groupAttrs([
      { field: 'a', value: '1' },
      { field: 'b', value: '2' },
      { field: 'a', value: '3' },
      { field: 'a', value: '4', neg: true },
    ])
    expect(g).toEqual([
      { field: 'a', neg: false, values: ['1', '3'] },
      { field: 'b', neg: false, values: ['2'] },
      { field: 'a', neg: true, values: ['4'] },
    ])
  })

  it('emits one equality for one value', () => {
    expect(attrStages([{ field: NS, value: 'prod' }])).toEqual([`| filter ${NS} == "prod"`])
  })

  it('ORs plain values of one field into a single in()', () => {
    expect(attrStages([{ field: NS, value: 'a' }, { field: NS, value: 'b' }])).toEqual([`| filter in(${NS}, {"a", "b"})`])
  })

  it('keeps typed legs beside plain values', () => {
    expect(attrStages([{ field: 'f', value: 'a' }, { field: 'f', value: '42' }])).toEqual(['| filter (f == "a" or (f == 42 or f == "42"))'])
    expect(attrStages([{ field: 'f', value: 'a' }, { field: 'f', value: 'b' }, { field: 'f', value: UNSET }])).toEqual([
      '| filter (in(f, {"a", "b"}) or isNull(f))',
    ])
  })

  it('ANDs different fields as separate stages', () => {
    expect(attrStages([{ field: NS, value: 'prod' }, { field: LABEL, value: 'web' }])).toEqual([`| filter ${NS} == "prod"`, `| filter ${LABEL_EXPR} == "web"`])
  })

  it('excludes with filterOut, which keeps records without the field', () => {
    expect(attrStages([{ field: NS, value: 'kube-system', neg: true }])).toEqual([`| filterOut ${NS} == "kube-system"`])
  })

  it('skips the excepted field (a field’s own counts ignore its own filter)', () => {
    expect(attrStages([{ field: NS, value: 'a' }, { field: 'g', value: 'b' }], NS)).toEqual(['| filter g == "b"'])
  })

  it('builds one condition for timeseries filter:{}', () => {
    expect(attrCondition([])).toBe('')
    expect(attrCondition([{ field: 'a', value: 'x' }, { field: 'b', value: 'y' }])).toBe('a == "x" and b == "y"')
  })

  it('keeps unset records on exclusion, like filterOut', () => {
    expect(attrCondition([{ field: 'primary_tags.team', value: 'x', neg: true }])).toBe('(not(primary_tags.team == "x") or isNull(primary_tags.team))')
    expect(attrCondition([{ field: LABEL, value: 'x', neg: true }])).toBe(`(not(${LABEL_EXPR} == "x") or isNull(${LABEL_EXPR}))`)
  })
})

describe('withAttrs', () => {
  const q = 'smartscapeNodes "HOST"\n| fields id, name\n| limit 5'

  it('injects stages right after the source line, before fields drops the maps', () => {
    expect(withAttrs(q, [{ field: LABEL, value: 'web' }])).toBe(`smartscapeNodes "HOST"\n| filter ${LABEL_EXPR} == "web"\n| fields id, name\n| limit 5`)
  })

  it('leaves the query untouched without filters', () => {
    expect(withAttrs(q, [])).toBe(q)
  })
})

describe('sources and queries', () => {
  it('builds smartscapeNodes heads for one or several types', () => {
    expect(nodesSource('HOST')).toEqual({ kind: 'nodes', head: 'smartscapeNodes "HOST"' })
    expect(nodesSource(['K8S_DEPLOYMENT', 'K8S_STATEFULSET']).head).toBe('smartscapeNodes "K8S_DEPLOYMENT", "K8S_STATEFULSET"')
  })

  it('samples nodes without their manifests', () => {
    expect(discoveryQuery(nodesSource('HOST'))).toBe('smartscapeNodes "HOST"\n| fieldsRemove k8s.object, aws.object, azure.object, gcp.object, references\n| limit 300')
  })

  it('samples the service metrics', () => {
    const q = discoveryQuery({ kind: 'service-metrics', head: '' })
    expect(q.split('\n')[0]).toBe('metrics')
    expect(q).toContain('"dt.service.request.count"')
    expect(q.endsWith('| limit 300')).toBe(true)
  })

  const filters: AttrFilter[] = [
    { field: LABEL, value: 'web' },
    { field: 'k8s.namespace.name', value: 'prod' },
  ]

  it('counts a node field’s values under every other filter but its own', () => {
    expect(valuesQuery(nodesSource('K8S_POD'), LABEL, filters)).toBe(
      ['smartscapeNodes "K8S_POD"', '| filter k8s.namespace.name == "prod"', `| fieldsSummary ${LABEL_EXPR}, topValues: ${TOP_VALUES}`].join('\n'),
    )
  })

  it('counts distinct services per metric dimension value', () => {
    const src: AttrSource = { kind: 'service-metrics', head: '' }
    const q = valuesQuery(src, 'primary_tags.team', [...filters, { field: 'primary_tags.team', value: 'x' }])
    expect(q).not.toContain('primary_tags.team == "x"')
    expect(q).toContain('| filter k8s.namespace.name == "prod"')
    expect(q).toContain('| summarize n = countDistinct(dt.smartscape.service), by:{v = primary_tags.team}')
    expect(q.endsWith(`| limit ${TOP_VALUES}`)).toBe(true)
  })
})

describe('parseDiscovery', () => {
  it('collects scalars and tag-map keys with their coverage, skipping identities and plumbing', () => {
    const records = [
      {
        id: 'HOST-0000000000000001',
        id_classic: 'HOST-0000000000000002',
        name: 'host-a',
        'os.type': 'LINUX',
        lifetime: { start: 1, end: 2 },
        'dt.smartscape.host': 'HOST-0000000000000001',
        ip: ['10.0.0.1'],
        empty: '',
        missing: null,
        'tags:k8s.labels': { app: 'web', tier: 'front' },
      },
      { id: 'HOST-0000000000000003', name: 'host-b', 'tags:k8s.labels': { app: 'api' }, 'tags:aws': null },
    ]
    const got = Object.fromEntries(parseDiscovery(records).map((c) => [c.field, c]))
    expect(Object.keys(got).sort()).toEqual(['name', 'os.type', 'tags:k8s.labels[app]', 'tags:k8s.labels[tier]'])
    expect(got['name'].coverage).toBe(1)
    expect(got['os.type'].coverage).toBe(0.5)
    expect(got['tags:k8s.labels[app]']).toEqual({ field: 'tags:k8s.labels[app]', group: 'Kubernetes labels', kind: 'label', name: 'app', coverage: 1 })
    expect(got['tags:k8s.labels[tier]'].coverage).toBe(0.5)
  })

  it('returns nothing for an empty sample', () => {
    expect(parseDiscovery([])).toEqual([])
  })
})

describe('parseValues', () => {
  it('reads fieldsSummary output, mapping empty values to "not set"', () => {
    const got = parseValues(nodesSource('HOST'), [
      {
        count: '12',
        values: [
          { value: 'a', count: '7' },
          { value: null, count: 5 },
          { value: { nested: true }, count: 1 },
        ],
      },
    ])
    expect(got).toEqual({
      values: [
        { value: 'a', count: 7 },
        { value: UNSET, count: 5 },
        { value: '{"nested":true}', count: 1 },
      ],
      total: 12,
    })
    expect(parseValues(nodesSource('HOST'), [])).toEqual({ values: [] })
  })

  it('reads the service-metrics summarize output', () => {
    expect(
      parseValues({ kind: 'service-metrics', head: '' }, [
        { v: 'team-a', n: '3' },
        { v: null, n: 2 },
      ]),
    ).toEqual({ values: [{ value: 'team-a', count: 3 }, { value: UNSET, count: 2 }] })
  })
})

describe('describeField / groupRank', () => {
  it('names tag-map keys by their context', () => {
    expect(describeField(LABEL)).toEqual({ group: 'Kubernetes labels', kind: 'label', name: 'app.kubernetes.io/name' })
    expect(describeField('tags:aws[Name]')).toEqual({ group: 'AWS tags', kind: 'AWS tag', name: 'Name' })
    expect(describeField('tags:custom_ctx[k]')).toEqual({ group: 'custom_ctx tags', kind: 'custom_ctx tag', name: 'k' })
  })

  it('names primary tags, namespace labels and raw attributes', () => {
    expect(describeField('primary_tags.team')).toEqual({ group: PRIMARY_TAGS, kind: 'primary tag', name: 'team' })
    expect(describeField('k8s.namespace.label.env')).toEqual({ group: NS_LABELS, kind: 'namespace label', name: 'env' })
    expect(describeField('os.type')).toEqual({ group: ATTRIBUTES, kind: '', name: 'os.type' })
  })

  it('ranks primary tags, then the common tag contexts, then the rest', () => {
    const groups = [ATTRIBUTES, 'custom tags', NS_LABELS, 'Kubernetes annotations', 'GCP labels', 'Azure tags', 'AWS tags', 'Kubernetes labels', PRIMARY_TAGS]
    expect([...groups].sort((a, b) => groupRank(a) - groupRank(b))).toEqual([
      PRIMARY_TAGS,
      'Kubernetes labels',
      'AWS tags',
      'Azure tags',
      'GCP labels',
      'Kubernetes annotations',
      'custom tags',
      NS_LABELS,
      ATTRIBUTES,
    ])
  })
})
