import { CalendarDays, Clock, MapPin } from 'lucide-react'
import { look } from '../look'
import { AddToCalendar } from '../kit/AddToCalendar'
import { MapLink } from '../kit/MapLink'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame } from './ui'

type Gathering = {
  /** The part of the day, e.g. "Akad nikah" or "Resepsi". */
  name: string
  /** The date as people say it, e.g. "Sabtu, 13 Februari 2027". */
  date?: string
  /** The time as people say it, e.g. "08.00 - 10.00 WIB". */
  time?: string
  /** The name of the place, e.g. "Masjid Al-Ikhlas". */
  place?: string
  /** The address, for the map link. */
  address?: string
  /** When it starts, for the calendar button: "YYYY-MM-DDTHH:MM". */
  start?: string
  /** When it ends: "YYYY-MM-DDTHH:MM". */
  end?: string
}

type EventDetailsProps = {
  /** The heading, e.g. "Waktu dan tempat". */
  title: string
  intro?: string
  /** One card per part of the day, in order. One to four. */
  events: Gathering[]
  tone?: Tone
  id?: string
}

/** The parts of an event (the ceremony, the reception), each with its date, time, place, a map link and a calendar button. */
export function EventDetails({ title, intro, events, tone = 'auto', id }: EventDetailsProps) {
  const list = (events ?? []).filter((event) => event && event.name).slice(0, 4)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const arch = look.event === 'arch'
  // An arch reads as a window only on a narrow card, so arched cards stay narrow.
  const columns =
    list.length === 1
      ? 'mx-auto max-w-md'
      : arch
        ? cx('mx-auto', list.length === 3 ? 'max-w-5xl md:grid-cols-3' : 'max-w-3xl md:grid-cols-2')
        : list.length === 3
          ? 'md:grid-cols-3'
          : 'md:grid-cols-2'
  const shape = arch ? 'rounded-t-full rounded-b-xl pt-24' : look.button === 'pill' ? 'rounded-3xl' : look.button === 'square' ? 'rounded-none' : 'rounded-xl'

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} center />
      <Stagger className={cx('grid gap-6', columns)}>
        {list.map((event) => {
          const where = event.address || event.place
          return (
            <article key={event.name} className={cx('flex h-full flex-col items-center gap-5 bg-surface px-6 pb-10 text-center text-foreground sm:px-10', arch ? '' : 'pt-10', shape, frame() || 'border border-border')}>
              <h3 className={cx('font-heading text-3xl leading-tight', look.caps && 'uppercase tracking-wide')}>{event.name}</h3>
              <span aria-hidden='true' className='h-px w-12 bg-accent' />
              <ul className='flex flex-col items-center gap-3 text-base'>
                {event.date ? (
                  <li className='flex items-center gap-2'>
                    <CalendarDays className='size-5 shrink-0 text-accent' aria-hidden='true' />
                    <span>{event.date}</span>
                  </li>
                ) : null}
                {event.time ? (
                  <li className='flex items-center gap-2'>
                    <Clock className='size-5 shrink-0 text-accent' aria-hidden='true' />
                    <span>{event.time}</span>
                  </li>
                ) : null}
                {event.place || event.address ? (
                  <li className='flex items-start gap-2'>
                    <MapPin className='mt-0.5 size-5 shrink-0 text-accent' aria-hidden='true' />
                    <span>
                      {event.place ? <span className='block font-semibold'>{event.place}</span> : null}
                      {event.address ? <span className='block text-muted'>{event.address}</span> : null}
                    </span>
                  </li>
                ) : null}
              </ul>
              <div className='mt-auto flex flex-col items-center gap-3 pt-2'>
                {where ? <MapLink address={where} /> : null}
                {event.start ? <AddToCalendar title={event.name} start={event.start} end={event.end} location={where} /> : null}
              </div>
            </article>
          )
        })}
      </Stagger>
    </Section>
  )
}

export default EventDetails
