import { useEffect, useState } from 'react'
import { words } from './words'

type CountdownProps = {
  /** Local date and time of the event, e.g. "2027-02-13T08:00". */
  to: string
  className?: string
}

function left(to: string) {
  const ms = new Date(to).getTime() - Date.now()
  if (Number.isNaN(ms)) return null
  const minutes = Math.max(0, Math.floor(ms / 60000))
  return { ms, days: Math.floor(minutes / 1440), hours: Math.floor((minutes % 1440) / 60), minutes: minutes % 60 }
}

/** Days, hours and minutes until a date. It changes once a minute, without movement. */
export function Countdown({ to, className }: CountdownProps) {
  const [time, setTime] = useState(() => left(to))
  useEffect(() => {
    const timer = setInterval(() => setTime(left(to)), 60000)
    return () => clearInterval(timer)
  }, [to])
  if (!time) return null
  if (time.ms <= 0) {
    const sameDay = new Date(to).toDateString() === new Date().toDateString()
    return <p className={className ?? 'font-heading text-2xl text-foreground'}>{sameDay ? words.today : words.past}</p>
  }
  const parts: [number, string][] = [
    [time.days, words.days],
    [time.hours, words.hours],
    [time.minutes, words.minutes],
  ]
  return (
    <div className={className ?? 'flex flex-wrap justify-center gap-3'} role='timer' aria-live='off'>
      {parts.map(([value, label]) => (
        <div key={label} className='min-w-20 rounded-md border border-border bg-surface px-4 py-3 text-center'>
          <div className='font-heading text-3xl text-foreground tabular-nums'>{value}</div>
          <div className='text-xs uppercase tracking-widest text-muted'>{label}</div>
        </div>
      ))}
    </div>
  )
}

export default Countdown
