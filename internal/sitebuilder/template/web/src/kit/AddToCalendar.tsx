import { CalendarDays } from 'lucide-react'
import { words } from './words'

type AddToCalendarProps = {
  title: string
  /** Local start, e.g. "2027-02-13T08:00". */
  start: string
  /** Local end; two hours after the start when left out. */
  end?: string
  location?: string
  details?: string
  label?: string
  className?: string
}

function stamp(value: Date) {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${value.getFullYear()}${pad(value.getMonth() + 1)}${pad(value.getDate())}T${pad(value.getHours())}${pad(value.getMinutes())}00`
}

function escapeText(text: string) {
  return text.replace(/[\\,;]/g, (c) => `\\${c}`).replace(/\n/g, '\\n')
}

/** Saves the event to the visitor's calendar: a calendar file made in the browser, no outside service. */
export function AddToCalendar({ title, start, end, location, details, label, className }: AddToCalendarProps) {
  function save() {
    const from = new Date(start)
    if (Number.isNaN(from.getTime())) return
    const to = end ? new Date(end) : new Date(from.getTime() + 2 * 3600000)
    const lines = [
      'BEGIN:VCALENDAR',
      'VERSION:2.0',
      'PRODID:-//site//calendar//EN',
      'BEGIN:VEVENT',
      `UID:${stamp(from)}-${Math.random().toString(36).slice(2)}@site`,
      `DTSTAMP:${stamp(new Date())}`,
      `DTSTART:${stamp(from)}`,
      `DTEND:${stamp(to)}`,
      `SUMMARY:${escapeText(title)}`,
      location ? `LOCATION:${escapeText(location)}` : '',
      details ? `DESCRIPTION:${escapeText(details)}` : '',
      'END:VEVENT',
      'END:VCALENDAR',
    ].filter(Boolean)
    const url = URL.createObjectURL(new Blob([lines.join('\r\n')], { type: 'text/calendar' }))
    const link = document.createElement('a')
    link.href = url
    link.download = 'acara.ics'
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <button
      type='button'
      onClick={save}
      className={className ?? 'inline-flex items-center gap-2 rounded-md border border-primary px-5 py-2.5 text-primary transition-colors hover:bg-primary hover:text-primary-foreground'}
    >
      <CalendarDays className='size-4' aria-hidden='true' />
      {label ?? words.calendar}
    </button>
  )
}

export default AddToCalendar
