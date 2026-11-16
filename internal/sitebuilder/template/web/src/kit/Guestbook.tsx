import { useEffect, useState } from 'react'
import { words } from './words'

type Entry = { name: string; message: string; at: string }

type GuestbookProps = {
  /** The guest book form's name (marked as a guest book in the brief). Its SiteForm uses the fields "name" and "message". */
  form: string
  className?: string
}

/** The messages visitors left in a guest book form, newest first. Updates when a message is sent. */
export function Guestbook({ form, className }: GuestbookProps) {
  const [entries, setEntries] = useState<Entry[] | null>(null)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let active = true
    const load = () =>
      fetch(`/api/forms/${form}/public`)
        .then((response) => (response.ok ? response.json() : Promise.reject(new Error(String(response.status)))))
        .then((data: { entries?: Entry[] }) => {
          if (!active) return
          setEntries(data.entries ?? [])
          setFailed(false)
        })
        .catch(() => {
          if (active) setFailed(true)
        })
    load()
    const sent = (event: Event) => {
      if ((event as CustomEvent<string>).detail === form) load()
    }
    window.addEventListener('site-form-sent', sent)
    return () => {
      active = false
      window.removeEventListener('site-form-sent', sent)
    }
  }, [form])

  if (failed) return <p className='text-sm text-muted'>{words.guestbookFailed}</p>
  if (entries === null) return <p className='text-sm text-muted'>{words.guestbookLoading}</p>
  if (entries.length === 0) return <p className='text-sm text-muted'>{words.guestbookEmpty}</p>
  return (
    <ul className={className ?? 'grid gap-4'} aria-live='polite'>
      {entries.map((entry, index) => (
        <li key={`${entry.at}-${index}`} className='rounded-md border border-border bg-surface p-4'>
          <p className='text-foreground'>{entry.message}</p>
          <p className='mt-2 text-sm text-muted'>{entry.name || words.guest}</p>
        </li>
      ))}
    </ul>
  )
}

export default Guestbook
