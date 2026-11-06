// Search every conversation you are in (or one), with filters, and Saved.
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useLive } from '../lib/live'
import { listTime, preview } from '../lib/chat'
import type { Message } from '../lib/types'
import { Avatar, Empty, Loading, Segmented, useAsync, useDebounce } from '../components/ui'
import { Select } from '../components/Select'
import { Icon } from '../components/icons'

const KINDS = [{ value: '', label: 'All' }, { value: 'photos', label: 'Photos' }, { value: 'files', label: 'Files' }, { value: 'links', label: 'Links' }, { value: 'cards', label: 'From apps' }, { value: 'polls', label: 'Polls' }, { value: 'voice', label: 'Voice' }]

function Result({ m }: { m: Message }) {
  return (
    <Link to={`/c/${m.conversation_id}?m=${m.id}`} className="result">
      <Avatar name={m.author_name} size={30} />
      <span className="grow">
        <span className="result-top"><b>{m.author_name || 'Pocket AI'}</b><span className="muted small">in {m.conversation_title}</span><span className="spacer" /><span className="muted small tnum">{listTime(m.created_at)}</span></span>
        <span className="result-text">{m.snippet || preview(m)}</span>
      </span>
    </Link>
  )
}

export default function Search() {
  const [params, setParams] = useSearchParams()
  const { conversations, people } = useLive()
  const [q, setQ] = useState(params.get('q') || '')
  const [kind, setKind] = useState(params.get('kind') || '')
  const [conv, setConv] = useState(params.get('conversation') || '')
  const [from, setFrom] = useState('')
  const dq = useDebounce(q, 250)
  const [res, setRes] = useState<Message[] | null>(null)
  useEffect(() => {
    const p = new URLSearchParams()
    if (dq) p.set('q', dq)
    if (kind) p.set('kind', kind)
    if (conv) p.set('conversation', conv)
    if (from) p.set('from', from)
    setParams(p, { replace: true })
    if (!dq && !kind && !from) { setRes(null); return }
    api.get<{ results: Message[] }>(`${V1}/search?${p}`).then(r => setRes(r.results)).catch(() => setRes([]))
  }, [dq, kind, conv, from]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="page narrow">
      <label className="search-field big"><Icon name="search" size={18} /><input value={q} onChange={e => setQ(e.target.value)} placeholder="Search your messages" autoFocus /></label>
      <div className="search-filters">
        <Segmented size="sm" value={kind} onChange={setKind} options={KINDS} />
        <Select ariaLabel="Conversation" value={conv} onChange={setConv} options={[{ value: '', label: 'Every conversation' }, ...(conversations || []).map(c => ({ value: String(c.id), label: c.name }))]} />
        <Select ariaLabel="From" value={from} onChange={setFrom} options={[{ value: '', label: 'From anyone' }, ...people.map(p => ({ value: String(p.id), label: p.name || p.email }))]} />
      </div>
      {res === null ? <Empty icon="search" title="Find anything said">Words, names, a shop, a place — accents do not matter. Filters find photos, files, links and cards shared from your apps.</Empty>
        : res.length === 0 ? <Empty icon="search" title="Nothing found" />
        : <div className="results">{res.map(m => <Result key={m.id} m={m} />)}</div>}
    </div>
  )
}

export function Saved() {
  const { data, loading } = useAsync(() => api.get<Message[]>(`${V1}/saved`), [])
  if (loading && !data) return <Loading />
  return (
    <div className="page narrow">
      <h2 className="page-title"><Icon name="star" size={18} />Saved</h2>
      {!data?.length ? <Empty icon="star" title="Nothing saved yet">Keep a message for later: ⋯ → Save. Only you see what you saved.</Empty>
        : <div className="results">{data.map(m => <Result key={m.id} m={m} />)}</div>}
    </div>
  )
}
