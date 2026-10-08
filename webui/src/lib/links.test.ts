import { describe, expect, it } from 'vitest'
import { parseAttrs } from './attrs'
import { listHref } from './links'

/** The attribute filters a list link carries, read back the way the list page reads them. */
const filterOf = (href: string) => parseAttrs(new URLSearchParams(href.slice(href.indexOf('?') + 1)))

describe('listHref', () => {
  const LABEL = 'tags:k8s.labels[app.kubernetes.io/name]'

  it('sends hosts to the Hosts page', () => {
    expect(listHref('HOST', 'tags:aws[Name]', 'web')).toBe('/hosts?a=tags%3Aaws%5BName%5D%3Dweb')
  })

  it('sends each Kubernetes kind to its view', () => {
    const cases: [string, string][] = [
      ['K8S_POD', 'pods'],
      ['K8S_DEPLOYMENT', 'workloads'],
      ['K8S_STATEFULSET', 'workloads'],
      ['K8S_DAEMONSET', 'workloads'],
      ['K8S_NODE', 'nodes'],
      ['K8S_NAMESPACE', 'namespaces'],
    ]
    for (const [type, view] of cases) expect(listHref(type, LABEL, 'web').startsWith(`/k8s?view=${view}&a=`)).toBe(true)
  })

  it('sends service primary tags to Services, which filters on metric dimensions', () => {
    expect(listHref('SERVICE', 'primary_tags.team', 'payments')).toBe('/services?a=primary_tags.team%3Dpayments')
  })

  it('sends service tag maps and every other type to the Smartscape instance list', () => {
    expect(listHref('SERVICE', 'tags:environment[stage]', 'prod').startsWith('/smartscape?type=SERVICE&a=')).toBe(true)
    expect(listHref('AWS_EC2_INSTANCE', 'tags:aws[team]', 'x').startsWith('/smartscape?type=AWS_EC2_INSTANCE&a=')).toBe(true)
  })

  it('round-trips awkward values through the URL', () => {
    for (const value of ['a b', 'a&b=c', '#hash', '100%', 'x/y?z', 'ünïcode']) {
      expect(filterOf(listHref('K8S_POD', LABEL, value))).toEqual([{ field: LABEL, value, neg: false }])
    }
  })
})
