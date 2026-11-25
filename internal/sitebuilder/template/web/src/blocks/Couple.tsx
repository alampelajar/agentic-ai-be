import { look } from '../look'
import { Reveal } from '../kit/Reveal'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame, mutedClass } from './ui'
import { firstPhoto } from './photos'

type Person = {
  /** The name people call them, e.g. "Nadia". */
  name: string
  /** The full name, only when the brief gives it. */
  fullName?: string
  /** One line about them from the brief, e.g. "Putri pertama dari Bapak Hasan dan Ibu Rina". */
  about?: string
  /** The place of the owner's photo of this person in site.images (0 is the first), only when its alt text says it shows them. */
  photo?: number
}

type CoupleProps = {
  /** The heading, e.g. "Mempelai" or "The couple". */
  title: string
  intro?: string
  /** One or two people, in the order the brief names them. */
  people: Person[]
  /** The word or sign between the two, "&" by default. */
  joiner?: string
  tone?: Tone
  id?: string
}

function Portrait({ person, arch }: { person: Person; arch: boolean }) {
  const photo = person.photo === undefined ? null : firstPhoto(person.photo)
  const shape = arch ? 'rounded-t-full rounded-b-lg' : look.button === 'pill' ? 'rounded-full' : 'rounded-lg'
  if (photo) {
    return <img src={photo.src} alt={photo.alt} loading='lazy' className={cx('aspect-4/5 w-44 object-cover sm:w-52', shape)} />
  }
  // No photo: the first letter, large, in a frame of the look's shape.
  return (
    <span aria-hidden='true' className={cx('flex aspect-4/5 w-36 items-center justify-center bg-surface font-heading text-6xl text-accent sm:w-40', shape, frame() || 'border border-border')}>
      {person.name.trim().charAt(0).toUpperCase()}
    </span>
  )
}

function Card({ person, arch, tone }: { person: Person; arch: boolean; tone: Tone }) {
  return (
    <div className='flex flex-col items-center gap-5 text-center'>
      <Portrait person={person} arch={arch} />
      <div>
        <h3 className={cx('font-heading text-3xl leading-tight md:text-4xl', look.caps && 'uppercase')}>{person.name}</h3>
        {person.fullName && person.fullName !== person.name ? <p className='mt-2 text-lg'>{person.fullName}</p> : null}
        {person.about ? <p className={cx('mx-auto mt-3 max-w-xs leading-relaxed', mutedClass(tone))}>{person.about}</p> : null}
      </div>
    </div>
  )
}

/** The two people of a wedding or engagement, each with a photo or their initial, their full name and one line about them. */
export function Couple({ title, intro, people, joiner = '&', tone = 'auto', id }: CoupleProps) {
  const list = (people ?? []).filter((person) => person && person.name).slice(0, 2)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const arch = look.event === 'arch'

  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} center />
      <div className='flex flex-col items-center justify-center gap-10 md:flex-row md:items-start md:gap-16'>
        <Reveal from={list.length > 1 ? 'left' : 'up'}>
          <Card person={list[0]} arch={arch} tone={tone} />
        </Reveal>
        {list.length > 1 ? (
          <>
            <div className='flex flex-col items-center gap-3 md:self-center' aria-hidden='true'>
              {look.event === 'band' ? null : <span className='hidden h-16 w-px bg-accent md:block' />}
              <span className='font-heading text-5xl text-accent md:text-6xl'>{joiner}</span>
            </div>
            <Reveal from='right'>
              <Card person={list[1]} arch={arch} tone={tone} />
            </Reveal>
          </>
        ) : null}
      </div>
    </Section>
  )
}

export default Couple
