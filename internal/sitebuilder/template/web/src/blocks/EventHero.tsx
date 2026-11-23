import { look } from '../look'
import { AddToCalendar } from '../kit/AddToCalendar'
import { Countdown } from '../kit/Countdown'
import { MapLink } from '../kit/MapLink'
import { Monogram } from '../kit/Monogram'
import { Ornament } from '../kit/Ornament'
import { Reveal } from '../kit/Reveal'
import { container, cx } from './ui'

type EventHeroProps = {
  /** The names or the event, e.g. "Raka & Nadia". It becomes the page's h1. */
  title: string
  /** The date as people say it, e.g. "Sabtu, 13 Februari 2027". */
  date: string
  /** When it starts, for the countdown and the calendar button: "YYYY-MM-DDTHH:MM". */
  start?: string
  /** When it ends: "YYYY-MM-DDTHH:MM". */
  end?: string
  /** The place, shown with a map link. */
  place?: string
  /** A short line above the names, e.g. "Undangan pernikahan". */
  label?: string
  /** Initials for the monogram, e.g. "R & N". */
  letters?: string
}

/** The opening of an invitation or event: the names, the date, a countdown and a calendar button. */
export function EventHero({ title, date, start, end, place, label, letters }: EventHeroProps) {
  const calendar = start ? <AddToCalendar title={`${label ? `${label}: ` : ''}${title}`} start={start} end={end} location={place} /> : null
  const countdown = start ? <Countdown to={start} /> : null
  const where = place ? (
    <div className='flex flex-col items-center gap-2'>
      <p className='text-lg'>{place}</p>
      <MapLink address={place} />
    </div>
  ) : null

  if (look.event === 'band') {
    return (
      <section className='bg-primary text-primary-foreground'>
        <div className={cx(container, 'flex flex-col items-center gap-6 py-20 text-center md:py-28')}>
          <Reveal from='up' className='flex flex-col items-center gap-5'>
            {label ? <p className='text-lg text-accent'>{label}</p> : null}
            <h1 className='font-heading text-5xl leading-tight tracking-tight md:text-7xl'>{title}</h1>
            <p className='text-xl md:text-2xl'>{date}</p>
          </Reveal>
          <Ornament kind='diamond' className='mx-auto w-full max-w-xs text-accent' />
          <Reveal from='up' delay={150} className='flex flex-col items-center gap-6'>
            {countdown}
            {calendar}
          </Reveal>
          {place ? (
            <Reveal from='fade' delay={250} className='flex flex-col items-center gap-2'>
              <p className='text-lg'>{place}</p>
              <MapLink address={place} className='text-sm underline underline-offset-4' />
            </Reveal>
          ) : null}
        </div>
      </section>
    )
  }

  if (look.event === 'arch') {
    return (
      <section className='bg-background text-foreground'>
        <div className={cx(container, 'flex flex-col items-center gap-8 py-20 text-center md:py-28')}>
          {letters ? (
            <Reveal from='fade'>
              <Monogram letters={letters} frame='arch' />
            </Reveal>
          ) : null}
          <Reveal from='up' delay={120} className='flex flex-col items-center gap-5'>
            {label ? <p className='text-sm uppercase tracking-widest text-muted'>{label}</p> : null}
            <h1 className='font-heading text-5xl font-normal italic leading-tight tracking-tight md:text-7xl'>{title}</h1>
            <p className='text-sm uppercase tracking-widest'>{date}</p>
          </Reveal>
          <Ornament kind='flourish' />
          <Reveal from='up' delay={220} className='flex flex-col items-center gap-6'>
            {countdown}
            {calendar}
          </Reveal>
          {where}
        </div>
      </section>
    )
  }

  return (
    <section className='bg-background text-foreground'>
      <div className={cx(container, 'grid gap-12 py-20 md:grid-cols-12 md:py-28')}>
        <Reveal from='up' className='md:col-span-7'>
          {label ? <p className='text-lg text-accent'>{label}</p> : null}
          <h1 className={cx('mt-3 font-heading text-5xl leading-none tracking-tight md:text-7xl', look.caps && 'uppercase')}>{title}</h1>
          <p className='mt-6 text-2xl'>{date}</p>
          {place ? <p className='mt-2 text-lg text-muted'>{place}</p> : null}
        </Reveal>
        <Reveal from='up' delay={150} className='flex flex-col items-start gap-6 md:col-span-5 md:items-end md:justify-end'>
          {countdown}
          {calendar}
          {place ? <MapLink address={place} /> : null}
        </Reveal>
      </div>
    </section>
  )
}

export default EventHero
