import { useEffect, useRef, useState } from 'react'
import { MailOpen } from 'lucide-react'
import { reducedMotion } from './motion'
import { Ornament } from './Ornament'
import { words } from './words'

type InvitationCoverProps = {
  /** A short line above the names, e.g. "Undangan Pernikahan". */
  label?: string
  /** The names or the name of the event, e.g. "Raka & Nadia". */
  title: string
  /** The date in words, e.g. "Sabtu, 13 Februari 2027". */
  date?: string
  /** The words on the button. Default "Buka undangan" / "Open invitation". */
  buttonLabel?: string
}

const OPENED = 'invitation-opened'

/**
 * The cover a guest sees first on an invitation, covering the whole screen: a line,
 * the names, the date and, when the link ends in ?to=Name, "Dear Name". Pressing the
 * button slides the cover up and sends the window event 'invitation-open' (Confetti
 * can wait for it). It opens only once per visit. The page is rendered underneath the
 * whole time, so nothing waits for it.
 */
export function InvitationCover({ label, title, date, buttonLabel }: InvitationCoverProps) {
  const [state, setState] = useState<'closed' | 'opening' | 'open'>('closed')
  const [guest, setGuest] = useState('')
  const button = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    try {
      if (sessionStorage.getItem(OPENED)) {
        setState('open')
        return
      }
    } catch {
      // Without storage the cover simply shows again on the next visit.
    }
    const name = new URLSearchParams(window.location.search).get('to') ?? ''
    setGuest(name.replace(/[<>]/g, '').trim().slice(0, 60))
    button.current?.focus()
  }, [])

  // The page behind does not scroll while the cover is there.
  useEffect(() => {
    if (state === 'open') return
    const before = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => {
      document.body.style.overflow = before
    }
  }, [state])

  const open = () => {
    try {
      sessionStorage.setItem(OPENED, '1')
    } catch {
      // Nothing to remember without storage.
    }
    window.dispatchEvent(new Event('invitation-open'))
    if (reducedMotion()) {
      setState('open')
      return
    }
    setState('opening')
    setTimeout(() => setState('open'), 900)
  }

  if (state === 'open') return null
  return (
    <div
      role='dialog'
      aria-modal='true'
      aria-label={title}
      className={`cover-slide fixed inset-0 z-50 flex items-center justify-center overflow-y-auto bg-surface px-6 py-10 ${
        state === 'opening' ? 'cover-gone' : ''
      }`}
    >
      <div className='w-full max-w-md rounded-lg border border-border bg-background p-2'>
        <div className='flex flex-col items-center gap-5 rounded-md border border-accent/40 px-6 py-12 text-center'>
          {label ? <p className='text-xs uppercase tracking-widest text-muted'>{label}</p> : null}
          <h2 className='font-heading text-4xl leading-tight text-foreground md:text-5xl'>{title}</h2>
          <Ornament kind='flourish' />
          {date ? <p className='text-sm text-foreground'>{date}</p> : null}
          {guest ? (
            <div className='space-y-1'>
              <p className='text-xs text-muted'>{words.dear}</p>
              <p className='font-heading text-xl text-foreground'>{guest}</p>
            </div>
          ) : null}
          <button
            ref={button}
            type='button'
            onClick={open}
            className='mt-2 inline-flex items-center gap-2 rounded-md bg-primary px-6 py-3 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary'
          >
            <MailOpen className='size-4' />
            {buttonLabel || words.openInvitation}
          </button>
        </div>
      </div>
    </div>
  )
}

export default InvitationCover
