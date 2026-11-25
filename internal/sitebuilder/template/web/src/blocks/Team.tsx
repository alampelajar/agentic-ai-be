import { look } from '../look'
import { Stagger } from '../kit/Stagger'
import { slug } from './Features'
import type { Tone } from './types'
import { Section, SectionHeading, cx, frame, mutedClass } from './ui'
import { firstPhoto } from './photos'

type Member = {
  /** Their name as the brief gives it. */
  name: string
  /** What they do, e.g. "Kepala dapur". */
  role?: string
  /** One sentence about them from the brief. */
  text?: string
  /** The place of the owner's photo of this person in site.images (0 is the first), only when its alt text says it shows them. */
  photo?: number
}

type TeamProps = {
  /** The heading, e.g. "Tim kami". */
  title: string
  intro?: string
  /** Only people the brief names. One to eight. */
  people: Member[]
  tone?: Tone
  id?: string
}

/** The people behind the business, each with a photo or their initial, their role and one line. Only people the brief names. */
export function Team({ title, intro, people, tone = 'auto', id }: TeamProps) {
  const list = (people ?? []).filter((person) => person && person.name).slice(0, 8)
  if (list.length === 0) return null
  const headingId = `${id ?? slug(title)}-title`
  const round = look.button === 'pill'
  const photoOf = (person: Member) => (person.photo === undefined ? null : firstPhoto(person.photo))
  const columns = list.length <= 2 ? 'mx-auto max-w-3xl sm:grid-cols-2' : list.length === 3 ? 'sm:grid-cols-3' : 'sm:grid-cols-2 lg:grid-cols-4'
  const words = (person: Member) => (
    <>
      <h3 className={cx('font-heading text-xl md:text-2xl', look.caps && 'uppercase')}>{person.name}</h3>
      {person.role ? <p className='mt-1 font-semibold'>{person.role}</p> : null}
      {person.text ? <p className={cx('mt-2 leading-relaxed', mutedClass(tone))}>{person.text}</p> : null}
    </>
  )

  // Large portraits only when everyone has a photo; otherwise one even row of cards,
  // each with a small photo or the initial, so nobody's box looks empty.
  if (list.every((person) => photoOf(person))) {
    return (
      <Section tone={tone} id={id} labelledBy={headingId}>
        <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
        <Stagger className={cx('grid gap-x-6 gap-y-12', columns)}>
          {list.map((person) => {
            const photo = photoOf(person)!
            return (
              <article key={person.name} className={cx('flex flex-col gap-4', look.centered && 'items-center text-center')}>
                <img src={photo.src} alt={photo.alt} loading='lazy' className={cx('aspect-square w-full object-cover', round ? 'rounded-full' : 'rounded-lg')} />
                <div>{words(person)}</div>
              </article>
            )
          })}
        </Stagger>
      </Section>
    )
  }

  const shape = round ? 'rounded-3xl' : look.button === 'square' ? 'rounded-none' : 'rounded-xl'
  return (
    <Section tone={tone} id={id} labelledBy={headingId}>
      <SectionHeading id={headingId} title={title} intro={intro} tone={tone} />
      <Stagger className={cx('grid gap-6', columns)}>
        {list.map((person) => {
          const photo = photoOf(person)
          return (
            <article key={person.name} className={cx('lift flex h-full flex-col gap-5 bg-surface p-7 text-foreground', shape, frame() || 'border border-border', look.centered && 'items-center text-center')}>
              {photo ? (
                <img src={photo.src} alt={photo.alt} loading='lazy' className='size-16 rounded-full object-cover' />
              ) : (
                <span aria-hidden='true' className='flex size-16 items-center justify-center rounded-full bg-primary font-heading text-2xl text-primary-foreground'>
                  {person.name.trim().charAt(0).toUpperCase()}
                </span>
              )}
              <div>{words(person)}</div>
            </article>
          )
        })}
      </Stagger>
    </Section>
  )
}

export default Team
