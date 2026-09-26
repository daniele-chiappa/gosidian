import { describe, expect, it } from 'vitest'
import type { WindowInstance } from 'plancia'
import { queryArgFromProps, queryPropsFromArg } from '@/composables/queryArg'
import { codec } from '@/composables/planciaKey'

const win = (type: string, props: Record<string, unknown> = {}): WindowInstance =>
  ({ type, props }) as WindowInstance

const props = {
  project: 'gosidian',
  where: [
    { field: 'type', op: 'eq', value: 'plan' },
    { field: 'status', op: 'in', value: ['draft', 'in-progress'] },
    { field: 'importance', op: 'gte', value: '3' },
    { field: 'description', op: 'exists' },
  ],
  sort: 'updated',
  order: 'desc',
  fields: ['status', 'updated'],
}

describe('query window URL arg', () => {
  it('round-trips a whole query', () => {
    const arg = queryArgFromProps(props)
    expect(arg).not.toBeNull()
    expect(queryPropsFromArg(arg)).toEqual(props)
  })

  it('drops conditions without a field or with an unknown operator', () => {
    const arg = queryArgFromProps({
      where: [
        { field: ' ', op: 'eq', value: 'x' },
        { field: 'status', op: 'like', value: 'd%' },
        { field: 'type', op: 'eq', value: 'plan' },
      ],
    })
    expect(queryPropsFromArg(arg)).toEqual({ where: [{ field: 'type', op: 'eq', value: 'plan' }] })
  })

  it('has no arg without a usable condition, and no props from a bad arg', () => {
    expect(queryArgFromProps({})).toBeNull()
    expect(queryArgFromProps({ where: [{ field: '', op: 'eq' }] })).toBeNull()
    for (const bad of [null, '', 'not json', '{"w":"x"}', '{"w":[["type","like","x"]]}', '[1,2]']) {
      expect(queryPropsFromArg(bad)).toEqual({})
    }
  })

  it('stays one window keyed "query" in the plancia codec', () => {
    const tok = codec.encode(win('query', props))!
    expect(tok.startsWith('query:')).toBe(true)
    const spec = codec.decode(tok)!
    expect(spec.type).toBe('query')
    expect(spec.key).toBe('query')
    expect(spec.props).toEqual(props)
    expect(codec.key({ type: 'query', props })).toBe('query')
    expect(codec.encode(win('query'))).toBe('query')
  })
})
